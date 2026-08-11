// Package pubsub provides ssoosshd's internal message-broker primitives,
// built on Watermill (https://github.com/ThreeDotsLabs/watermill).
//
// This is Phase 1 of docs/watermill-signer-plan.md: gochannel (in-process,
// in-memory) only — no config-driven backend selection yet, and nothing in
// the request-handling path publishes or subscribes to anything through
// this package yet. See docs/watermill-phase1-pubsub.md for the full scope
// of this phase, and docs/watermill-phase6-nats-deferred.md for where NATS
// support (and the config surface to select it) eventually goes.
package pubsub

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
)

// routerStartTimeout bounds how long Run's shutdown watcher waits for the
// router to come up before closing it anyway, so a router that never starts
// can't wedge shutdown.
const routerStartTimeout = 5 * time.Second

// PubSub holds ssoosshd's message-broker primitives: Publisher/Subscriber
// (currently always backed by an in-process gochannel.GoChannel) and a
// Router for the "start once, handle every message as it arrives" style
// consumers later phases add (the signer and listener/resolver components
// in docs/watermill-phase4-signer-listener.md). Router has no handlers
// registered on it yet in this phase.
type PubSub struct {
	Publisher  message.Publisher
	Subscriber message.Subscriber
	Router     *message.Router

	channel *gochannel.GoChannel
}

// New builds the gochannel-backed pub/sub pair and a Router ready for
// handlers to be registered on it by later phases. It does not start the
// Router — call Run for that, matching this codebase's existing
// servicerunner.Service pattern (see server/bootstrap/bootstrap.go) for
// long-running components.
func New(logger *slog.Logger) (*PubSub, error) {
	wmLogger := watermill.NewSlogLogger(logger)

	channel := gochannel.NewGoChannel(gochannel.Config{
		// Persistent deliberately left false. It looks tempting for the
		// wake topic (a subscriber attaching slightly after a fast
		// publish would still see the message), but two things make it
		// the wrong choice:
		//
		//  1. It's unnecessary there: server/service/certrequest.go's
		//     Wait already re-reads the DB right after subscribing
		//     (lookupRequest/reconcileStatus), and reconcileStatus's
		//     terminal branch already re-notifies when it finds a
		//     resolved-but-not-yet-cached status. That covers the same
		//     race Persistent would, just via one extra loop iteration
		//     instead of a replayed message.
		//  2. It's actively wrong for the future sign queue
		//     (docs/watermill-phase3-sign-queue.md): gochannel replays a
		//     Persistent topic's *entire* history to every new
		//     subscriber, not just messages it missed. A signer
		//     restarting and re-subscribing to the shared sign-queue
		//     topic would get every job ever published replayed to it,
		//     including ones long since signed — not just unconsumed
		//     ones. There's also no eviction: a Persistent topic's
		//     backlog only ever grows for the life of the process (see
		//     docs/watermill-phase2-wake-topic.md's now-resolved caveat
		//     about this).
		//
		// Safe to leave off in gochannel-only (single-process) mode:
		// Phase 4's signer registers its subscription at boot, before
		// Approve can ever publish a job, so there's no late-subscriber
		// gap to cover. Once Phase 6/NATS makes the signer a genuinely
		// separate process that can restart independently, durability
		// there is a JetStream ack/redelivery problem — a different,
		// correct mechanism, not something this flag was ever going to
		// solve properly anyway.
		Persistent: false,
	}, wmLogger)

	// CloseTimeout bounds how long Close waits for in-flight handlers.
	// Watermill's default is 30s, which overruns bootstrap's own 5s shutdown
	// budget (see shutdownManager.Run) — so a wedged handler would blow past
	// shutdown rather than being cut short by it.
	router, err := message.NewRouter(message.RouterConfig{CloseTimeout: 3 * time.Second}, wmLogger)
	if err != nil {
		return nil, fmt.Errorf("failed to create watermill router: %w", err)
	}

	// Order matters: the first middleware added is the outermost, so this is
	// dropAfterRetries(Retry(handler)) — retries happen inside, and whatever
	// error survives them is swallowed outside.
	//
	// Both halves are needed because of how gochannel handles a Nack: it
	// redelivers *immediately*, in a tight loop with no backoff. So a
	// handler that keeps failing (a wedged database, say) would spin the CPU
	// rather than retry politely, and would never stop. Retry supplies the
	// backoff; dropAfterRetries supplies the giving up.
	router.AddMiddleware(dropAfterRetries(wmLogger), middleware.Retry{
		MaxRetries:      3,
		InitialInterval: 100 * time.Millisecond,
		Multiplier:      2,
		Logger:          wmLogger,
	}.Middleware)

	return &PubSub{
		Publisher:  channel,
		Subscriber: channel,
		Router:     router,
		channel:    channel,
	}, nil
}

// dropAfterRetries returns a middleware that acknowledges a message whose
// handler still failed after the retry middleware inside it gave up, rather
// than letting the error nack.
//
// Without this, an exhausted retry nacks, and gochannel immediately
// redelivers the same message forever — turning a persistent failure into a
// busy loop that never drains. Dropping is the lesser evil, but it *is* data
// loss, so the discarded payload is logged: for a signed certificate that
// log line may be the only surviving record it existed at all.
//
// A dead-letter topic would be the better answer; it's deliberately deferred
// until there's a durable broker to put one on (see
// docs/watermill-phase6-nats-deferred.md) — a poison queue nobody consumes is
// no better than this log line.
func dropAfterRetries(logger watermill.LoggerAdapter) message.HandlerMiddleware {
	return func(next message.HandlerFunc) message.HandlerFunc {
		return func(msg *message.Message) ([]*message.Message, error) {
			produced, err := next(msg)
			if err != nil {
				logger.Error("dropping message after exhausting retries", err, watermill.LogFields{
					"message_uuid": msg.UUID,
					"payload":      string(msg.Payload),
				})
				return nil, nil
			}
			return produced, nil
		}
	}
}

// Run starts the Router, blocking until ctx is canceled or Close is
// called. Matches servicerunner.Service's signature, for use as a
// long-running component in bootstrap.Bootstrap's serviceRunners list —
// servicerunner cancels ctx to signal shutdown and expects Run to return
// promptly, but Router.Run only unblocks on an explicit Close (canceling
// its context alone stops handler processing, not Run itself — see
// watermill's message.Router.Run), so this watches ctx itself and closes
// the Router when it's done.
func (p *PubSub) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()

		// Wait for the router to actually be running before closing it.
		// Closing one that hasn't started its handlers yet doesn't return
		// early — it waits out the full CloseTimeout on handlers that will
		// never report in. That's reachable whenever ctx is already canceled
		// as Run is called (an immediate shutdown, or a test using a
		// pre-canceled context), and it stayed invisible until there were
		// handlers registered to wait for.
		select {
		case <-p.Router.Running():
		case <-time.After(routerStartTimeout):
		}

		// Best-effort; Run's own return value is what servicerunner observes.
		_ = p.Router.Close()
	}()

	return p.Router.Run(ctx)
}

// Close shuts down the Router before the underlying gochannel, so any
// in-flight handler gets a chance to finish before its transport goes
// away. Matches servicerunner.Service's signature, for use with
// bootstrap's shutdownManager.
func (p *PubSub) Close(context.Context) error {
	if err := p.Router.Close(); err != nil {
		return fmt.Errorf("failed to close watermill router: %w", err)
	}
	if err := p.channel.Close(); err != nil {
		return fmt.Errorf("failed to close gochannel pub/sub: %w", err)
	}
	return nil
}
