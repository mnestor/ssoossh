package api

// Test methodology: unit tests for waitForOutcome (resty SSESource-backed)
// and decodeSSEConnectError, against a real httptest.NewServer. Broader
// create-then-wait flows (including the reconnect case) are covered in
// certrequest_test.go; these focus on the SSE-reading unit itself.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWaitForOutcome_ShouldDecodeApprovedEvent(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSEEvent(w, "approved", map[string]string{"certificate": "ssh-ed25519-cert-v01@openssh.com AAAA..."})
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusApproved {
		t.Errorf("got status %q, want %q", result.Status, StatusApproved)
	}
	if result.Certificate != "ssh-ed25519-cert-v01@openssh.com AAAA..." {
		t.Errorf("got certificate %q, want the signed certificate", result.Certificate)
	}
}

func TestWaitForOutcome_ShouldDecodeDeniedEventWithNoCertificate(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSEEvent(w, "denied", map[string]string{})
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusDenied {
		t.Errorf("got status %q, want %q", result.Status, StatusDenied)
	}
	if result.Certificate != "" {
		t.Errorf("got certificate %q, want empty", result.Certificate)
	}
}

func TestWaitForOutcome_ShouldErrorOnNon2xxConnect(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"data":null,"error":"certificate request \"abc\" not found"}`))
	}))
	t.Cleanup(ts.Close)

	_, err := waitForOutcome(context.Background(), nil, ts.URL)
	respErr, ok := err.(*ResponseError)
	if !ok {
		t.Fatalf("expected a *ResponseError, got %T: %v", err, err)
	}
	if respErr.StatusCode != http.StatusNotFound {
		t.Errorf("got status %d, want %d", respErr.StatusCode, http.StatusNotFound)
	}
	if respErr.Message != `certificate request "abc" not found` {
		t.Errorf("got message %q, want the decoded error body", respErr.Message)
	}
}

func TestWaitForOutcome_ShouldRespectContextCancellation(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := waitForOutcome(ctx, nil, ts.URL)
	if err == nil {
		t.Fatal("expected an error from an already-canceled context, got nil")
	}
}

func TestDecodeSSEConnectError_ShouldReadErrorBody(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"data":null,"error":"request expired"}`))
	}))
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL) //nolint:noctx // test helper, no cancellation needed
	if err != nil {
		t.Fatalf("unexpected error making request: %v", err)
	}

	respErr := decodeSSEConnectError(resp, errors.New("request failed"))
	if respErr.StatusCode != http.StatusGone {
		t.Errorf("got status %d, want %d", respErr.StatusCode, http.StatusGone)
	}
	if respErr.Message != "request expired" {
		t.Errorf("got message %q, want the decoded error body", respErr.Message)
	}
}
