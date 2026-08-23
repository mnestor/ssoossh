//go:build pam

package main

// Test methodology: config-validation cases run with no server at all
// (Authenticate must fail before any network call); everything past that —
// the actual create-then-await flow, all four checks wired together, error
// classification — runs against a real httptest.Server standing in for
// ssoosshd, signing genuinely CA-signed certificates for whatever public key
// the request under test posted. This is what
// what the PAM design called "the full
// authenticate path against a fake server", and — per the same doc's Work
// item 6, which explicitly leaves the choice between a real `sudo` tier and
// calling Authenticate directly to a judgment call and recommends "direct
// Authenticate in the PR gate" — this is that: a real OIDC browser and a
// real ssoosshd are deferred to the phase 7 rehearsal, not wired up here.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/api"
)

// stubLogger is a no-op Logger for tests that don't care about log output.
type stubLogger struct{}

func (stubLogger) Debugf(format string, v ...any)   {}
func (stubLogger) Infof(format string, v ...any)    {}
func (stubLogger) Noticef(format string, v ...any)  {}
func (stubLogger) Warningf(format string, v ...any) {}
func (stubLogger) Errorf(format string, v ...any)   {}
func (stubLogger) SetDebug(d string)                {}
func (stubLogger) Close() error                     { return nil }

// fakeConversation is a Conversation that records every message shown,
// standing in for the real PAM conversation function.
type fakeConversation struct {
	shown []string
}

func (f *fakeConversation) Info(msg string) error {
	f.shown = append(f.shown, msg)
	return nil
}

func TestAuthenticate_ConfigValidation(t *testing.T) {
	validCAFile := writeAuthorizedKeysFile(t, newTestCA(t).publicKey())
	garbageCAFile := filepath.Join(t.TempDir(), "garbage.pub")
	if err := os.WriteFile(garbageCAFile, []byte("not a key\n"), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	tests := []struct {
		name     string
		cfg      config
		wantCode int
	}{
		{
			name:     "should return PamUserUnknown when server is not configured",
			cfg:      config{trustedCAFile: validCAFile, waitTimeout: time.Second},
			wantCode: PamUserUnknown,
		},
		{
			name:     "should return PamNoModuleData when trusted-ca-file is not configured",
			cfg:      config{server: "https://example.invalid", waitTimeout: time.Second},
			wantCode: PamNoModuleData,
		},
		{
			name:     "should return PamNoModuleData when trusted-ca-file cannot be parsed",
			cfg:      config{server: "https://example.invalid", trustedCAFile: garbageCAFile, waitTimeout: time.Second},
			wantCode: PamNoModuleData,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log Logger = stubLogger{}
			gotCode, gotErr := Authenticate(context.Background(), log, &fakeConversation{}, "alice", tt.cfg)
			if gotCode != tt.wantCode {
				t.Errorf("Authenticate() code = %d, want %d", gotCode, tt.wantCode)
			}
			if gotErr == nil {
				t.Error("Authenticate() err = nil, want non-nil for a failure code")
			}
		})
	}
}

// should turn every terminal status ssoosshd can send into the right PAM
// code, certificate, and error — including a nil result and an unrecognized
// status, neither of which the end-to-end Authenticate tests exercise.
func TestOutcomeCertificate(t *testing.T) {
	tests := []struct {
		name       string
		result     *api.CertificateResult
		wantCode   int
		wantCert   string
		wantErrNil bool
	}{
		{
			name:     "should error when the result is nil",
			result:   nil,
			wantCode: PamAuthErr,
		},
		{
			name:       "should return the certificate when approved",
			result:     &api.CertificateResult{Status: api.StatusApproved, Certificate: "cert-data"},
			wantCode:   PamSuccess,
			wantCert:   "cert-data",
			wantErrNil: true,
		},
		{
			name:     "should error when approved but no certificate was delivered",
			result:   &api.CertificateResult{Status: api.StatusApproved, Certificate: ""},
			wantCode: PamAuthErr,
		},
		{
			name:     "should error when denied",
			result:   &api.CertificateResult{Status: api.StatusDenied},
			wantCode: PamAuthErr,
		},
		{
			name:     "should error when expired",
			result:   &api.CertificateResult{Status: api.StatusExpired},
			wantCode: PamAuthErr,
		},
		{
			name:     "should error when signing failed server-side",
			result:   &api.CertificateResult{Status: api.StatusFailed},
			wantCode: PamAuthErr,
		},
		{
			name:     "should error on an unrecognized status",
			result:   &api.CertificateResult{Status: "something-new"},
			wantCode: PamAuthErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, cert, err := outcomeCertificate(tt.result)
			if code != tt.wantCode {
				t.Errorf("outcomeCertificate() code = %d, want %d", code, tt.wantCode)
			}
			if cert != tt.wantCert {
				t.Errorf("outcomeCertificate() cert = %q, want %q", cert, tt.wantCert)
			}
			if (err == nil) != tt.wantErrNil {
				t.Errorf("outcomeCertificate() err = %v, want nil: %v", err, tt.wantErrNil)
			}
		})
	}
}

