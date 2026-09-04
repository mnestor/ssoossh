package webtypes_test

// Test methodology: marshal a fully-populated instance of every response
// type and diff it against a checked-in golden file.
//
// These types have three consumers that Go cannot check for us — the
// generated TypeScript in frontend/src/lib/api/generated/, the spec in
// docs/openapi.yaml, and the frontend code reading the fields — so a renamed
// json tag or a changed type compiles fine and breaks all three silently.
// The golden turns that into a failing test with a readable diff.
//
// Run `go test ./server/webtypes/ -update` to accept an intended change,
// then `make types` and `make openapi` — which is what the failure message
// tells you to do.

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/openapidoc"
	"github.com/mnestor/ssoossh/server/webtypes"
)

var update = flag.Bool("update", false, "rewrite the golden files instead of comparing against them")

// issuedAt is a fixed timestamp with a non-UTC offset, so the goldens pin
// the serialization format (RFC 3339, offset preserved) and not just the
// field names. tygo maps time.Time to string on the strength of this.
var issuedAt = time.Date(2026, 8, 14, 9, 30, 0, 0, time.FixedZone("CEST", 2*60*60))

// fullFixtures are instances with every field set to a distinguishable
// non-zero value. Non-zero matters: a field with an omitempty tag vanishes
// from the JSON when it is zero, so a fixture built from a zero value would
// let a new optional field slip in without appearing in any golden.
// assertAllFieldsSet enforces it.
func fullFixtures() map[string]any {
	options := webtypes.CertificateOptionsResponse{
		Extensions:      []string{"permit-pty", "permit-agent-forwarding"},
		ForceCommand:    "/usr/local/bin/audit-shell",
		SourceAddresses: []string{"198.51.100.0/24", "203.0.113.7/32"},
		NoTouchRequired: true,
	}

	certificateValidSeconds := 28800

	return map[string]any{
		"current_user": webtypes.CurrentUserResponse{
			Subject:         "9c1f0f8e-1d0a-4a37-9d1e-2f6a1b4c5d6e",
			Username:        "alice",
			Email:           "alice@example.org",
			Groups:          []string{"engineering", "sre"},
			OtherAccounts:   []string{"alice.adm"},
			ServiceAccounts: []string{"svc-deploy"},
			Extra: map[string]any{
				"employee_id": "E-12345",
				"department":  "Engineering",
			},
			IsAuditor: true,
		},
		"certificate_options": options,
		"request_detail": webtypes.RequestDetailResponse{
			ID:            "0b4f2b1a-7c3d-4e5f-8a9b-0c1d2e3f4a5b",
			Type:          model.CertificateTypeUser,
			Status:        model.CertificateRequestStatusPending,
			SourceIP:      "198.51.100.7",
			LocalUsername: "alice",
			LocalHostname: "alice-laptop",
			TargetAccount: "root",
			Hostname:      "web01",
			PAMService:    "login",
			TTY:           "tty1",
			RemoteHost:    "198.51.100.7",
			ExpiresAt:     issuedAt,
			PublicKey:     "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample alice@laptop",
			Principals:    []string{"alice", "alice@example.org"},
			ValidSeconds:  28800,
			Requested:     options,
			Granted:       options,
			CreatedAt:     issuedAt,
			ApprovalURL:   "/approve/0b4f2b1a-7c3d-4e5f-8a9b-0c1d2e3f4a5b",
			IsOwnedByYou:  true,
			AlreadyClosed: true,

			DecidedByOutcome:         "approved",
			DecidedBySubject:         "9c1f0f8e-1d0a-4a37-9d1e-2f6a1b4c5d6e",
			DecidedByUsername:        "alice",
			DecidedByEmail:           "alice@example.org",
			DecidedByGroups:          []string{"engineering", "sre"},
			DecidedByOtherAccounts:   []string{"alice.other"},
			DecidedByServiceAccounts: []string{"svc-backup"},
			DecidedSourceIP:          "198.51.100.7",
			DecidedUserAgent:         "curl/8.0.0",
			DecidedAcceptLanguage:    "en-US",
			DecidedForwardedFor:      "198.51.100.7, 10.0.0.1",
			DecidedAt:                &issuedAt,
		},
		"notification_preferences": webtypes.NotificationPreferencesResponse{
			MailEnabled: true,
			Address:     "alice@example.org",
			Kinds: []webtypes.NotificationKindResponse{
				{
					Kind:        "service_enrollment_created",
					Title:       "Service enrollment created",
					Description: "Sent when you approve a service certificate request.",
					Enabled:     true,
				},
			},
		},
		"enrollment_retrievals": webtypes.EnrollmentRetrievalsResponse{
			Retrievals: []webtypes.EnrollmentRetrievalResponse{{
				RetrievedAt:       issuedAt,
				SourceIP:          "198.51.100.44",
				CertificateSerial: 7346115228134082560,
				Succeeded:         true,
			}},
			// Deliberately larger than the page: the truncated case is the
			// one the frontend has to render differently.
			Total: 8760,
		},
		"service_enrollment": webtypes.ServiceEnrollmentResponse{
			ID:                   "3a2b1c0d-9e8f-4a7b-8c6d-5e4f3a2b1c0d",
			ServiceAccount:       "svc-backup",
			ApprovedByUsername:   "alice",
			CertificateRequestID: "0b4f2b1a-7c3d-4e5f-8a9b-0c1d2e3f4a5b",
			Principals:           []string{"svc-backup"},
			KeyID:                "svc-backup/0b4f2b1a",
			PublicKeyFingerprint: "SHA256:2Fd4rIWZ8kQnGx0mJvKp1YhLcTzXbA3sNeR5uW7oPqM",
			Options:              options,
			// A pointer so the zero fixture can show the field vanishing:
			// an enrollment predating the lifetime split reports no
			// certificate duration at all.
			CertificateValidSeconds: &certificateValidSeconds,
			CreatedAt:               issuedAt,
			ExpiresAt:               issuedAt.Add(90 * 24 * time.Hour),
			FirstRedeemedAt:         &issuedAt,
			LastRetrievedAt:         &issuedAt,
			RetrievalCount:          17,
			NotificationEmail:       "backups@example.org",
		},
		"certificate": webtypes.CertificateResponse{
			ID:           "6d5c4b3a-2f1e-4d0c-9b8a-7f6e5d4c3b2a",
			Type:         model.CertificateTypeService,
			SerialNumber: 7346115228134082560,
			KeyID:        "alice@example.org/0b4f2b1a",
			Principals:   "alice,alice@example.org",
			Fingerprint:  "SHA256:2Fd4rIWZ8kQnGx0mJvKp1YhLcTzXbA3sNeR5uW7oPqM",
			IssuedAt:     issuedAt,
			ExpiresAt:    issuedAt.Add(8 * time.Hour),

			RetrievedSourceIP: "198.51.100.44",
			RetrievedAt:       &issuedAt,
			EnrollmentID:      "3a2b1c0d-9e8f-4a7b-8c6d-5e4f3a2b1c0d",

			Extensions:      []string{"permit-pty", "permit-agent-forwarding"},
			CriticalOptions: map[string]string{"force-command": "/usr/local/bin/audit-shell"},

			DecidedByOutcome:         "approved",
			DecidedBySubject:         "9c1f0f8e-1d0a-4a37-9d1e-2f6a1b4c5d6e",
			DecidedByUsername:        "alice",
			DecidedByEmail:           "alice@example.org",
			DecidedByGroups:          []string{"engineering", "sre"},
			DecidedByOtherAccounts:   []string{"alice.other"},
			DecidedByServiceAccounts: []string{"svc-backup"},
			DecidedSourceIP:          "198.51.100.7",
			DecidedUserAgent:         "curl/8.0.0",
			DecidedAcceptLanguage:    "en-US",
			DecidedForwardedFor:      "198.51.100.7, 10.0.0.1",
			DecidedAt:                &issuedAt,
		},
	}
}

