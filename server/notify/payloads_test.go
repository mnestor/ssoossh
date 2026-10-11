package notify

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// advancedSetter mirrors what delivery (server/mail) asserts a payload to:
// the method promoted from the embedded Reader.
type advancedSetter interface {
	SetAdvanced(advanced bool)
}

// Delivery sets the recipient's level through the promoted method without
// knowing the payload's type, so every registered payload has to carry it.
func TestReader_shouldBePromotedToEveryPayload(t *testing.T) {
	for _, def := range Definitions() {
		t.Run(string(def.Kind), func(t *testing.T) {
			if _, ok := def.NewPayload().(advancedSetter); !ok {
				t.Errorf("payload %T does not embed Reader", def.NewPayload())
			}
		})
	}
}

// SetAdvanced is the only way a template's {{ .Advanced }} changes, in
// either direction.
func TestReader_shouldRecordTheLevelWhenSetAdvancedIsCalled(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start bool
		set   bool
	}{
		{name: "should turn advanced on", start: false, set: true},
		{name: "should turn advanced off", start: true, set: false},
		{name: "should keep simplified", start: false, set: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Reader{Advanced: tc.start}
			r.SetAdvanced(tc.set)
			if r.Advanced != tc.set {
				t.Errorf("Advanced = %v, want %v", r.Advanced, tc.set)
			}
		})
	}
}

// The level describes one recipient and is set at delivery, so it must
// never be encoded onto the queue with the event.
func TestReader_shouldNotTravelOnTheQueue(t *testing.T) {
	for _, def := range Definitions() {
		t.Run(string(def.Kind), func(t *testing.T) {
			p := def.NewPayload()
			p.(advancedSetter).SetAdvanced(true)

			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatalf("marshal %T: %v", p, err)
			}
			if strings.Contains(strings.ToLower(string(raw)), "advanced") {
				t.Errorf("encoded payload carries the detail level: %s", raw)
			}
		})
	}
}

// A payload decoded off the queue starts simplified, whatever the sender
// had set, until delivery sets it for the recipient.
func TestReader_shouldDecodeAsSimplified(t *testing.T) {
	p := ServiceEnrollmentCreated{ServiceAccount: "bot"}
	p.SetAdvanced(true)

	ev, err := NewEvent(KindServiceEnrollmentCreated, "user-1", p)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	decoded, err := ev.DecodePayload()
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if reflect.ValueOf(decoded).Elem().FieldByName("Advanced").Bool() {
		t.Error("decoded payload is advanced, want simplified")
	}
}
