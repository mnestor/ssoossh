package api

// Test methodology: unit tests against a real httptest.NewServer that
// writes an SSE response in the same shape gin's c.SSEvent produces,
// verifying both the outgoing request body and the parsed outcome.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestRequestUserCertificate_ShouldReturnApprovedOutcome(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/certs/user" {
			t.Errorf("got path %q, want %q", r.URL.Path, "/api/certs/user")
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody) //nolint:errcheck // test assertion, failure surfaces via the nil check below

		writeSSEEvent(w, "approved", map[string]string{"certificate": "ssh-ed25519-cert-v01@openssh.com AAAA..."})
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	result, err := c.RequestUserCertificate(context.Background(), "ssh-ed25519 AAAA... test", RequestedOptions{Extensions: []string{"permit-pty"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "approved" {
		t.Errorf("got status %q, want %q", result.Status, "approved")
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
		if r.URL.Path != "/api/certs/host/sign" {
			t.Errorf("got path %q, want %q", r.URL.Path, "/api/certs/host/sign")
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody) //nolint:errcheck // test assertion, failure surfaces via the nil check below

		writeSSEEvent(w, "denied", map[string]string{})
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	result, err := c.RequestHostCertificate(context.Background(), "ssh-ed25519 AAAA... host", "db01.internal", RequestedOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "denied" {
		t.Errorf("got status %q, want %q", result.Status, "denied")
	}
	if gotBody["hostname"] != "db01.internal" {
		t.Errorf("got hostname %v, want %q", gotBody["hostname"], "db01.internal")
	}
}

func TestEnrollService_ShouldHitEnrollEndpoint(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/certs/service/enroll" {
			t.Errorf("got path %q, want %q", r.URL.Path, "/api/certs/service/enroll")
		}
		writeSSEEvent(w, "approved", map[string]string{})
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	result, err := c.EnrollService(context.Background(), "ssh-ed25519 AAAA... service", RequestedOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != "approved" {
		t.Errorf("got status %q, want %q", result.Status, "approved")
	}
}

func TestStreamCertificateRequest_ShouldReturnResponseErrorOnNon2xx(t *testing.T) {
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

	_, err = c.RequestUserCertificate(context.Background(), "", RequestedOptions{})
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
