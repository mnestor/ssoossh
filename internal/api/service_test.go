package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRetrieveServiceCertificate_ShouldReturnCertificate(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/certs/service/retrieve" {
			t.Errorf("got path %q, want %q", r.URL.Path, "/api/certs/service/retrieve")
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"certificate":"ssh-ed25519-cert-v01@openssh.com AAAA... service"},"error":null}`))
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	got, err := c.RetrieveServiceCertificate(context.Background(), "enroll-code-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "ssh-ed25519-cert-v01@openssh.com AAAA... service"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if gotBody["code"] != "enroll-code-123" {
		t.Errorf("got code %v, want %q", gotBody["code"], "enroll-code-123")
	}
}

func TestRetrieveServiceCertificate_ShouldReturnResponseErrorOnFailure(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"data":null,"error":"invalid or already-redeemed code"}`))
	}))
	t.Cleanup(ts.Close)

	c, err := NewClient(Config{ServerURL: ts.URL})
	if err != nil {
		t.Fatalf("unexpected error building client: %v", err)
	}

	_, err = c.RetrieveServiceCertificate(context.Background(), "bad-code")
	respErr := &ResponseError{}
	ok := errors.As(err, &respErr)
	if !ok {
		t.Fatalf("expected a *ResponseError, got %T: %v", err, err)
	}
	if respErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("got status %d, want %d", respErr.StatusCode, http.StatusUnauthorized)
	}
}
