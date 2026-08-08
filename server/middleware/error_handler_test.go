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
	c.Error(&TooManyRequestsError{}) //nolint:errcheck
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
	c.Error(errors.New("boom")) //nolint:errcheck
	c.Abort()
	handler(c)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("got status %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if !strings.Contains(w.Body.String(), "boom") {
		t.Errorf("expected body to contain the error message, got %q", w.Body.String())
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
	c.Error(&TooManyRequestsError{}) //nolint:errcheck

	handler(c)

	if w.Code != http.StatusCreated {
		t.Errorf("got status %d, want the already-written %d", w.Code, http.StatusCreated)
	}
}
