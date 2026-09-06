package middleware

// Test methodology: Unit tests for error handling middleware. Tests run in
// parallel (t.Parallel()). Verifies error conversion to appropriate HTTP
// status codes and response bodies. Uses table-driven tests for multiple
// error types. Uses httptest and JSON response inspection.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

func TestErrorHandlerMiddleware_ShouldDoNothingWhenNoErrorRegistered(t *testing.T) {
	t.Parallel()

	handler := NewErrorHandlerMiddleware().Add()

	c, w := newTestRequest("203.0.113.30:1111")
	handler(c)

	if w.Code != http.StatusOK {
		// gin.CreateTestContext defaults to 200 unless changed
		t.Errorf("got status %d, want default 200 (no response written)", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("expected no body to be written, got %q", w.Body.String())
	}
}

func TestErrorHandlerMiddleware_ShouldUseErrorsHttpStatusCodeWhenAvailable(t *testing.T) {
	t.Parallel()

	handler := NewErrorHandlerMiddleware().Add()

	c, w := newTestRequest("203.0.113.31:1111")
	c.Error(&errorresponses.TooManyRequestsError{})
	c.Abort()
	handler(c)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("got status %d, want %d", w.Code, http.StatusTooManyRequests)
	}
	if !strings.Contains(w.Body.String(), "Too many requests") {
		t.Errorf("expected body to contain the error message, got %q", w.Body.String())
	}
}

func TestErrorHandlerMiddleware_ShouldDefaultTo500ForPlainErrors(t *testing.T) {
	t.Parallel()

	handler := NewErrorHandlerMiddleware().Add()

	c, w := newTestRequest("203.0.113.32:1111")
	c.Error(errors.New("boom"))
	c.Abort()
	handler(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(w.Body.String(), apitypes.ErrorCodeInternalError) {
		t.Errorf("expected body to carry the internal_error code, got %q", w.Body.String())
	}
}

func TestErrorHandlerMiddleware_ShouldNotReflectInternalErrorTextToTheCaller(t *testing.T) {
	t.Parallel()

	handler := NewErrorHandlerMiddleware().Add()

	c, w := newTestRequest("203.0.113.33:1111")
	// A bare error carries no status, so it takes the 500 default. Its text
	// could name schema, a file path, or a fragment of the caller's own
	// input echoed by a parser, so it must not be reflected back.
	c.Error(errors.New("pq: relation \"users\" does not exist"))
	c.Abort()
	handler(c)

	if strings.Contains(w.Body.String(), "users") {
		t.Errorf("internal error text leaked to the caller: %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "internal server error") {
		t.Errorf("expected the generic message, got %q", w.Body.String())
	}
}

func TestErrorHandlerMiddleware_ShouldReflectMessageForErrorsThatDeclareAStatus(t *testing.T) {
	t.Parallel()

	handler := NewErrorHandlerMiddleware().Add()

	c, w := newTestRequest("203.0.113.34:1111")
	// A typed error that declares its own status is caller-safe by design:
	// its Reason is written to be returned, so it is reflected.
	c.Error(&errorresponses.InvalidRequestError{Reason: "the widget field must be a positive integer"})
	c.Abort()
	handler(c)

	if w.Code != http.StatusBadRequest {
		t.Errorf("got status %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "the widget field must be a positive integer") {
		t.Errorf("expected the typed error's reason to be reflected, got %q", w.Body.String())
	}
}

func TestErrorHandlerMiddleware_ShouldNotOverwriteAResponseAlreadyWritten(t *testing.T) {
	t.Parallel()

	handler := NewErrorHandlerMiddleware().Add()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.JSON(http.StatusCreated, gin.H{"ok": true})
	// A handler could register an error after already writing a response
	// (unusual, but possible); the written response must win.
	c.Error(&errorresponses.TooManyRequestsError{})

	handler(c)

	if w.Code != http.StatusCreated {
		t.Errorf("got status %d, want the already-written %d", w.Code, http.StatusCreated)
	}
}

// statusToErrorCode is the fallback for errors that do not implement
// errorCoder -- the bare errors older handlers still pass through. It is the
// only thing standing between such an error and a response with no
// machine-readable code, so every status the API actually returns needs to
// map to something a client can branch on, and anything unrecognised has to
// fall through to the internal-error code rather than to "".
func TestStatusToErrorCode_ShouldMapEveryStatusTheAPIReturns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		want   string
	}{
		{name: "bad request", status: http.StatusBadRequest, want: apitypes.ErrorCodeInvalidRequest},
		{name: "unauthorized", status: http.StatusUnauthorized, want: apitypes.ErrorCodeUnauthenticated},
		{name: "forbidden", status: http.StatusForbidden, want: apitypes.ErrorCodeForbidden},
		{name: "not found", status: http.StatusNotFound, want: apitypes.ErrorCodeNotFound},
		{name: "gone", status: http.StatusGone, want: apitypes.ErrorCodeUnavailable},
		{name: "too many requests", status: http.StatusTooManyRequests, want: apitypes.ErrorCodeRateLimited},
		{name: "not implemented", status: http.StatusNotImplemented, want: apitypes.ErrorCodeNotImplemented},
		// The default arm. A status with no specific mapping must still
		// produce a code, not an empty string.
		{name: "internal server error", status: http.StatusInternalServerError, want: apitypes.ErrorCodeInternalError},
		{name: "unrecognised status", status: http.StatusTeapot, want: apitypes.ErrorCodeInternalError},
		{name: "service unavailable", status: http.StatusServiceUnavailable, want: apitypes.ErrorCodeInternalError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := statusToErrorCode(tt.status)
			if got != tt.want {
				t.Errorf("statusToErrorCode(%d) = %q, want %q", tt.status, got, tt.want)
			}
			if got == "" {
				t.Errorf("statusToErrorCode(%d) returned an empty code", tt.status)
			}
		})
	}
}
