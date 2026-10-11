package api

// Test methodology: unit tests against a real httptest.NewServer standing
// in for ssoosshd (matches how server/bootstrap fakes an external HTTP
// dependency, e.g. newTestOIDCProvider).

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetCA_ShouldReturnTheCAPublicKey(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ca" {
			t.Errorf("got path %q, want %q", r.URL.Path, "/api/ca")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ca":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIP/7mq9cQq4DRbk0doDBs9CsbCTEmTI1bSk1z8s1AdWc test-ca"},"error":null}`))
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
	if want := testCAKey; got != want {
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
	respErr := &ResponseError{}
	ok := errors.As(err, &respErr)
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

// testCAKey is a real ed25519 public key, since GetCA now parses what the
// server returns.
const testCAKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIP/7mq9cQq4DRbk0doDBs9CsbCTEmTI1bSk1z8s1AdWc test-ca"

func TestGetCA_ShouldRejectAnUnusableAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ca   string
		want string
	}{
		{
			name: "should reject an empty CA key",
			ca:   "",
			want: "failed to get CA public key: the server returned an empty CA key; the server may be misconfigured",
		},
		{
			name: "should reject a CA key of only whitespace",
			ca:   "  \\n ",
			want: "failed to get CA public key: the server returned an empty CA key; the server may be misconfigured",
		},
		{
			name: "should reject a CA key that does not parse",
			ca:   "not a key",
			want: "failed to get CA public key: the server returned a CA key that is not an SSH public key (\"not a key\"); the server may be misconfigured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"data":{"ca":"` + tt.ca + `"},"error":null}`))
			}))
			t.Cleanup(ts.Close)
			c, err := NewClient(Config{ServerURL: ts.URL})
			if err != nil {
				t.Fatalf("unexpected error building client: %v", err)
			}

			_, err = c.GetCA(context.Background())

			if err == nil || err.Error() != tt.want {
				t.Errorf("got %v, want %q", err, tt.want)
			}
		})
	}
}

func TestTruncateForError_ShouldShortenALongLine(t *testing.T) {
	t.Parallel()

	got := truncateForError(strings.Repeat("x", 50))
	if want := strings.Repeat("x", 40) + "..."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGetCA_ShouldAcceptOneKeyPerLine(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ca":"` + testCAKey + `\n` + testCAKey + `\n"},"error":null}`))
	}))
	t.Cleanup(ts.Close)
	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	if _, err := c.GetCA(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
