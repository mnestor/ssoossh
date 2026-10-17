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

// Event is one thing worth telling people about, addressed one of three
// ways.
//
// UserID names a single recipient by users.id. ServiceAccount names an
// audience instead — everyone holding that account — because a service
// enrollment has no single owning user to address (see
// https://mnestor.github.io/ssoossh/concepts/service-certificates/).
// NSSKeyID names a fleet key, whose notification address is the recipient.
// It is set alone: a key has no account to fan out over, and no user whose
// preference could gate it.
//
// Exactly one of the three forms is set: a user-scoped kind has no account to
// fan out over, an enrollment-scoped one has nobody in particular to name, and
// a fleet key event is about the key itself.
//
// Neither form carries an address. Delivery resolves the current
// recipients, their current addresses, and their current preferences at
// send time: an event sitting in a queue during a retry backoff must not
// deliver to an address a user has since changed, nor to a set of holders
// that has since changed.
type Event struct {
	Kind           Kind   `json:"kind"`
	UserID         string `json:"user_id,omitempty"`
	ServiceAccount string `json:"service_account,omitempty"`

	// EnrollmentID names the enrollment an account-addressed event is
	// about, so delivery can check that enrollment's own notification
	// address before falling back to fanning out over the account. Set only
	// alongside ServiceAccount; a user-addressed event has no enrollment.
	//
	// The ID rather than the address itself, for the same reason no other
	// address travels in an event: the row is read at delivery, so an event
	// that waited through a retry backoff honors the address the enrollment
	// has now rather than the one it had when the event was published.
	EnrollmentID string `json:"enrollment_id,omitempty"`

	// NSSKeyID names the NSS fleet key an event is about. Delivery reads the
	// key's notification_email when it sends, the same way it reads an
	// enrollment's address. It is an id, never an address, so an event that
	// waited through a retry backoff honors the address the key has now.
	NSSKeyID string `json:"nss_key_id,omitempty"`

	OccurredAt time.Time       `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
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

// NewEnrollmentEvent encodes payload into an Event about one enrollment:
// delivered to that enrollment's notification address if it has one, and
// otherwise fanned out over serviceAccount exactly like
// NewServiceAccountEvent.
func NewEnrollmentEvent(kind Kind, enrollmentID, serviceAccount string, payload any) (Event, error) {
	return newEvent(kind, Event{ServiceAccount: serviceAccount, EnrollmentID: enrollmentID}, payload)
}

// NewNSSKeyEvent encodes payload into an Event about one NSS fleet key:
// delivered to the key's notification address, read at delivery time.
func NewNSSKeyEvent(kind Kind, keyID string, payload any) (Event, error) {
	return newEvent(kind, Event{NSSKeyID: keyID}, payload)
}

// newEvent fills in the parts every addressing form shares. It rejects
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
