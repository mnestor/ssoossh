package bootstrap

import (
	"log/slog"

	"github.com/mnestor/ssoossh/server/pubsub"
)

// initPubSub builds the message-broker primitives the signing pipeline runs
// on (see docs/signing-pipeline.md). Branches on configured backend:
// gochannel (in-process, default) or NATS (multi-instance). initPipeline
// registers handlers on its Router, and services that publish take its
// Publisher/Subscriber by injection.
func (a *app) initPubSub() (*pubsub.PubSub, error) {
	return pubsub.New(&a.config.Signer.PubSub, slog.With("type", "queue"))
}
