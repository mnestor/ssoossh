package notify

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// Every registered kind has to be complete enough to drive the three things
// the registry exists to drive: the preferences UI, the generated docs, and
// template rendering. A half-filled entry compiles fine and then produces a
// blank checkbox or an undocumented template, so assert the whole shape.
func TestDefinitions_shouldBeCompleteForEveryKind(t *testing.T) {
	for _, def := range Definitions() {
		t.Run(string(def.Kind), func(t *testing.T) {
			if def.Kind == "" {
				t.Error("kind is empty")
			}
			if def.Title == "" {
				t.Error("title is empty")
			}
			if def.Description == "" {
				t.Error("description is empty")
			}
			if len(def.Fields) == 0 {
				t.Error("no documented fields")
			}
			if def.NewPayload == nil {
				t.Fatal("NewPayload is nil")
			}
			if def.NewPayload() == nil {
				t.Error("NewPayload returned nil")
			}
		})
	}
}

// The docs table and the preferences UI both list fields; a duplicate name
// means one of the two rows is a lie about a template variable.
func TestDefinitions_shouldNotRepeatAFieldName(t *testing.T) {
	for _, def := range Definitions() {
		seen := map[string]bool{}
		for _, f := range def.Fields {
			if f.Name == "" || f.Type == "" || f.Description == "" {
				t.Errorf("%s: incomplete field %+v", def.Kind, f)
			}
			if seen[f.Name] {
				t.Errorf("%s: duplicate field %q", def.Kind, f.Name)
			}
			seen[f.Name] = true
		}
	}
}

// Two entries sharing a kind would make Lookup's answer depend on
// registration order, and would collide on the preferences table's unique
// (user_id, kind).
func TestDefinitions_shouldNotRepeatAKind(t *testing.T) {
	seen := map[Kind]bool{}
	for _, def := range Definitions() {
		if seen[def.Kind] {
			t.Errorf("duplicate kind %q", def.Kind)
		}
		seen[def.Kind] = true
	}
}

// Documented fields must actually exist on the payload struct, or the docs
// promise a template variable that renders empty.
func TestDefinitions_shouldDocumentOnlyRealPayloadFields(t *testing.T) {
	for _, def := range Definitions() {
		t.Run(string(def.Kind), func(t *testing.T) {
			present := payloadFieldNames(t, def.NewPayload())
			for _, f := range def.Fields {
				if !present[f.Name] {
					t.Errorf("documented field %q is not on the payload struct", f.Name)
				}
			}
			for name := range present {
				if !hasField(def.Fields, name) {
					t.Errorf("payload field %q is undocumented", name)
				}
			}
		})
	}
}

func TestLookup_shouldReportUnknownKinds(t *testing.T) {
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup reported an unregistered kind as known")
	}
	if _, ok := Lookup(KindServiceEnrollmentCreated); !ok {
		t.Error("Lookup did not find a registered kind")
	}
}

func TestNewEvent_shouldRoundTripThePayload(t *testing.T) {
	created := &ServiceEnrollmentCreated{
		ServiceAccount: "deploy-bot",
		CodeExpiresAt:  time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
	}

	event, err := NewEvent(KindServiceEnrollmentCreated, "user-1", created)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Event
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	payload, err := decoded.DecodePayload()
	if err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	got, ok := payload.(*ServiceEnrollmentCreated)
	if !ok {
		t.Fatalf("DecodePayload returned %T, want *ServiceEnrollmentCreated", payload)
	}
	if got.ServiceAccount != created.ServiceAccount {
		t.Errorf("ServiceAccount = %q, want %q", got.ServiceAccount, created.ServiceAccount)
	}
	if !got.CodeExpiresAt.Equal(created.CodeExpiresAt) {
		t.Errorf("CodeExpiresAt = %v, want %v", got.CodeExpiresAt, created.CodeExpiresAt)
	}
}

func TestNewEvent_shouldRejectAnUnregisteredKind(t *testing.T) {
	if _, err := NewEvent("nope", "user-1", struct{}{}); err == nil {
		t.Error("NewEvent accepted an unregistered kind")
	}
}