// pamRequestBody mirrors just the fields of apitypes.PAMRequestBody this
// test needs to read back off the wire.
type pamRequestBody struct {
	PublicKey string `json:"public_key"`
	Username  string `json:"username"`
}

// writeEnvelope writes payload in the {data, error} envelope every JSON
// response from ssoosshd uses.
func writeEnvelope(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck // test helper, encoding a static map never fails
		"data":  payload,
		"error": nil,
	})
}

// writeSSEEvent writes name/data in gin's c.SSEvent wire format.
func writeSSEEvent(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/event-stream")
	encoded, _ := json.Marshal(map[string]any{"data": data, "error": nil}) //nolint:errcheck // test helper, inputs are always marshalable
	_, _ = w.Write([]byte("event:" + name + "\n"))
	_, _ = w.Write([]byte("data:" + string(encoded) + "\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// newPAMTestServerResolving is a fake ssoosshd exposing just POST
// /api/certs/pam and its events endpoint: it captures the posted public key
// and username, and lets resolve decide what the events endpoint sends back.
func newPAMTestServerResolving(t *testing.T, resolve func(pub ssh.PublicKey, username string) (event, certificate string)) *httptest.Server {
	t.Helper()

	type captured struct {
		pub      ssh.PublicKey
		username string
	}
	got := make(chan captured, 1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/pam":
			body, _ := io.ReadAll(r.Body)
			var req pamRequestBody
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("harness: failed to decode request body: %v", err)
				return
			}
			pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.PublicKey))
			if err != nil {
				t.Errorf("harness: failed to parse posted public key: %v", err)
				return
			}
			got <- captured{pub: pub, username: req.Username}
			writeEnvelope(w, map[string]string{
				"request_id":   "req-pam",
				"events_url":   "/api/certs/requests/req-pam/events",
				"approval_url": "/approve/req-pam",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/certs/requests/req-pam/events":
			c := <-got
			event, certificate := resolve(c.pub, c.username)
			writeSSEEvent(w, event, map[string]string{"certificate": certificate})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestAuthenticate_ShouldSucceedAgainstAFakeServer(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeAuthorizedKeysFile(t, ca.publicKey())

	ts := newPAMTestServerResolving(t, func(pub ssh.PublicKey, username string) (string, string) {
		now := time.Now()
		cert := ca.sign(t, pub, []string{username}, now.Add(-time.Second), now.Add(time.Minute))
		return "approved", string(ssh.MarshalAuthorizedKey(cert))
	})

	cfg := config{server: ts.URL, trustedCAFile: caFile, waitTimeout: 5 * time.Second, skewTolerance: 2 * time.Second}
	conv := &fakeConversation{}

	code, err := Authenticate(context.Background(), stubLogger{}, conv, "alice", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != PamSuccess {
		t.Errorf("got code %d, want PamSuccess", code)
	}
	if len(conv.shown) == 0 {
		t.Error("expected the approval URL to be displayed through the PAM conversation")
	}
}

// erroringConversation always fails to display the approval URL, standing
// in for a PAM conversation function that can't reach the terminal.
type erroringConversation struct{}

func (erroringConversation) Info(msg string) error {
	return errors.New("conversation function unavailable")
}

// TestAuthenticate_ShouldSucceedEvenWhenTheConversationFails covers the
// non-fatal Warningf branch in Authenticate: the human never sees the
// approval URL through this channel, but the request still resolves.
func TestAuthenticate_ShouldSucceedEvenWhenTheConversationFails(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeAuthorizedKeysFile(t, ca.publicKey())

	ts := newPAMTestServerResolving(t, func(pub ssh.PublicKey, username string) (string, string) {
		now := time.Now()
		cert := ca.sign(t, pub, []string{username}, now.Add(-time.Second), now.Add(time.Minute))
		return "approved", string(ssh.MarshalAuthorizedKey(cert))
	})

	cfg := config{server: ts.URL, trustedCAFile: caFile, waitTimeout: 5 * time.Second, skewTolerance: 2 * time.Second}

	code, err := Authenticate(context.Background(), stubLogger{}, erroringConversation{}, "alice", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if code != PamSuccess {
		t.Errorf("got code %d, want PamSuccess", code)
	}
}

// TestAuthenticate_ShouldRejectAnUnparseableCertificate covers a server that
// reports "approved" but sends back something that isn't a valid
// authorized_keys-format certificate string.
func TestAuthenticate_ShouldRejectAnUnparseableCertificate(t *testing.T) {
	caFile := writeAuthorizedKeysFile(t, newTestCA(t).publicKey())

	ts := newPAMTestServerResolving(t, func(_ ssh.PublicKey, _ string) (string, string) {
		return "approved", "not a certificate"
	})

	cfg := config{server: ts.URL, trustedCAFile: caFile, waitTimeout: 5 * time.Second, skewTolerance: 2 * time.Second}

	code, err := Authenticate(context.Background(), stubLogger{}, &fakeConversation{}, "alice", cfg)
	if err == nil {
		t.Fatal("expected an error: the certificate string is unparseable")
	}
	if code != PamAuthErr {
		t.Errorf("got code %d, want PamAuthErr", code)
	}
}

// TestAuthenticate_ShouldRejectWhenPrincipalsExcludeTheAuthenticatingUser
// covers check 3 wired through the full flow: a CA-signed certificate,
// issued to the right key, but naming a principal other than the account
// PAM is authenticating.
func TestAuthenticate_ShouldRejectWhenPrincipalsExcludeTheAuthenticatingUser(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeAuthorizedKeysFile(t, ca.publicKey())

	ts := newPAMTestServerResolving(t, func(pub ssh.PublicKey, _ string) (string, string) {
		now := time.Now()
		cert := ca.sign(t, pub, []string{"somebody-else"}, now.Add(-time.Second), now.Add(time.Minute))
		return "approved", string(ssh.MarshalAuthorizedKey(cert))
	})

	cfg := config{server: ts.URL, trustedCAFile: caFile, waitTimeout: 5 * time.Second, skewTolerance: 2 * time.Second}

	code, err := Authenticate(context.Background(), stubLogger{}, &fakeConversation{}, "alice", cfg)
	if err == nil {
		t.Fatal("expected an error: the certificate's principals do not include the authenticating user")
	}
	if code != PamAuthErr {
		t.Errorf("got code %d, want PamAuthErr", code)
	}
}

func TestAuthenticate_ShouldRejectApprovedCertificateIssuedToADifferentKey(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeAuthorizedKeysFile(t, ca.publicKey())
	attacker := newTestKeypair(t)

	ts := newPAMTestServerResolving(t, func(_ ssh.PublicKey, username string) (string, string) {
		// Simulate a compromised or malicious server response naming a
		// certificate for someone else's key rather than the one this
		// request generated — exactly the attack check 2 exists to catch.
		now := time.Now()
		cert := ca.sign(t, attacker.Public(), []string{username}, now.Add(-time.Second), now.Add(time.Minute))
		return "approved", string(ssh.MarshalAuthorizedKey(cert))
	})

	cfg := config{server: ts.URL, trustedCAFile: caFile, waitTimeout: 5 * time.Second, skewTolerance: 2 * time.Second}

	code, err := Authenticate(context.Background(), stubLogger{}, &fakeConversation{}, "alice", cfg)
	if err == nil {
		t.Fatal("expected an error: the certificate was issued to a different key")
	}
	if code != PamAuthErr {
		t.Errorf("got code %d, want PamAuthErr", code)
	}
}

func TestAuthenticate_ShouldRejectCertificateSignedByAnUntrustedCA(t *testing.T) {
	trusted := newTestCA(t)
	untrusted := newTestCA(t)
	caFile := writeAuthorizedKeysFile(t, trusted.publicKey())

	ts := newPAMTestServerResolving(t, func(pub ssh.PublicKey, username string) (string, string) {
		now := time.Now()
		cert := untrusted.sign(t, pub, []string{username}, now.Add(-time.Second), now.Add(time.Minute))
		return "approved", string(ssh.MarshalAuthorizedKey(cert))
	})

	cfg := config{server: ts.URL, trustedCAFile: caFile, waitTimeout: 5 * time.Second, skewTolerance: 2 * time.Second}

	code, err := Authenticate(context.Background(), stubLogger{}, &fakeConversation{}, "alice", cfg)
	if err == nil {
		t.Fatal("expected an error: the certificate is not signed by a trusted CA")
	}
	if code != PamAuthErr {
		t.Errorf("got code %d, want PamAuthErr", code)
	}
}

func TestAuthenticate_ShouldRejectAnExpiredCertificate(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeAuthorizedKeysFile(t, ca.publicKey())

	ts := newPAMTestServerResolving(t, func(pub ssh.PublicKey, username string) (string, string) {
		now := time.Now()
		cert := ca.sign(t, pub, []string{username}, now.Add(-time.Hour), now.Add(-time.Minute))
		return "approved", string(ssh.MarshalAuthorizedKey(cert))
	})

	cfg := config{server: ts.URL, trustedCAFile: caFile, waitTimeout: 5 * time.Second, skewTolerance: 2 * time.Second}

	code, err := Authenticate(context.Background(), stubLogger{}, &fakeConversation{}, "alice", cfg)
	if err == nil {
		t.Fatal("expected an error: the certificate has expired")
	}
	if code != PamAuthErr {
		t.Errorf("got code %d, want PamAuthErr", code)
	}
}

func TestAuthenticate_ShouldRejectWhenDenied(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeAuthorizedKeysFile(t, ca.publicKey())

	ts := newPAMTestServerResolving(t, func(_ ssh.PublicKey, _ string) (string, string) {
		return "denied", ""
	})

	cfg := config{server: ts.URL, trustedCAFile: caFile, waitTimeout: 5 * time.Second, skewTolerance: 2 * time.Second}

	code, err := Authenticate(context.Background(), stubLogger{}, &fakeConversation{}, "alice", cfg)
	if err == nil {
		t.Fatal("expected an error for a denied request")
	}
	if code != PamAuthErr {
		t.Errorf("got code %d, want PamAuthErr", code)
	}
}

// TestAuthenticate_ShouldRejectWhenSigningFailed covers the "failed" SSE
// outcome: the request reached ssoosshd and was processed (unlike the
// unreachable-server case below), but signing itself failed server-side.
// Deliberately PamAuthErr rather than PamAuthInfoUnavail — see the comment
// on outcomeCertificate's StatusFailed case in auth.go.
func TestAuthenticate_ShouldRejectWhenSigningFailed(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeAuthorizedKeysFile(t, ca.publicKey())

	ts := newPAMTestServerResolving(t, func(_ ssh.PublicKey, _ string) (string, string) {
		return "failed", ""
	})

	cfg := config{server: ts.URL, trustedCAFile: caFile, waitTimeout: 5 * time.Second, skewTolerance: 2 * time.Second}

	code, err := Authenticate(context.Background(), stubLogger{}, &fakeConversation{}, "alice", cfg)
	if err == nil {
		t.Fatal("expected an error when signing fails server-side")
	}
	if code != PamAuthErr {
		t.Errorf("got code %d, want PamAuthErr", code)
	}
}

func TestAuthenticate_ShouldFailFastWhenServerUnreachable(t *testing.T) {
	caFile := writeAuthorizedKeysFile(t, newTestCA(t).publicKey())

	// Nothing listens here; connection refused. Fails fast rather than
	// hanging so the PAM stack can fall through to whatever comes next in
	// /etc/pam.d — see docs/pam.d-sudo.example.
	cfg := config{server: "http://127.0.0.1:1", trustedCAFile: caFile, waitTimeout: 5 * time.Second, skewTolerance: 2 * time.Second}

	code, err := Authenticate(context.Background(), stubLogger{}, &fakeConversation{}, "alice", cfg)
	if err == nil {
		t.Fatal("expected an error when the server is unreachable")
	}
	if code != PamAuthInfoUnavail {
		t.Errorf("got code %d, want PamAuthInfoUnavail", code)
	}
}

func TestAuthenticate_ShouldAbandonOnTimeout(t *testing.T) {
	caFile := writeAuthorizedKeysFile(t, newTestCA(t).publicKey())

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/pam":
			writeEnvelope(w, map[string]string{
				"request_id":   "req-pam",
				"events_url":   "/api/certs/requests/req-pam/events",
				"approval_url": "/approve/req-pam",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/certs/requests/req-pam/events":
			// Nobody ever approves: stands in for a human who never opens
			// the browser. Blocks on the request's own context rather than a
			// separate channel, so the handler unblocks itself the moment
			// the client (Authenticate, via ctx) gives up and the
			// connection is torn down — no coordination with test cleanup
			// needed.
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)

	cfg := config{server: ts.URL, trustedCAFile: caFile, waitTimeout: 100 * time.Millisecond, skewTolerance: 2 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.waitTimeout)
	defer cancel()

	code, err := Authenticate(ctx, stubLogger{}, &fakeConversation{}, "alice", cfg)
	if err == nil {
		t.Fatal("expected an error when the wait times out")
	}
	if code != PamAuthErr {
		t.Errorf("got code %d, want PamAuthErr", code)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected the error to wrap context.DeadlineExceeded, got %v", err)
	}
}

func TestAuthenticate_ShouldAbandonOnCancellation(t *testing.T) {
	caFile := writeAuthorizedKeysFile(t, newTestCA(t).publicKey())

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/pam":
			writeEnvelope(w, map[string]string{
				"request_id":   "req-pam",
				"events_url":   "/api/certs/requests/req-pam/events",
				"approval_url": "/approve/req-pam",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/certs/requests/req-pam/events":
			// See TestAuthenticate_ShouldAbandonOnTimeout: unblocks itself
			// via the request's own context once the client (Authenticate)
			// gives up.
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)

	cfg := config{server: ts.URL, trustedCAFile: caFile, waitTimeout: 5 * time.Second, skewTolerance: 2 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	code, err := Authenticate(ctx, stubLogger{}, &fakeConversation{}, "alice", cfg)

	if err == nil {
		t.Fatal("expected an error when the context is cancelled (Ctrl-C at the sudo prompt)")
	}
	if code != PamAuthErr {
		t.Errorf("got code %d, want PamAuthErr", code)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected the error to wrap context.Canceled, got %v", err)
	}
}

// TestAuthenticate_EveryFailureCodeHasANonNilError is the structural
// counterpart to pam_ssoossh.go's fix: Authenticate itself must never
// return a failure code with a nil error, independent of the caller's own
// logging discipline. Exercises every branch above via the same table shape
// rather than duplicating assertions per test.
func TestAuthenticate_EveryFailureCodeHasANonNilError(t *testing.T) {
	ca := newTestCA(t)
	caFile := writeAuthorizedKeysFile(t, ca.publicKey())

	cases := []struct {
		name string
		cfg  config
	}{
		{name: "no server", cfg: config{trustedCAFile: caFile, waitTimeout: time.Second}},
		{name: "no trusted CA file", cfg: config{server: "https://example.invalid", waitTimeout: time.Second}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, err := Authenticate(context.Background(), stubLogger{}, &fakeConversation{}, "alice", tc.cfg)
			if code == PamSuccess {
				t.Fatal("expected a failure code")
			}
			if err == nil {
				t.Errorf("failure code %d returned with a nil error", code)
			}
		})
	}
}
