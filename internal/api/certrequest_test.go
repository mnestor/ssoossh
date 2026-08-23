package api

// Test methodology: unit tests against a real httptest.NewServer standing
// in for ssoosshd's two-call flow — POST to create (ordinary JSON,
// returning events_url/approval_url) then GET events_url for the actual
// SSE outcome, matching server/controller/certrequests.go's
// createUserRequestHandler + eventsHandler split.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// writeSSEEvent writes name/data in gin's c.SSEvent wire format:
// "event:<name>\ndata:<json>\n\n", with data wrapped in the {data, error}
// envelope every JSON body this API emits.
func writeSSEEvent(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/event-stream")
	encoded, _ := json.Marshal(map[string]any{"data": data, "error": nil})
	_, _ = w.Write([]byte("event:" + name + "\n"))
	_, _ = w.Write([]byte("data:" + string(encoded) + "\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// writeCreateResponse writes the enveloped JSON body a create call
// returns, with events_url pointing back at eventsPath on the same server.
func writeCreateResponse(w http.ResponseWriter, requestID, eventsPath string) {
	writeEnvelope(w, map[string]string{
		"request_id":   requestID,
		"events_url":   eventsPath,
		"approval_url": "/approve/" + requestID,
	})
}

// writeEnvelope writes payload in the {data, error} envelope every JSON
// response from ssoosshd uses (see apitypes.Envelope).
func writeEnvelope(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data":  payload,
		"error": nil,
	})
}

func TestCreateUserRequest_ShouldReturnApprovedOutcome(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/user":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			writeCreateResponse(w, "req-1", "/api/certs/requests/req-1/events")
		case r.Method == http.MethodGet && r.URL.Path == "/api/certs/requests/req-1/events":
			writeSSEEvent(w, "approved", map[string]string{"certificate": "ssh-ed25519-cert-v01@openssh.com AAAA..."})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	pending, err := c.CreateUserRequest(context.Background(), "ssh-ed25519 AAAA... test", "alice", "alice-laptop", RequestedOptions{Extensions: []string{"permit-pty"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pending.ApprovalURL != ts.URL+"/approve/req-1" {
		t.Errorf("got approvalURL %q, want %q", pending.ApprovalURL, ts.URL+"/approve/req-1")
	}

	result, err := c.AwaitCertificate(context.Background(), pending)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusApproved {
		t.Errorf("got status %q, want %q", result.Status, StatusApproved)
	}
	if result.Certificate != "ssh-ed25519-cert-v01@openssh.com AAAA..." {
		t.Errorf("got certificate %q, want the signed certificate", result.Certificate)
	}

	if gotBody == nil {
		t.Fatal("server did not receive a request body")
	}
	if gotBody["public_key"] != "ssh-ed25519 AAAA... test" {
		t.Errorf("got public_key %v, want the requested key", gotBody["public_key"])
	}
	opts, ok := gotBody["requested_options"].(map[string]any)
	if !ok {
		t.Fatalf("expected requested_options in the request body, got %v", gotBody)
	}
	exts, ok := opts["extensions"].([]any)
	if !ok || len(exts) != 1 || exts[0] != "permit-pty" {
		t.Errorf("expected requested_options.extensions to round-trip [\"permit-pty\"], got %v", opts["extensions"])
	}
}

func TestCreateServiceEnrollment_ShouldHitEnrollEndpoint(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/service/enroll":
			writeCreateResponse(w, "req-3", "/api/certs/requests/req-3/events")
		case r.Method == http.MethodGet && r.URL.Path == "/api/certs/requests/req-3/events":
			writeSSEEvent(w, "approved", map[string]string{})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	pending, err := c.CreateServiceEnrollment(context.Background(), "ssh-ed25519 AAAA... service", RequestedOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, err := c.AwaitCertificate(context.Background(), pending)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusApproved {
		t.Errorf("got status %q, want %q", result.Status, StatusApproved)
	}
}

func TestCreatePAMRequest_ShouldSendUsername(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/pam":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody)
			writeCreateResponse(w, "req-4", "/api/certs/requests/req-4/events")
		case r.Method == http.MethodGet && r.URL.Path == "/api/certs/requests/req-4/events":
			writeSSEEvent(w, "approved", map[string]string{"certificate": "ssh-ed25519-cert-v01@openssh.com AAAA..."})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	pending, err := c.CreatePAMRequest(context.Background(), "ssh-ed25519 AAAA... test", "alice", RequestedOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, err := c.AwaitCertificate(context.Background(), pending)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusApproved {
		t.Errorf("got status %q, want %q", result.Status, StatusApproved)
	}
	if gotBody["username"] != "alice" {
		t.Errorf("got username %v, want %q", gotBody["username"], "alice")
	}
}

func TestCreateUserRequest_ShouldReturnResponseErrorWhenCreateFails(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"data":null,"error":"public key is required"}`))
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	_, err = c.CreateUserRequest(context.Background(), "", "", "", RequestedOptions{})
	respErr := &ResponseError{}
	ok := errors.As(err, &respErr)
	if !ok {
		t.Fatalf("expected a *ResponseError, got %T: %v", err, err)
	}
	if respErr.StatusCode != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", respErr.StatusCode, http.StatusBadRequest)
	}
	if respErr.Message != "public key is required" {
		t.Errorf("got message %q, want %q", respErr.Message, "public key is required")
	}
}

func TestAwaitCertificate_ShouldReturnResponseErrorWhenEventsConnectionFails(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/user":
			writeCreateResponse(w, "gone", "/api/certs/requests/gone/events")
		case r.Method == http.MethodGet && r.URL.Path == "/api/certs/requests/gone/events":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"data":null,"error":"certificate request \"gone\" not found"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	pending, err := c.CreateUserRequest(context.Background(), "ssh-ed25519 AAAA... test", "", "", RequestedOptions{})
	if err != nil {
		t.Fatalf("unexpected error creating the request: %v", err)
	}
	if pending.ApprovalURL != ts.URL+"/approve/gone" {
		t.Errorf("got approvalURL %q, want it available before the wait is attempted", pending.ApprovalURL)
	}

	result, err := c.AwaitCertificate(context.Background(), pending)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if result != nil {
		t.Errorf("expected a nil result on failure, got %+v", result)
	}
}

// TestAwaitCertificate_ShouldReconnectAfterDroppedEventsConnection
// simulates the events connection dropping once (server closes without a
// terminal event) before eventually resolving — resty's SSESource should
// reconnect on its own per the SSE spec, without any retry logic in this
// package.
func TestAwaitCertificate_ShouldReconnectAfterDroppedEventsConnection(t *testing.T) {
	t.Parallel()

	var eventsHits int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/user":
			writeCreateResponse(w, "req-flaky", "/api/certs/requests/req-flaky/events")
		case r.Method == http.MethodGet && r.URL.Path == "/api/certs/requests/req-flaky/events":
			hit := atomic.AddInt64(&eventsHits, 1)
			if hit == 1 {
				// First connection: close immediately with no event, as if
				// an idle proxy dropped it while the request was still
				// pending.
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				return
			}
			writeSSEEvent(w, "approved", map[string]string{"certificate": "ssh-ed25519-cert-v01@openssh.com AAAA..."})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pending, err := c.CreateUserRequest(ctx, "ssh-ed25519 AAAA... test", "", "", RequestedOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	result, err := c.AwaitCertificate(ctx, pending)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusApproved {
		t.Errorf("got status %q, want %q", result.Status, StatusApproved)
	}
	if hits := atomic.LoadInt64(&eventsHits); hits < 2 {
		t.Errorf("expected at least 2 events connections (initial + reconnect), got %d", hits)
	}
}

// TestCreateUserRequest_ShouldReturnApprovalURLBeforeAnyoneApproves is the
// regression test for the shape this API used to have: create and wait were
// one call, so the approval URL only came back once approval had already
// happened. `ssh login` cannot work that way — it has to print the URL while
// the request is still pending.
func TestCreateUserRequest_ShouldReturnApprovalURLBeforeAnyoneApproves(t *testing.T) {
	t.Parallel()

	approved := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/user":
			writeCreateResponse(w, "req-slow", "/api/certs/requests/req-slow/events")
		case r.Method == http.MethodGet && r.URL.Path == "/api/certs/requests/req-slow/events":
			// Stands in for a human taking their time: no terminal event
			// until the test says so.
			<-approved
			writeSSEEvent(w, "approved", map[string]string{"certificate": "ssh-ed25519-cert-v01@openssh.com AAAA..."})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ts.Close)
	t.Cleanup(func() {
		select {
		case <-approved:
		default:
			close(approved)
		}
	})

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The point of the test: this returns while the request is unresolved.
	pending, err := c.CreateUserRequest(ctx, "ssh-ed25519 AAAA... test", "", "", RequestedOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pending.ApprovalURL == "" {
		t.Fatal("expected an approval URL while the request was still pending")
	}

	waited := make(chan *CertificateResult, 1)
	go func() {
		result, _ := c.AwaitCertificate(ctx, pending)
		waited <- result
	}()

	select {
	case <-waited:
		t.Fatal("the wait resolved before anything approved the request")
	case <-time.After(100 * time.Millisecond):
	}

	close(approved)
	select {
	case result := <-waited:
		if result == nil || result.Status != StatusApproved {
			t.Errorf("got %+v, want an approved result once the request resolved", result)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for the outcome")
	}
}

// TestAwaitCertificate_ShouldRejectRequestItDidNotCreate covers the zero
// value a caller could hand-build: without the events URL there is nothing
// to connect to, and a confusing URL-parse failure is worse than saying so.
func TestAwaitCertificate_ShouldRejectRequestItDidNotCreate(t *testing.T) {
	t.Parallel()

	c, err := NewClient(Config{ServerURL: "https://ssh.example.com"})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	if _, err := c.AwaitCertificate(context.Background(), &PendingRequest{RequestID: "made-up"}); err == nil {
		t.Error("expected an error for a request this client did not create")
	}
}

func TestNewClient_ShouldAssumeHTTPSWhenTheServerURLHasNoScheme(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "bare host", in: "ssh.example.com", want: "https://ssh.example.com"},
		{name: "trailing slash", in: "https://ssh.example.com/", want: "https://ssh.example.com"},
		{name: "explicit http is kept", in: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{name: "surrounding space", in: "  ssh.example.com  ", want: "https://ssh.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := NewClient(Config{ServerURL: tt.in})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.serverURL != tt.want {
				t.Errorf("got serverURL %q, want %q", c.serverURL, tt.want)
			}
		})
	}
}
