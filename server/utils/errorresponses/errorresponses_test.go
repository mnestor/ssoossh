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

func TestCertificateUnavailableError_ShouldIncludeRequestIDInMessage(t *testing.T) {
	t.Parallel()

	err := &CertificateUnavailableError{RequestID: "req-123"}
	want := `certificate for request "req-123" is no longer available; please make a new request`
	if got := err.Error(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestCertificateUnavailableError_ShouldReturn410FromHttpStatusCode(t *testing.T) {
	t.Parallel()

	err := &CertificateUnavailableError{}
	if got := err.HTTPStatusCode(); got != http.StatusGone {
		t.Errorf("got %d, want %d", got, http.StatusGone)
	}
}

func TestNotImplementedError_ShouldReturnMessageFromError(t *testing.T) {
	t.Parallel()

	err := &NotImplementedError{}
	if got := err.Error(); got != "Not implemented" {
		t.Errorf("got %q, want %q", got, "Not implemented")
	}
}

func TestNotImplementedError_ShouldReturn501FromHttpStatusCode(t *testing.T) {
	t.Parallel()

	err := &NotImplementedError{}
	if got := err.HTTPStatusCode(); got != http.StatusNotImplemented {
		t.Errorf("got %d, want %d", got, http.StatusNotImplemented)
	}
}

func TestForbiddenError_ShouldReturnReasonWhenSet(t *testing.T) {
	t.Parallel()

	err := &ForbiddenError{Reason: "not the owner of this resource"}
	if got := err.Error(); got != "not the owner of this resource" {
		t.Errorf("got %q, want %q", got, "not the owner of this resource")
	}
}

func TestForbiddenError_ShouldReturnGenericMessageWhenReasonEmpty(t *testing.T) {
	t.Parallel()

	err := &ForbiddenError{}
	if got := err.Error(); got != "forbidden" {
		t.Errorf("got %q, want %q", got, "forbidden")
	}
}

func TestForbiddenError_ShouldReturn403FromHttpStatusCode(t *testing.T) {
	t.Parallel()

	err := &ForbiddenError{}
	if got := err.HTTPStatusCode(); got != http.StatusForbidden {
		t.Errorf("got %d, want %d", got, http.StatusForbidden)
	}
}
