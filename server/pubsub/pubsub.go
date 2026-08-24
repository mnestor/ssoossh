// Package pubsub provides ssoosshd's internal message-broker primitives,
// built on Watermill (https://github.com/ThreeDotsLabs/watermill).
//
// Supports two backends: gochannel (in-process, single instance) and NATS
// (required for multi-instance deployments). See docs/signing-pipeline.md
// for how the certificate pipeline uses this.
package pubsub

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	natslib "github.com/ThreeDotsLabs/watermill-nats/v2/pkg/nats"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	natsgo "github.com/nats-io/nats.go"

	"github.com/mnestor/ssoossh/server/config"
)

// routerStartTimeout bounds how long Run's shutdown watcher waits for the
// router to come up before closing it anyway, so a router that never starts
// can't wedge shutdown.
const routerStartTimeout = 5 * time.Second

// PubSub holds ssoosshd's message-broker primitives: Publisher/Subscriber
// (backed by either gochannel for single-instance or NATS for multi-instance)
// and a Router for the "start once, handle every message as it arrives" style
// consumers (the signer and listener/resolver in docs/signing-pipeline.md).
type PubSub struct {
	Publisher  message.Publisher
	Subscriber message.Subscriber
	Router     *message.Router

	// Backend-specific: only one of these is populated depending on config
	channel        *gochannel.GoChannel
	natsPublisher  *natslib.Publisher
	natsSubscriber *natslib.Subscriber
}

// New builds a pub/sub pair and Router ready for handlers to be registered.
// Branches on config.PubSub.Backend: "gochannel" (default, in-process) or
// "nats" (multi-instance). Does not start the Router — call Run for that,
// matching this codebase's servicerunner.Service pattern.
func New(cfg *config.PubSubConfig, logger *slog.Logger) (*PubSub, error) {
	wmLogger := watermill.NewSlogLogger(logger)

	var pubsub *PubSub
	var err error

	backend := cfg.Backend
	if backend == "" {
		backend = config.PubSubBackendGoChannel
	}

	if backend == config.PubSubBackendNATS {
		pubsub, err = newNATS(cfg, wmLogger, logger)
	} else {
		pubsub, err = newGoChannel(wmLogger)
	}
	if err != nil {
		return nil, err
	}

	// Router configuration is backend-independent
	if err := pubsub.buildRouter(wmLogger); err != nil {
		return nil, err
	}

	return pubsub, nil
}

// newGoChannel builds a gochannel-backed pub/sub pair.
func newGoChannel(wmLogger watermill.LoggerAdapter) (*PubSub, error) {
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
		//     (docs/signing-pipeline.md): gochannel replays a
		//     Persistent topic's *entire* history to every new
		//     subscriber, not just messages it missed. A signer
		//     restarting and re-subscribing to the shared sign-queue
		//     topic would get every job ever published replayed to it,
		//     including ones long since signed — not just unconsumed
		//     ones. There's also no eviction: a Persistent topic's
		//     backlog only ever grows for the life of the process (see
		//     docs/signing-pipeline.md's now-resolved caveat
		//     about this).
		//
		// Safe to leave off in gochannel-only (single-process) mode:
		// the signer registers its subscription at boot, before
		// Approve can ever publish a job, so there's no late-subscriber
		// gap to cover. Once NATS makes the signer a genuinely
		// separate process that can restart independently, durability
		// there is a JetStream ack/redelivery problem — a different,
		// correct mechanism, not something this flag was ever going to
		// solve properly anyway.
		Persistent: false,
	}, wmLogger)

	return &PubSub{
		Publisher:  channel,
		Subscriber: channel,
		channel:    channel,
	}, nil
}

