package middleware

// Test methodology: Unit tests for HSTS (HTTP Strict-Transport-Security)
// middleware. Tests run in parallel (t.Parallel()). Verifies HSTS header
// is set correctly or omitted based on configuration. Uses httptest and
// header inspection.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHstsMiddleware_ShouldSetHeaderWhenValueConfigured(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	NewHstsMiddleware("max-age=31536000; includeSubDomains").Add()(c)

	if got := w.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Errorf("got %q, want %q", got, "max-age=31536000; includeSubDomains")
	}
}

func TestHstsMiddleware_ShouldNotSetHeaderWhenValueEmpty(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	NewHstsMiddleware("").Add()(c)

	if _, present := w.Header()["Strict-Transport-Security"]; present {
		t.Errorf("expected no Strict-Transport-Security header, got %q", w.Header().Get("Strict-Transport-Security"))
	}
}
