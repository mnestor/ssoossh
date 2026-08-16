package bootstrap

import (
	"log/slog"

	"github.com/mnestor/ssoossh/server/pubsub"
)

// initPubSub builds the gochannel-backed message-broker primitives the
// signing pipeline runs on (see docs/signing-pipeline.md). initPipeline
// registers handlers on its Router, and services that publish take its
// Publisher/Subscriber by injection.
func (a *app) initPubSub() (*pubsub.PubSub, error) {
	return pubsub.New(slog.With("type", "queue"))
}
