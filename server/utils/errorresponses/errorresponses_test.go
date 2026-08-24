package errorresponses

// Test methodology: Unit tests for custom error types and response behavior.
// Tests run in parallel (t.Parallel()). Uses table-driven tests to verify
// error messages and HTTP status codes. Each test verifies one error type
// or behavior.

import (
	"net/http"
	"testing"

	"github.com/mnestor/ssoossh/internal/apitypes"
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

// Every ErrorCode method sat at 0% coverage. They are what the API returns
// as the machine-readable half of an error, which clients branch on, so a
// wrong or duplicated code is a wire-contract break that reads as a
// cosmetic string change in review.
//
// Asserted together in one table so the codes can be seen side by side:
// two errors sharing a code is only visible when they are read next to each
// other, and Misdirected/Forbidden deliberately do share one.
func TestErrorCode_ShouldReportTheWireCodeForEachError(t *testing.T) {
	tests := []struct {
		name string
		err  interface {
			error
			ErrorCode() string
			HTTPStatusCode() int
		}
		wantCode   string
		wantStatus int
	}{
		{name: "too many requests", err: &TooManyRequestsError{}, wantCode: apitypes.ErrorCodeRateLimited, wantStatus: http.StatusTooManyRequests},
		{name: "misdirected request", err: &MisdirectedRequestError{}, wantCode: apitypes.ErrorCodeForbidden, wantStatus: http.StatusMisdirectedRequest},
		{name: "not found", err: &NotFoundError{Resource: "certificate request \"abc\""}, wantCode: apitypes.ErrorCodeNotFound, wantStatus: http.StatusNotFound},
		{name: "certificate unavailable", err: &CertificateUnavailableError{}, wantCode: apitypes.ErrorCodeUnavailable, wantStatus: http.StatusGone},
		{name: "not implemented", err: &NotImplementedError{}, wantCode: apitypes.ErrorCodeNotImplemented, wantStatus: http.StatusNotImplemented},
		{name: "forbidden", err: &ForbiddenError{}, wantCode: apitypes.ErrorCodeForbidden, wantStatus: http.StatusForbidden},
		{name: "invalid request", err: &InvalidRequestError{Reason: "unknown notification kind \"nope\""}, wantCode: apitypes.ErrorCodeInvalidRequest, wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.ErrorCode(); got != tt.wantCode {
				t.Errorf("ErrorCode() = %q, want %q", got, tt.wantCode)
			}
			if got := tt.err.HTTPStatusCode(); got != tt.wantStatus {
				t.Errorf("HTTPStatusCode() = %d, want %d", got, tt.wantStatus)
			}
			// A code the client cannot branch on is no better than none.
			if tt.err.ErrorCode() == "" {
				t.Error("ErrorCode() is empty")
			}
		})
	}
}

// InvalidRequestError's message is returned to the caller, so an empty
// Reason has to still say something rather than rendering as "".
func TestInvalidRequestError_ShouldDescribeItself(t *testing.T) {
	tests := []struct {
		name string
		err  InvalidRequestError
		want string
	}{
		{name: "with a reason", err: InvalidRequestError{Reason: "kinds must be an object"}, want: "kinds must be an object"},
		{name: "without a reason", err: InvalidRequestError{}, want: "invalid request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}
