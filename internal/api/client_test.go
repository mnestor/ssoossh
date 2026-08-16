package api

// Test methodology: unit tests for NewClient and its TLS verification,
// using httptest.NewTLSServer for a real (self-signed) TLS endpoint to
// connect to. Tests run in parallel where they don't share server state.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewClient_ShouldErrorWhenServerURLEmpty(t *testing.T) {
	t.Parallel()

	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("expected an error for an empty ServerURL, got nil")
	}
}

func TestNewClient_TLSVerification(t *testing.T) {
	t.Parallel()

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"ca":"ssh-ed25519 AAAA..."},"error":null}`))
	}))
	// t.Cleanup, not defer: the subtests below call t.Parallel(), which
	// pauses them and returns control to this function immediately — a
	// deferred Close() would run right then, before the subtests actually
	// execute. t.Cleanup runs after every subtest (parallel or not)
	// finishes instead.
	t.Cleanup(ts.Close)

	t.Run("should fail without SkipVerifySSL against a self-signed cert", func(t *testing.T) {
		t.Parallel()

		c, err := NewClient(Config{ServerURL: ts.URL})
		if err != nil {
			t.Fatalf("unexpected error building client: %v", err)
		}

		if _, err := c.GetCA(context.Background()); err == nil {
			t.Error("expected the request to fail standard TLS verification against a self-signed cert, got nil")
		}
	})

	t.Run("should succeed with SkipVerifySSL against a self-signed cert", func(t *testing.T) {
		t.Parallel()

		c, err := NewClient(Config{ServerURL: ts.URL, SkipVerifySSL: true})
		if err != nil {
			t.Fatalf("unexpected error building client: %v", err)
		}

		if _, err := c.GetCA(context.Background()); err != nil {
			t.Errorf("unexpected error with SkipVerifySSL set: %v", err)
		}
	})
}