// zeroFixtures are the same types with nothing set. They document which
// fields disappear under omitempty, which is exactly the set the generated
// TypeScript marks optional — a field that becomes optional without the
// frontend learning about it is how a "cannot read property of undefined"
// reaches production.
func zeroFixtures() map[string]any {
	return map[string]any{
		"current_user":             webtypes.CurrentUserResponse{},
		"certificate_options":      webtypes.CertificateOptionsResponse{},
		"request_detail":           webtypes.RequestDetailResponse{},
		"certificate":              webtypes.CertificateResponse{},
		"service_enrollment":       webtypes.ServiceEnrollmentResponse{},
		"enrollment_retrievals":    webtypes.EnrollmentRetrievalsResponse{},
		"notification_preferences": webtypes.NotificationPreferencesResponse{},
	}
}

// errorFixtures are fully-populated error response envelopes, one for each
// error code. The envelope shape is part of the wire contract: changes to
// error_code or error require regenerating the spec and updating the frontend.
func errorFixtures() map[string]any {
	return map[string]any{
		"error_invalid_request": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "request body was malformed or a validation check failed",
			ErrorCode: apitypes.ErrorCodeInvalidRequest,
		},
		"error_unauthenticated": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "no valid session",
			ErrorCode: apitypes.ErrorCodeUnauthenticated,
		},
		"error_forbidden": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "access denied to this resource",
			ErrorCode: apitypes.ErrorCodeForbidden,
		},
		"error_not_found": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "certificate request \"9f1c0b2a-...\" not found",
			ErrorCode: apitypes.ErrorCodeNotFound,
		},
		"error_unavailable": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "certificate for request \"9f1c0b2a-...\" is no longer available",
			ErrorCode: apitypes.ErrorCodeUnavailable,
		},
		"error_rate_limited": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "too many requests",
			ErrorCode: apitypes.ErrorCodeRateLimited,
		},
		"error_not_implemented": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "not implemented",
			ErrorCode: apitypes.ErrorCodeNotImplemented,
		},
		"error_internal_error": openapidoc.ErrorEnvelope{
			Data:      nil,
			Error:     "internal server error",
			ErrorCode: apitypes.ErrorCodeInternalError,
		},
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

