package notify

import (
	"encoding/json"
	"fmt"
	"time"
)

// Topic is the queue topic notification events travel on, from the code
// that observed something worth reporting to the delivery handler that
// sends the mail.
//
// Routing this through the broker rather than calling a mailer inline is
// what keeps SMTP off the request path: an approval or a redemption
// publishes and returns, and a slow, unreachable, or greylisting mail
// server delays nothing a browser or an unattended job is waiting on. See
// server/service/notification.go.
const Topic = "notification.send"

// Event is one thing worth telling a user about. UserID names the
// recipient by users.id rather than carrying an address, so delivery reads
// the current address and the current preference at send time — an event
// sitting in a queue during a retry backoff must not deliver to an address
// the user has since changed.
type Event struct {
	Kind       Kind            `json:"kind"`
	UserID     string          `json:"user_id"`
	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
}

// NewEvent encodes payload into an Event for kind. It rejects unregistered
// kinds here, at the publishing call site, rather than letting the delivery
// handler discover the problem after the originating request is long gone.
func NewEvent(kind Kind, userID string, payload any) (Event, error) {
	if _, ok := Lookup(kind); !ok {
		return Event{}, fmt.Errorf("unregistered notification kind %q", kind)
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("failed to encode %s payload: %w", kind, err)
	}

	return Event{
		Kind:       kind,
		UserID:     userID,
		OccurredAt: time.Now(),
		Payload:    encoded,
	}, nil
}

// DecodePayload returns the typed payload for e.Kind, ready to hand to a
// template. The concrete type is whatever the kind's NewPayload builds, so
// a caller type-asserts against the struct in payloads.go.
func (e Event) DecodePayload() (any, error) {
	def, ok := Lookup(e.Kind)
	if !ok {
		return nil, fmt.Errorf("unregistered notification kind %q", e.Kind)
	}

	payload := def.NewPayload()
	if err := json.Unmarshal(e.Payload, payload); err != nil {
		return nil, fmt.Errorf("failed to decode %s payload: %w", e.Kind, err)
	}
	return payload, nil
}
