package middleware

// Test methodology: Unit tests for HTTP middleware using httptest to verify
// header manipulation and response behavior without a real listener. Tests
// run in parallel (t.Parallel()). Uses TestMain to configure gin test mode
// once per package. Each test verifies one specific middleware effect.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestMain puts gin into test mode once for the whole package, avoiding
// per-test log spam and matching gin's own recommended test setup. This
// replaces an init() function, which CONTRIBUTING.md disallows.
func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

func TestCacheControlMiddleware_ShouldSetHeaderWhenAbsent(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	NewCacheControlMiddleware().Add()(c)

	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Errorf("got %q, want %q", got, "private, no-store")
	}
}

func TestCacheControlMiddleware_ShouldNotOverrideExistingHeader(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Header("Cache-Control", "public, max-age=3600")

	NewCacheControlMiddleware().Add()(c)

	if got := w.Header().Get("Cache-Control"); got != "public, max-age=3600" {
		t.Errorf("got %q, want existing header to be preserved: %q", got, "public, max-age=3600")
	}
}
