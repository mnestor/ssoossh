package apitypes_test

// Test methodology: marshal a fully-populated instance of every wire type in
// this package and diff it against a checked-in golden file.
//
// server/webtypes has the same test for the browser-facing response shapes.
// This one covers the other half of the API: the request and response bodies
// the non-browser clients speak. Those have a consumer Go cannot check for us
// at all — pam_ssoossh (github.com/mnestor/ssoossh-pam) is a separate project
// written in C, so a renamed json tag here compiles fine, regenerates
// docs/openapi.yaml without complaint, passes every gate, and breaks that
// module in the field.
//
// The goldens are the artifact that closes that hole. They are plain JSON, so
// the C side reads them with no Go toolchain: it parses each file and asserts
// its own decoder produces the values below. docs/wire-contract.json hashes
// them, which is what makes a change to one show up in review as a version
// bump rather than as a diff nobody connected to the other repository.
//
// Run `go test ./internal/apitypes/ -update` to accept an intended change,
// then `make wire-contract` and `make openapi` — which is what the failure
// message tells you to do.

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
)

var update = flag.Bool("update", false, "rewrite the golden files instead of comparing against them")

// clientTime is a fixed timestamp with a non-UTC offset, so the goldens pin
// the serialization format (RFC 3339, offset preserved) and not just the
// field names. A C client parsing these has to accept an offset that is not
// "Z", which is the mistake this value exists to catch.
var clientTime = time.Date(2026, 9, 5, 13, 4, 5, 0, time.FixedZone("CEST", 2*60*60))

// expiresAt is deliberately UTC, so the pair of them proves both spellings
// appear on the wire and neither side may assume one.
var expiresAt = time.Date(2026, 9, 5, 11, 34, 5, 0, time.UTC)

// hostContext is the self-reported block PAMRequestBody and ConsoleRequestBody
// both carry. Declared once because the two types are identical in shape by
// design (see ConsoleRequestBody's doc comment) and a fixture that drifted
// between them would hide exactly the divergence that matters.
var (
	callerUID  = int64(1000)
	callerPID  = int64(4242)
	callerPPID = int64(4200)
)

// requestedOptions is populated for every request body that carries one, so
// the nested shape is pinned inside each parent rather than only standalone.
var requestedOptions = apitypes.RequestedOptions{
	Extensions:      []string{"permit-pty", "permit-agent-forwarding"},
	ForceCommand:    "/usr/local/bin/audit-shell",
	SourceAddresses: []string{"198.51.100.0/24", "203.0.113.7/32"},
	NoTouchRequired: true,
}

