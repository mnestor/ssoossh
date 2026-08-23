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

// TestWaitForOutcome_ShouldTreatEnrolledAsTerminal is a regression test:
// "enrolled" was emitted by the server but missing from this client's
// terminal-status list, so a service enrollment blocked until the stream
// closed instead of resolving. Any status the server can send must be
// registered here — see apitypes.TerminalStatuses.
func TestWaitForOutcome_ShouldTreatEnrolledAsTerminal(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSEEvent(w, "enrolled", map[string]string{"code": "token-abc"})
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusEnrolled {
		t.Errorf("got status %q, want %q", result.Status, StatusEnrolled)
	}
	if result.Code != "token-abc" {
		t.Errorf("got code %q, want %q", result.Code, "token-abc")
	}
}

func TestWaitForOutcome_ShouldTreatFailedAsTerminal(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSSEEvent(w, "failed", map[string]string{})
	}))
	t.Cleanup(ts.Close)

	result, err := waitForOutcome(context.Background(), nil, ts.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusFailed {
		t.Errorf("got status %q, want %q", result.Status, StatusFailed)
	}
	if result.Certificate != "" {
		t.Errorf("expected no certificate on a failed outcome, got %q", result.Certificate)
	}
}

// TestWaitForOutcome_ShouldSurfaceGoneWithoutRetrying covers the ephemeral
// certificate case: the server answers 410 when a certificate is no longer
// available. That must surface as a *ResponseError the caller can act on,
// and must not send the client into a reconnect loop (410 is deliberately
// outside resty's retry conditions).
func TestWaitForOutcome_ShouldSurfaceGoneWithoutRetrying(t *testing.T) {
	t.Parallel()

	var attempts int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		_, _ = w.Write([]byte(`{"data":null,"error":"certificate is no longer available"}`))
	}))
	t.Cleanup(ts.Close)

	_, err := waitForOutcome(context.Background(), nil, ts.URL)

	respErr := &ResponseError{}
	ok := errors.As(err, &respErr)
	if !ok {
		t.Fatalf("expected a *ResponseError, got %T: %v", err, err)
	}
	if respErr.StatusCode != http.StatusGone {
		t.Errorf("got status %d, want %d", respErr.StatusCode, http.StatusGone)
	}
	if attempts != 1 {
		t.Errorf("expected exactly one attempt (410 must not be retried), got %d", attempts)
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
	respErr := &ResponseError{}
	ok := errors.As(err, &respErr)
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
