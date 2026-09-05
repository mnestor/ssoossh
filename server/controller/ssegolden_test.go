package controller

// Test methodology: drive the real events handler for every terminal status
// and diff the raw response body against a checked-in golden.
//
// internal/apitypes's goldens pin the JSON an event carries. This one pins
// the framing around it, which is the half no other artifact states anywhere:
// docs/openapi.yaml says outright that an SSE payload's shape "is not in this
// document", because the response is a stream and there is nowhere in OpenAPI
// to declare one. So the only description of what ssoosshd actually writes on
// the wire here used to be prose.
//
// That matters because pam_ssoossh (github.com/mnestor/ssoossh-pam) is a
// separate project in C with its own hand-written SSE reader, and the exact
// bytes are easy to guess wrong:
//
//   - gin writes "event:approved", with no space after the colon. A parser
//     that splits on ": " (the spelling in every SSE tutorial, and what the
//     WHATWG grammar makes optional) matches nothing and hangs until timeout.
//   - The status is in the event name and nowhere else, so a reader that only
//     decodes the data line cannot tell approved from denied.
//   - The stream carries exactly one terminal event and then closes.
//
// Run `go test ./server/controller/ -update` to accept an intended change.

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

var updateSSE = flag.Bool("update", false, "rewrite the SSE golden files instead of comparing against them")

// sseEnrollmentExpiry matches the expires_at in
// internal/apitypes/testdata/sse_payload_enrolled.json, so the two halves of
// the contract describe one event rather than two unrelated examples.
var sseEnrollmentExpiry = time.Date(2026, 9, 5, 11, 34, 5, 0, time.UTC)

// sseOutcomes is one WaitOutcome per terminal status, populated exactly as
// the service populates it for that status: a certificate only on approved,
// the enrollment triple only on enrolled, nothing on the three failure
// outcomes.
func sseOutcomes() map[string]service.WaitOutcome {
	return map[string]service.WaitOutcome{
		apitypes.StatusApproved: {
			Status:      model.CertificateRequestStatusApproved,
			Certificate: "ssh-ed25519-cert-v01@openssh.com AAAAIExampleUserCert",
		},
		apitypes.StatusDenied:  {Status: model.CertificateRequestStatusDenied},
		apitypes.StatusExpired: {Status: model.CertificateRequestStatusExpired},
		apitypes.StatusEnrolled: {
			Status:         model.CertificateRequestStatusEnrolled,
			Code:           "K7M4-QP2X",
			ServiceAccount: "deploy",
			ExpiresAt:      sseEnrollmentExpiry,
		},
		apitypes.StatusFailed: {Status: model.CertificateRequestStatusFailed},
	}
}

// should write the same bytes for a given outcome as the golden records.
func TestEventsHandler_ShouldMatchItsSSEGolden(t *testing.T) {
	for status, outcome := range sseOutcomes() {
		t.Run(status, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			svc := &fakeCertRequestService{waitOutcome: outcome}

			r := gin.New()
			NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/certs/requests/req-1/events", nil)
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
			}

			assertSSEGolden(t, filepath.Join("testdata", "sse_stream_"+status+".sse"), w.Body.String())
		})
	}
}

// should cover every terminal status, so a status added to TerminalStatuses
// without a golden fails here rather than reaching a client that has never
// seen its framing.
func TestEventsHandlerGoldens_ShouldCoverEveryTerminalStatus(t *testing.T) {
	outcomes := sseOutcomes()

	for _, status := range apitypes.TerminalStatuses() {
		if _, ok := outcomes[status]; !ok {
			t.Errorf("terminal status %q has no SSE stream golden; add one so ssoossh-pam has a statement of the bytes that status produces", status)
		}
	}
}

// assertSSEGolden compares the raw stream against the file at path, or
// rewrites the file when -update is set. Compared as raw bytes rather than
// parsed: the framing is the thing under test, so normalizing it away first
// would defeat the point.
func assertSSEGolden(t *testing.T, path, got string) {
	t.Helper()

	if *updateSSE {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("failed to create testdata directory: %v", err)
		}
		//nolint:gosec // golden files are committed artifacts meant to be world-readable
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("failed to write golden %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read golden %s: %v\nrun: go test ./server/controller/ -update", path, err)
	}

	if got != string(want) {
		t.Errorf(`the SSE framing changed.

golden %s
want:
%q
got:
%q
These bytes are parsed by a hand-written reader in another repository. If the
change is intended, then in the same commit:
  1. go test ./server/controller/ -update
  2. make wire-contract    (bumps docs/wire-contract.json's version)
and open the matching change in github.com/mnestor/ssoossh-pam before
releasing either side.`, path, want, got)
	}
}
