package errorresponses

// Test methodology: Unit tests for custom error types and response behavior.
// Tests run in parallel (t.Parallel()). Uses table-driven tests to verify
// error messages and HTTP status codes. Each test verifies one error type
// or behavior.

import (
	"net/http"
	"testing"
)

func TestTooManyRequestsError_ShouldReturnMessageFromError(t *testing.T) {
	t.Parallel()

	err := &TooManyRequestsError{}
	if got := err.Error(); got != "Too many requests" {
		t.Errorf("got %q, want %q", got, "Too many requests")
	}
}

func TestTooManyRequestsError_ShouldReturn429FromHttpStatusCode(t *testing.T) {
	t.Parallel()

	err := &TooManyRequestsError{}
	if got := err.HTTPStatusCode(); got != http.StatusTooManyRequests {
		t.Errorf("got %d, want %d", got, http.StatusTooManyRequests)
	}
}

func TestMisdirectedRequestError_ShouldReturnMessageFromError(t *testing.T) {
	t.Parallel()

	err := &MisdirectedRequestError{}
	if got := err.Error(); got != "Misdirected request" {
		t.Errorf("got %q, want %q", got, "Misdirected request")
	}
}

func TestMisdirectedRequestError_ShouldReturn421FromHttpStatusCode(t *testing.T) {
	t.Parallel()

	err := &MisdirectedRequestError{}
	if got := err.HTTPStatusCode(); got != http.StatusMisdirectedRequest {
		t.Errorf("got %d, want %d", got, http.StatusMisdirectedRequest)
	}
}