// The two constructors differ only in addressing, and delivery fans out on
// exactly that: a ServiceAccount event goes to every current holder, a
// UserID event to one person. An event carrying both would double-send.
func TestNewServiceAccountEvent_shouldAddressTheAccountNotAUser(t *testing.T) {
	event, err := NewServiceAccountEvent(KindServiceEnrollmentRedeemed, "deploy-bot", &ServiceEnrollmentRedeemed{ServiceAccount: "deploy-bot"})
	if err != nil {
		t.Fatalf("NewServiceAccountEvent: %v", err)
	}
	if event.ServiceAccount != "deploy-bot" {
		t.Errorf("ServiceAccount = %q, want %q", event.ServiceAccount, "deploy-bot")
	}
	if event.UserID != "" {
		t.Errorf("UserID = %q, want it empty on an account-addressed event", event.UserID)
	}
	if event.Kind != KindServiceEnrollmentRedeemed {
		t.Errorf("Kind = %q, want %q", event.Kind, KindServiceEnrollmentRedeemed)
	}
	if event.OccurredAt.IsZero() {
		t.Error("OccurredAt was not stamped")
	}
}

func TestNewServiceAccountEvent_shouldRejectAnUnregisteredKind(t *testing.T) {
	if _, err := NewServiceAccountEvent("nope", "deploy-bot", struct{}{}); err == nil {
		t.Error("NewServiceAccountEvent accepted an unregistered kind")
	}
}

// An enrollment-addressed event carries both: the enrollment so delivery can
// read its notification address, and the account to fan out over when it has
// none. Carrying only one would make the fallback impossible.
func TestNewEnrollmentEvent_shouldCarryBothTheEnrollmentAndTheAccount(t *testing.T) {
	event, err := NewEnrollmentEvent(KindServiceEnrollmentExpiring, "enr-1", "deploy-bot",
		&ServiceEnrollmentExpiring{ServiceAccount: "deploy-bot"})
	if err != nil {
		t.Fatalf("NewEnrollmentEvent: %v", err)
	}
	if event.EnrollmentID != "enr-1" {
		t.Errorf("EnrollmentID = %q, want %q", event.EnrollmentID, "enr-1")
	}
	if event.ServiceAccount != "deploy-bot" {
		t.Errorf("ServiceAccount = %q, want %q", event.ServiceAccount, "deploy-bot")
	}
	if event.UserID != "" {
		t.Errorf("UserID = %q, want it empty on an enrollment-addressed event", event.UserID)
	}
}

func TestNewEnrollmentEvent_shouldRejectAnUnregisteredKind(t *testing.T) {
	if _, err := NewEnrollmentEvent("nope", "enr-1", "deploy-bot", struct{}{}); err == nil {
		t.Error("NewEnrollmentEvent accepted an unregistered kind")
	}
}

// A payload that cannot encode must fail at the publishing call site, where
// the originating request can still see the error, not at delivery.
func TestNewEvent_shouldRejectAnUnencodablePayload(t *testing.T) {
	if _, err := NewEvent(KindServiceEnrollmentCreated, "user-1", func() {}); err == nil {
		t.Error("NewEvent accepted a payload json cannot encode")
	}
}

func TestDecodePayload_shouldRejectAnUnregisteredKind(t *testing.T) {
	if _, err := (Event{Kind: "nope", Payload: []byte("{}")}).DecodePayload(); err == nil {
		t.Error("DecodePayload accepted an unregistered kind")
	}
}

func TestDecodePayload_shouldRejectMalformedJSON(t *testing.T) {
	e := Event{Kind: KindServiceEnrollmentCreated, Payload: []byte("{")}
	if _, err := e.DecodePayload(); err == nil {
		t.Error("DecodePayload accepted malformed JSON")
	}
}

// payloadFieldNames returns the exported field names of the struct behind
// p, which are exactly the names a template can reference.
func payloadFieldNames(t *testing.T, p any) map[string]bool {
	t.Helper()

	v := reflect.ValueOf(p)
	for v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		t.Fatalf("payload is %s, want a struct", v.Kind())
	}

	names := map[string]bool{}
	typ := v.Type()
	for i := range typ.NumField() {
		if f := typ.Field(i); f.IsExported() {
			names[f.Name] = true
		}
	}
	return names
}

// hasField reports whether fields documents name.
func hasField(fields []Field, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

// DefaultEnabled is what a user who has never answered gets, so it decides
// whether an existing deployment starts sending something nobody asked
// for. An unregistered kind must never be sent.
func TestDefaultEnabled_shouldAnswerFromTheRegistry(t *testing.T) {
	for _, def := range Definitions() {
		if got := DefaultEnabled(def.Kind); got != def.DefaultEnabled {
			t.Errorf("DefaultEnabled(%s) = %v, want %v", def.Kind, got, def.DefaultEnabled)
		}
	}

	if DefaultEnabled("nope") {
		t.Error("DefaultEnabled reported an unregistered kind as enabled")
	}
}
