package bootstrap

import (
	"log/slog"

	"github.com/mnestor/ssoossh/server/pubsub"
)

// initPubSub builds the gochannel-backed message-broker primitives (Phase
// 1 of docs/watermill-signer-plan.md — see docs/watermill-phase1-pubsub.md
// for scope). Nothing consumes it yet; later phases register handlers on
// its Router and inject its Publisher/Subscriber into services that need
// them.
func (a *app) initPubSub() (*pubsub.PubSub, error) {
	return pubsub.New(slog.With("type", "queue"))
}
