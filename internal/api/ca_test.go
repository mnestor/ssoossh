package api

// Test methodology: unit tests against a real httptest.NewServer standing
// in for ssoosshd (matches how server/bootstrap fakes an external HTTP
// dependency, e.g. newTestOIDCProvider).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetCA_ShouldReturnTheCAPublicKey(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ca" {
			t.Errorf("got path %q, want %q", r.URL.Path, "/api/ca")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ca":"ssh-ed25519 AAAA... ca"}`))
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	got, err := c.GetCA(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "ssh-ed25519 AAAA... ca"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGetCA_ShouldReturnResponseErrorOnFailure(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"data":null,"error":"CA key not configured"}`))
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	_, err = c.GetCA(context.Background())
	respErr, ok := err.(*ResponseError)
	if !ok {
		t.Fatalf("expected a *ResponseError, got %T: %v", err, err)
	}
	if respErr.StatusCode != http.StatusInternalServerError {
		t.Errorf("got status %d, want %d", respErr.StatusCode, http.StatusInternalServerError)
	}
	if respErr.Message != "CA key not configured" {
		t.Errorf("got message %q, want %q", respErr.Message, "CA key not configured")
	}
}