// fullFixtures are instances with every field set to a distinguishable
// non-zero value. Non-zero matters: a field with an omitempty tag vanishes
// from the JSON when it is zero, so a fixture built from a zero value would
// let a new optional field slip in without appearing in any golden.
// assertAllFieldsSet enforces it.
func fullFixtures() map[string]any {
	return map[string]any{
		"requested_options": requestedOptions,

		"user_request": apitypes.UserRequestBody{
			PublicKey:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleUserKey alice@workstation",
			LocalUsername:    "alice",
			LocalHostname:    "workstation.example.org",
			RequestedOptions: requestedOptions,
		},

		"service_enroll_request": apitypes.ServiceEnrollRequestBody{
			PublicKey:        "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleServiceKey deploy@ci",
			RequestedOptions: requestedOptions,
		},

		"pam_request": apitypes.PAMRequestBody{
			PublicKey:             "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExamplePAMKey pam@host",
			Username:              "alice",
			Hostname:              "db-01.example.org",
			PAMService:            "sudo",
			TTY:                   "/dev/pts/3",
			RemoteHost:            "198.51.100.7",
			RequestingUser:        "alice",
			Process:               "sudo systemctl restart nginx",
			CallerUID:             &callerUID,
			CallerPID:             &callerPID,
			CallerPPID:            &callerPPID,
			MachineID:             "6b3a1f9c8d2e4a5b9c0d1e2f3a4b5c6d",
			OS:                    "Debian GNU/Linux 13 (trixie) Linux 6.12.0",
			Client:                "pam_ssoossh-c/0.3.0",
			Mode:                  "auto",
			ClientTime:            &clientTime,
			TrustedCAFingerprints: []string{"SHA256:1yQ0mE2xExampleFingerprintOne", "SHA256:9pR4nT7zExampleFingerprintTwo"},
			RequestedOptions:      requestedOptions,
		},

		"console_request": apitypes.ConsoleRequestBody{
			PublicKey:             "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleConsoleKey pam@host",
			Username:              "alice",
			Hostname:              "db-01.example.org",
			PAMService:            "login",
			TTY:                   "/dev/tty1",
			RemoteHost:            "198.51.100.7",
			RequestingUser:        "alice",
			Process:               "login",
			CallerUID:             &callerUID,
			CallerPID:             &callerPID,
			CallerPPID:            &callerPPID,
			MachineID:             "6b3a1f9c8d2e4a5b9c0d1e2f3a4b5c6d",
			OS:                    "Debian GNU/Linux 13 (trixie) Linux 6.12.0",
			Client:                "pam_ssoossh-c/0.3.0",
			Mode:                  "console",
			ClientTime:            &clientTime,
			TrustedCAFingerprints: []string{"SHA256:1yQ0mE2xExampleFingerprintOne", "SHA256:9pR4nT7zExampleFingerprintTwo"},
			RequestedOptions:      requestedOptions,
		},

		"create_request_response": apitypes.CreateRequestResponse{
			RequestID:               "9f1c0b2a-3d4e-5f60-8a91-b2c3d4e5f607",
			EventsURL:               "/api/certs/requests/9f1c0b2a-3d4e-5f60-8a91-b2c3d4e5f607/events",
			ApprovalURL:             "/approve/9f1c0b2a-3d4e-5f60-8a91-b2c3d4e5f607",
			UserCode:                "K7M4-QP2X",
			VerificationURL:         "/console",
			VerificationURLComplete: "/c/K7M4QP2X",
			ExpiresAt:               expiresAt,
		},

		"approve_response": apitypes.ApproveResponse{Status: "signing"},
		"deny_response":    apitypes.DenyResponse{Status: "denied"},

		"retrieve_request":  apitypes.RetrieveRequestBody{Code: "K7M4-QP2X"},
		"retrieve_response": apitypes.RetrieveResponse{Certificate: "ssh-ed25519-cert-v01@openssh.com AAAAIExampleServiceCert"},

		"ca_response": apitypes.CAResponse{CA: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleCAKey ssoossh-ca"},
	}
}

// zeroFixtures are the same types with nothing set. They document which
// fields disappear under omitempty, which is the set a decoder on the other
// side has to treat as absent rather than as empty-string. A field that
// becomes optional without the C module learning about it is how a
// null-dereference reaches a production sudo.
func zeroFixtures() map[string]any {
	return map[string]any{
		"requested_options":       apitypes.RequestedOptions{},
		"user_request":            apitypes.UserRequestBody{},
		"service_enroll_request":  apitypes.ServiceEnrollRequestBody{},
		"pam_request":             apitypes.PAMRequestBody{},
		"console_request":         apitypes.ConsoleRequestBody{},
		"create_request_response": apitypes.CreateRequestResponse{},
		"ca_response":             apitypes.CAResponse{},
	}
}

// ssePayloadFixtures are the envelopes the events endpoint sends, one per
// terminal status. This is the shape docs/openapi.yaml cannot describe: the
// response is a stream, so the spec declares the endpoint but says nothing
// about what an individual event carries. These files are the only
// machine-readable statement of it.
//
// Two conventions they exist to pin, both of which a second implementation
// gets wrong by guessing:
//
//   - The status is NOT in the payload. CertificateResult.Status is
//     `json:"-"`; the SSE event *name* carries it. A decoder looking for a
//     "status" field finds nothing and must read the event name instead.
//   - Which fields are present depends on the status. Only "approved" carries
//     a certificate; only "enrolled" carries a code, service_account and
//     expires_at; "denied", "expired" and "failed" carry an empty data object.
func ssePayloadFixtures() map[string]apitypes.Envelope[apitypes.CertificateResult] {
	return map[string]apitypes.Envelope[apitypes.CertificateResult]{
		apitypes.StatusApproved: {Data: apitypes.CertificateResult{
			Status:      apitypes.StatusApproved,
			Certificate: "ssh-ed25519-cert-v01@openssh.com AAAAIExampleUserCert",
		}},
		apitypes.StatusDenied:  {Data: apitypes.CertificateResult{Status: apitypes.StatusDenied}},
		apitypes.StatusExpired: {Data: apitypes.CertificateResult{Status: apitypes.StatusExpired}},
		apitypes.StatusEnrolled: {Data: apitypes.CertificateResult{
			Status:         apitypes.StatusEnrolled,
			Code:           "K7M4-QP2X",
			ServiceAccount: "deploy",
			ExpiresAt:      &expiresAt,
		}},
		apitypes.StatusFailed: {Data: apitypes.CertificateResult{Status: apitypes.StatusFailed}},
	}
}

func TestFullFixturesShouldMatchTheirGoldenJSON(t *testing.T) {
	for name, fixture := range fullFixtures() {
		t.Run(name, func(t *testing.T) {
			assertAllFieldsSet(t, reflect.ValueOf(fixture), name)
			assertGolden(t, filepath.Join("testdata", name+".full.json"), fixture)
		})
	}
}

func TestZeroFixturesShouldMatchTheirGoldenJSON(t *testing.T) {
	for name, fixture := range zeroFixtures() {
		t.Run(name, func(t *testing.T) {
			assertGolden(t, filepath.Join("testdata", name+".zero.json"), fixture)
		})
	}
}

func TestSSEPayloadFixturesShouldMatchTheirGoldenJSON(t *testing.T) {
	for status, fixture := range ssePayloadFixtures() {
		t.Run(status, func(t *testing.T) {
			assertGolden(t, filepath.Join("testdata", "sse_payload_"+status+".json"), fixture)
		})
	}
}

// should cover every terminal status with a payload fixture, so a status
// added to TerminalStatuses without a fixture fails here rather than
// reaching a client that has never seen it. A client must register for all
// of them or it blocks forever waiting for an event that never comes, which
// makes an unfixtured status a hang rather than an error.
func TestSSEPayloadFixturesShouldCoverEveryTerminalStatus(t *testing.T) {
	fixtures := ssePayloadFixtures()

	for _, status := range apitypes.TerminalStatuses() {
		if _, ok := fixtures[status]; !ok {
			t.Errorf("terminal status %q has no SSE payload fixture; add one so ssoossh-pam has a statement of what that event carries", status)
		}
	}

	if len(fixtures) != len(apitypes.TerminalStatuses()) {
		t.Errorf("got %d SSE payload fixtures for %d terminal statuses; a fixture for a status that no longer exists is worse than none",
			len(fixtures), len(apitypes.TerminalStatuses()))
	}
}

// should keep the status out of the encoded payload. CertificateResult.Status
// is `json:"-"` on purpose — the SSE event name carries it — and a stray
// "status" key appearing here would give a decoder two disagreeing sources
// for the same fact.
func TestSSEPayloadShouldNotEncodeTheStatusField(t *testing.T) {
	for status, fixture := range ssePayloadFixtures() {
		t.Run(status, func(t *testing.T) {
			encoded, err := json.Marshal(fixture)
			if err != nil {
				t.Fatalf("failed to marshal fixture: %v", err)
			}

			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("failed to decode fixture: %v", err)
			}

			data, ok := decoded["data"].(map[string]any)
			if !ok {
				t.Fatalf("envelope data is not an object: %s", encoded)
			}
			if _, present := data["status"]; present {
				t.Errorf("the payload carries a %q key; the SSE event name is the only place the status belongs: %s", "status", encoded)
			}
		})
	}
}

