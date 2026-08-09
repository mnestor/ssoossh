package api

// Test methodology: unit tests for NewClient and its TLS fingerprint
// pinning, using httptest.NewTLSServer for a real (self-signed) TLS
// endpoint to connect to. Tests run in parallel where they don't share
// server state.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClient_ShouldErrorWhenServerURLEmpty(t *testing.T) {
	t.Parallel()

	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("expected an error for an empty ServerURL, got nil")
	}
}

// fingerprintOf returns the uppercase hex SHA-256 fingerprint of ts's
// certificate, in the same shape Config.SSLFingerprint expects.
func fingerprintOf(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	sum := sha256.Sum256(ts.Certificate().Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

func TestNewClient_SSLFingerprintPinning(t *testing.T) {
	t.Parallel()

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ca":"ssh-ed25519 AAAA..."}`))
	}))
	// t.Cleanup, not defer: the subtests below call t.Parallel(), which
	// pauses them and returns control to this function immediately — a
	// deferred Close() would run right then, before the subtests actually
	// execute. t.Cleanup runs after every subtest (parallel or not)
	// finishes instead.
	t.Cleanup(ts.Close)

	t.Run("should fail without SkipVerifySSL or a matching fingerprint (self-signed cert)", func(t *testing.T) {
		t.Parallel()

		c, err := NewClient(Config{ServerURL: ts.URL})
		if err != nil {
			t.Fatalf("unexpected error building client: %v", err)
		}

		if _, err := c.GetCA(context.Background()); err == nil {
			t.Error("expected the request to fail standard TLS verification against a self-signed cert, got nil")
		}
	})
}