// buildRouter creates and configures the message router with middleware.
// This is transport-independent, used by both gochannel and NATS backends.
func (p *PubSub) buildRouter(wmLogger watermill.LoggerAdapter) error {
	// CloseTimeout bounds how long Close waits for in-flight handlers.
	// Watermill's default is 30s, which overruns bootstrap's own 5s shutdown
	// budget (see shutdownManager.Run) — so a wedged handler would blow past
	// shutdown rather than being cut short by it.
	router, err := message.NewRouter(message.RouterConfig{CloseTimeout: 3 * time.Second}, wmLogger)
	if err != nil {
		// not covered: RouterConfig is the hardcoded value above, and
		// Validate() cannot fail on it.
		return fmt.Errorf("failed to create watermill router: %w", err)
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

	p.Router = router
	return nil
}

// newNATS builds NATS-backed publisher and subscriber with queue-group
// semantics for competing consumers. Topics are mapped via a custom
// SubjectCalculator that derives queue groups from topic names:
//   - "certrequest.sign" and "certrequest.signed" use queue groups to ensure
//     only one instance processes each (competing consumer pattern)
//   - "certrequest.wait.*" topics use empty queue groups for fan-out to the
//     one instance holding that request's SSE connection
//
// JetStream is disabled (Disabled: true) — only NATS core is used with
// at-most-once delivery. A dropped job costs the client a full client_timeout
// wait before retrying, which is acceptable for the interactive approval
// flow. See docs/dev/multi-instance-safety-plan.md for durability reasoning.
func newNATS(cfg *config.PubSubConfig, wmLogger watermill.LoggerAdapter, sLogger *slog.Logger) (*PubSub, error) {
	// Create the publisher
	pub, err := natslib.NewPublisher(
		natslib.PublisherConfig{
			URL: cfg.NATS.URL,
			NatsOptions: []natsgo.Option{
				natsgo.ClientCert(cfg.NATS.CertFile, cfg.NATS.KeyFile),
				natsgo.RootCAs(cfg.NATS.CAFile),
			},
			Marshaler:         natslib.JSONMarshaler{},
			SubjectCalculator: subjectCalculator,
			JetStream: natslib.JetStreamConfig{
				Disabled: true,
			},
		},
		wmLogger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create NATS publisher: %w", err)
	}

	// The subscriber runs on a connection this package holds itself, so
	// Subscribe can be confirmed against the server before it returns (see
	// confirmingSubscriber). The publisher keeps its own connection:
	// Subscriber.Close drains whatever connection it was handed, which
	// would take the publisher down with it if the two shared one.
	subConn, err := natsgo.Connect(cfg.NATS.URL,
		natsgo.ClientCert(cfg.NATS.CertFile, cfg.NATS.KeyFile),
		natsgo.RootCAs(cfg.NATS.CAFile),
	)
	if err != nil {
		_ = pub.Close()
		return nil, fmt.Errorf("failed to connect to NATS for subscriptions: %w", err)
	}

	// URL and NatsOptions are deliberately absent: this config is consumed
	// through GetSubscriberSubscriptionConfig, which carries only the
	// per-subscribe settings, the connection having already been made.
	subCfg := natslib.SubscriberConfig{
		QueueGroupPrefix:  "ssoossh", // Prefix for deriving queue group names
		Unmarshaler:       natslib.JSONMarshaler{},
		SubjectCalculator: subjectCalculator,
		SubscribersCount:  1,
		CloseTimeout:      3 * time.Second,
		AckWaitTimeout:    30 * time.Second,
		SubscribeTimeout:  5 * time.Second,
		JetStream: natslib.JetStreamConfig{
			Disabled: true,
		},
	}
	sub, err := natslib.NewSubscriberWithNatsConn(subConn, subCfg.GetSubscriberSubscriptionConfig(), wmLogger)
	if err != nil {
		_ = pub.Close()
		subConn.Close()
		return nil, fmt.Errorf("failed to create NATS subscriber: %w", err)
	}

	sLogger.Info("NATS transport initialized", "url", cfg.NATS.URL)

	return &PubSub{
		Publisher:      pub,
		Subscriber:     &confirmingSubscriber{Subscriber: sub, conn: subConn},
		natsPublisher:  pub,
		natsSubscriber: sub,
	}, nil
}

// confirmingSubscriber makes Subscribe synchronous with respect to the NATS
// server.
//
// watermill issues the SUB and returns as soon as it is written to the
// client's outbound buffer, not when the server has processed it. Core NATS
// has no replay, so anything published in that gap is dropped outright --
// and for a certificate request a dropped wake is unrecoverable, not merely
// late: the certificate is deliberately never persisted (see
// docs/signing-pipeline.md), so the instance that missed it can read
// "approved" from the database and still have nothing to hand its client,
// which then blocks until the request expires.
//
// Measured at roughly 35ms on a loopback connection, which is wide enough
// for an approval to land inside it. The cost of closing it is one server
// round trip per Wait call.
type confirmingSubscriber struct {
	message.Subscriber
	conn *natsgo.Conn
}

// subscribeConfirmTimeout bounds the confirmation round trip, matching the
// SubscribeTimeout the subscriber is configured with.
const subscribeConfirmTimeout = 5 * time.Second

// Subscribe delegates, then flushes the connection so the subscription is
// registered server-side before the caller is told it is subscribed.
func (c *confirmingSubscriber) Subscribe(ctx context.Context, topic string) (<-chan *message.Message, error) {
	messages, err := c.Subscriber.Subscribe(ctx, topic)
	if err != nil {
		return nil, err
	}
	// FlushTimeout, not FlushWithContext: the subscribe contexts here are
	// lifetime contexts with no deadline (the router's, or an SSE client's
	// connection), and FlushWithContext rejects those outright.
	if err := c.conn.FlushTimeout(subscribeConfirmTimeout); err != nil {
		return nil, fmt.Errorf("failed to confirm subscription to %q: %w", topic, err)
	}
	return messages, nil
}

// subjectCalculator derives queue groups from topic names to implement
// competing-consumer semantics for sign and signed-reply topics, and
// fan-out semantics for per-request wake topics.
func subjectCalculator(queueGroupPrefix, topic string) *natslib.SubjectDetail {
	detail := &natslib.SubjectDetail{
		Primary: topic,
	}

	// Certrequest.sign and certrequest.signed use queue groups so only one
	// instance processes each job. Derive a stable queue group name from the
	// topic. Certrequest.wait.* topics get no queue group (empty string),
	// giving them ordinary fan-out semantics scoped to one instance.
	switch topic {
	case "certrequest.sign":
		detail.QueueGroup = "signer"
	case "certrequest.signed":
		detail.QueueGroup = "signed-listeners"
	}

	return detail
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
// docs/dev/signer-split-deferred.md) — a poison queue nobody consumes is
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
		//
		// not covered (the timeout arm only): forcing it deterministically
		// needs a pre-canceled context racing an unstarted Router, which
		// in practice leaves Router.Run blocked rather than honoring ctx.
		// That is a real hang risk in a test, not a reliable assertion.
		select {
		case <-p.Router.Running():
		case <-time.After(routerStartTimeout):
		}

		// Best-effort; Run's own return value is what servicerunner observes.
		_ = p.Router.Close()
	}()

	return p.Router.Run(ctx)
}

