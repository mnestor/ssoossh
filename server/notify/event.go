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

// Event is one thing worth telling people about, addressed one of two
// ways.
//
// UserID names a single recipient by users.id. ServiceAccount names an
// audience instead — everyone holding that account — because a service
// enrollment has no single owning user to address (see
// docs/proposals/enrollment-group-ownership.md). Exactly one of the two is
// set: a user-scoped kind has no account to fan out over, and an
// enrollment-scoped one has nobody in particular to name.
//
// Neither form carries an address. Delivery resolves the current
// recipients, their current addresses, and their current preferences at
// send time: an event sitting in a queue during a retry backoff must not
// deliver to an address a user has since changed, nor to a set of holders
// that has since changed.
type Event struct {
	Kind           Kind            `json:"kind"`
	UserID         string          `json:"user_id,omitempty"`
	ServiceAccount string          `json:"service_account,omitempty"`
	OccurredAt     time.Time       `json:"occurred_at"`
	Payload        json.RawMessage `json:"payload"`
}

// NewEvent encodes payload into a user-addressed Event for kind.
func NewEvent(kind Kind, userID string, payload any) (Event, error) {
	return newEvent(kind, Event{UserID: userID}, payload)
}

// NewServiceAccountEvent encodes payload into an Event addressed to every
// holder of serviceAccount, resolved at delivery.
func NewServiceAccountEvent(kind Kind, serviceAccount string, payload any) (Event, error) {
	return newEvent(kind, Event{ServiceAccount: serviceAccount}, payload)
}

// newEvent fills in the parts both addressing forms share. It rejects
// unregistered kinds here, at the publishing call site, rather than letting
// the delivery handler discover the problem after the originating request
// is long gone.
func newEvent(kind Kind, addressed Event, payload any) (Event, error) {
	if _, ok := Lookup(kind); !ok {
		return Event{}, fmt.Errorf("unregistered notification kind %q", kind)
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return Event{}, fmt.Errorf("failed to encode %s payload: %w", kind, err)
	}

	addressed.Kind = kind
	addressed.OccurredAt = time.Now()
	addressed.Payload = encoded
	return addressed, nil
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