func TestErrorFixturesShouldMatchTheirGoldenJSON(t *testing.T) {
	for name, fixture := range errorFixtures() {
		t.Run(name, func(t *testing.T) {
			// Error responses intentionally have Data=nil, so skip assertAllFieldsSet.
			// The important part is that error_code doesn't drift from the spec.
			assertGolden(t, filepath.Join("testdata", name+".json"), fixture)
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
		t.Fatalf("failed to read golden %s: %v\nrun: go test ./server/webtypes/ -update", path, err)
	}

	if string(encoded) != string(want) {
		t.Errorf(`the wire shape changed.

golden %s
want:
%s
got:
%s
If this change is intended, then in the same commit:
  1. go test ./server/webtypes/ -update
  2. make types            (regenerates frontend/src/lib/api/generated/)
  3. make openapi          (regenerates docs/openapi.yaml)
Anything less leaves the frontend or the spec describing a response the
server no longer sends.`, path, want, encoded)
	}
}

// assertAllFieldsSet fails when any field reachable from v is still at its
// zero value, naming the path to it. Recurses into nested structs so a field
// added to CertificateOptionsResponse is caught inside RequestDetailResponse
// too.
//
// This is the half that catches additions. The golden diff catches renames
// and retypes on its own, but a new field with a zero value and an omitempty
// tag would produce an identical golden and slip through.
func assertAllFieldsSet(t *testing.T, v reflect.Value, path string) {
	t.Helper()

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
