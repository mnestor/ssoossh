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

func TestNotFoundError_ShouldIncludeResourceInMessage(t *testing.T) {
	t.Parallel()

	err := &NotFoundError{Resource: `certificate request "abc"`}
	want := `certificate request "abc" not found`
	if got := err.Error(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNotFoundError_ShouldReturnGenericMessageWhenResourceEmpty(t *testing.T) {
	t.Parallel()

	err := &NotFoundError{}
	if got := err.Error(); got != "not found" {
		t.Errorf("got %q, want %q", got, "not found")
	}
}

func TestNotFoundError_ShouldReturn404FromHttpStatusCode(t *testing.T) {
	t.Parallel()

	err := &NotFoundError{}
	if got := err.HTTPStatusCode(); got != http.StatusNotFound {
		t.Errorf("got %d, want %d", got, http.StatusNotFound)
	}
}
