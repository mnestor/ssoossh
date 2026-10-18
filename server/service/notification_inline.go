package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"

	"github.com/mnestor/ssoossh/server/notify"
)

// InlinePublisher returns a publisher that delivers each notification
// through this handler on the publishing goroutine, retrying a failed
// delivery as the router's retry middleware retries a queued one (see
// pubsub.buildRouter).
//
// It is for a short-lived process with no router running: `ssoosshd ldap
// sync` can disable an admin, whose email is always sent, and the process
// must not exit before that mail is out. A gochannel broker of its own
// would deliver to nobody, and joining a NATS queue group would take
// notifications meant for the running servers. Publish therefore blocks on
// the relay, which a request path must never do, and is used nowhere else.
func (h *NotificationHandler) InlinePublisher() message.Publisher {
	retry := middleware.Retry{
		MaxRetries:      3,
		InitialInterval: 100 * time.Millisecond,
		Multiplier:      2,
	}
	return &inlinePublisher{deliver: retry.Middleware(func(msg *message.Message) ([]*message.Message, error) {
		return nil, h.handle(msg)
	})}
}

// inlinePublisher is InlinePublisher's message.Publisher.
type inlinePublisher struct {
	deliver message.HandlerFunc
}

// Publish delivers every message now, returning the deliveries that failed
// after their retries. Only the notification topic is accepted: nothing
// else has a handler in this process.
func (p *inlinePublisher) Publish(topic string, messages ...*message.Message) error {
	if topic != notify.Topic {
		return fmt.Errorf("the inline notification publisher only delivers %q, not %q", notify.Topic, topic)
	}
	var errs []error
	for _, msg := range messages {
		if _, err := p.deliver(msg); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Close implements message.Publisher. There is nothing to release.
func (p *inlinePublisher) Close() error { return nil }