// Close shuts down the Router before the underlying transport (gochannel or
// NATS), so any in-flight handler gets a chance to finish before its
// transport goes away. Matches servicerunner.Service's signature, for use
// with bootstrap's shutdownManager.
func (p *PubSub) Close(context.Context) error {
	// Only close a Router that actually started. watermill's Router.Close on
	// a never-started Router does not return early - it waits out the full
	// CloseTimeout on handlers that never reported in (the same trap Run's
	// shutdown goroutine documents). In normal operation Run's goroutine has
	// already closed it by the time this runs, so IsRunning() is false and
	// this is a fast no-op rather than a redundant blocking double-close;
	// skipping it also keeps this safe if Close is ever reached without Run
	// having started the Router.
	if p.Router.IsRunning() {
		if err := p.Router.Close(); err != nil {
			// not covered: Router is a concrete watermill type, not an
			// interface, so a Close failure cannot be injected from
			// outside; a double Close was verified to return no error.
			return fmt.Errorf("failed to close watermill router: %w", err)
		}
	}

	if p.natsPublisher != nil {
		if err := p.natsPublisher.Close(); err != nil {
			return fmt.Errorf("failed to close NATS publisher: %w", err)
		}
	}

	if p.natsSubscriber != nil {
		if err := p.natsSubscriber.Close(); err != nil {
			return fmt.Errorf("failed to close NATS subscriber: %w", err)
		}
	}

	if p.channel != nil {
		if err := p.channel.Close(); err != nil {
			// not covered: channel is a concrete gochannel type, not an
			// interface, so a Close failure cannot be injected from
			// outside; a double Close was verified to return no error.
			return fmt.Errorf("failed to close gochannel pub/sub: %w", err)
		}
	}

	return nil
}
