package api

// Test methodology: unit tests against a real httptest.NewServer standing
// in for ssoosshd's two-call flow — POST to create (ordinary JSON,
// returning events_url/approval_url) then GET events_url for the actual
// SSE outcome, matching server/controller/certrequests.go's
// createUserRequestHandler + eventsHandler split.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// writeSSEEvent writes name/data in gin's c.SSEvent wire format:
// "event:<name>\ndata:<json>\n\n".
func writeSSEEvent(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/event-stream")
	encoded, _ := json.Marshal(data) //nolint:errcheck // test helper, inputs are always marshalable
	_, _ = w.Write([]byte("event:" + name + "\n"))
	_, _ = w.Write([]byte("data:" + string(encoded) + "\n\n"))
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// writeCreateResponse writes the (unwrapped, per this codebase's success-
// response convention — see server/controller/certrequests.go's
// createRequestResponse) JSON body a create call returns, with events_url
// pointing back at eventsPath on the same server.
func writeCreateResponse(w http.ResponseWriter, requestID, eventsPath string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{ //nolint:errcheck // test helper, encoding a static map never fails
		"request_id":   requestID,
		"events_url":   eventsPath,
		"approval_url": "/approve/" + requestID,
	})
}

func TestRequestUserCertificate_ShouldReturnApprovedOutcome(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/user":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody) //nolint:errcheck // test assertion, failure surfaces via the nil check below
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

	approvalURL, result, err := c.RequestUserCertificate(context.Background(), "ssh-ed25519 AAAA... test", RequestedOptions{Extensions: []string{"permit-pty"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if approvalURL != "/approve/req-1" {
		t.Errorf("got approvalURL %q, want %q", approvalURL, "/approve/req-1")
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

func TestRequestHostCertificate_ShouldSendHostname(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/certs/host/sign":
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &gotBody) //nolint:errcheck // test assertion, failure surfaces via the nil check below
			writeCreateResponse(w, "req-2", "/api/certs/requests/req-2/events")
		case r.Method == http.MethodGet && r.URL.Path == "/api/certs/requests/req-2/events":
			writeSSEEvent(w, "denied", map[string]string{})
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

	_, result, err := c.RequestHostCertificate(context.Background(), "ssh-ed25519 AAAA... host", "db01.internal", RequestedOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusDenied {
		t.Errorf("got status %q, want %q", result.Status, StatusDenied)
	}
	if gotBody["hostname"] != "db01.internal" {
		t.Errorf("got hostname %v, want %q", gotBody["hostname"], "db01.internal")
	}
}

func TestEnrollService_ShouldHitEnrollEndpoint(t *testing.T) {
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

	_, result, err := c.EnrollService(context.Background(), "ssh-ed25519 AAAA... service", RequestedOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusApproved {
		t.Errorf("got status %q, want %q", result.Status, StatusApproved)
	}
}

func TestRequestUserCertificate_ShouldReturnResponseErrorWhenCreateFails(t *testing.T) {
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

	_, _, err = c.RequestUserCertificate(context.Background(), "", RequestedOptions{})
	respErr, ok := err.(*ResponseError)
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

func TestRequestUserCertificate_ShouldReturnResponseErrorWhenEventsConnectionFails(t *testing.T) {
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

	approvalURL, result, err := c.RequestUserCertificate(context.Background(), "ssh-ed25519 AAAA... test", RequestedOptions{})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if approvalURL != "/approve/gone" {
		t.Errorf("got approvalURL %q, want it returned even though the wait failed", approvalURL)
	}
	if result != nil {
		t.Errorf("expected a nil result on failure, got %+v", result)
	}
}

// TestRequestUserCertificate_ShouldReconnectAfterDroppedEventsConnection
// simulates the events connection dropping once (server closes without a
// terminal event) before eventually resolving — resty's SSESource should
// reconnect on its own per the SSE spec, without any retry logic in this
// package.
func TestRequestUserCertificate_ShouldReconnectAfterDroppedEventsConnection(t *testing.T) {
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

	_, result, err := c.RequestUserCertificate(ctx, "ssh-ed25519 AAAA... test", RequestedOptions{})
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
