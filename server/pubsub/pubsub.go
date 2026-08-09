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

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
)

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
		// Persistent so a subscriber that attaches slightly after a fast
		// publish still sees the message — matters once real topics exist
		// (Phase 2 onward); a no-op while nothing publishes yet.
		Persistent: true,
	}, wmLogger)

	router, err := message.NewRouter(message.RouterConfig{}, wmLogger)
	if err != nil {
		return nil, fmt.Errorf("failed to create watermill router: %w", err)
	}

	return &PubSub{
		Publisher:  channel,
		Subscriber: channel,
		Router:     router,
		channel:    channel,
	}, nil
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