// assertGolden compares fixture's JSON against the file at path, or rewrites
// the file when -update is set.
func assertGolden(t *testing.T, path string, fixture any) {
	t.Helper()

	encoded, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal fixture: %v", err)
	}
	encoded = append(encoded, '\n')

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("failed to create testdata directory: %v", err)
		}
		//nolint:gosec // golden files are committed artifacts meant to be world-readable
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("failed to write golden %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read golden %s: %v\nrun: go test ./internal/apitypes/ -update", path, err)
	}

	if string(encoded) != string(want) {
		t.Errorf(`the wire contract changed.

golden %s
want:
%s
got:
%s
This shape is spoken by a consumer in another repository. If the change is
intended, then in the same commit:
  1. go test ./internal/apitypes/ -update
  2. make openapi          (regenerates docs/openapi.yaml)
  3. make wire-contract    (bumps docs/wire-contract.json's version)
and open the matching change in github.com/mnestor/ssoossh-pam before
releasing either side.`, path, want, encoded)
	}
}

// assertAllFieldsSet fails when any field reachable from v is still at its
// zero value, naming the path to it. Recurses into nested structs, and
// through pointers, so a field added to RequestedOptions is caught inside
// PAMRequestBody too.
//
// This is the half that catches additions. The golden diff catches renames
// and retypes on its own, but a new field with a zero value and an omitempty
// tag would produce an identical golden and slip through.
//
// Deliberately a copy of server/webtypes's helper rather than a shared one:
// the two live in different packages, and a test-only helper package to hold
// thirty lines would be a worse trade than the duplication.
func assertAllFieldsSet(t *testing.T, v reflect.Value, path string) {
	t.Helper()

	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return
	}

	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		if !field.IsExported() {
			continue
		}

		value := v.Field(i)
		fieldPath := path + "." + field.Name

		if value.IsZero() {
			t.Errorf("%s is unset in the full fixture; every field has to carry a non-zero value or omitempty could hide it from the golden", fieldPath)
			continue
		}

		assertAllFieldsSet(t, value, fieldPath)
	}
}
