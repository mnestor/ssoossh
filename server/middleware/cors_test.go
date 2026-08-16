package middleware

// Test methodology: Unit tests for CORS middleware. Tests run in parallel
// (t.Parallel()). Uses table-driven tests to verify path matching and header
// setting behavior. Uses httptest for request/response capture.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIsCorsPath_ShouldReturnTrueForMatchedPaths(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/api/oidc/token",
		"/api/oidc/userinfo",
		"/oidc/end-session",
		"/api/oidc/introspect",
		"/.well-known/jwks.json",
		"/.well-known/openid-configuration",
	}

	for _, p := range paths {
		p := p
		t.Run(p, func(t *testing.T) {
			t.Parallel()
			if !isCorsPath(p) {
				t.Errorf("expected %q to be a CORS path", p)
			}
		})
	}
}

func TestIsCorsPath_ShouldReturnFalseForUnmatchedPath(t *testing.T) {
	t.Parallel()

	if isCorsPath("/api/ca") {
		t.Error("expected /api/ca to not be a CORS path")
	}
}

func TestIsCorsPath_ShouldReturnFalseForEmptyPath(t *testing.T) {
	t.Parallel()

	if isCorsPath("") {
		t.Error("expected empty path to not be a CORS path")
	}
}

func TestCorsMiddleware_ShouldSetHeadersForMatchedPath(t *testing.T) {
	t.Parallel()

	// Route the request through a real engine (rather than invoking the
	// handler directly) so that c.FullPath() is populated, exercising the
	// primary (non-fallback) branch of path resolution.
	r := gin.New()
	r.Use(NewCorsMiddleware().Add())
	r.GET("/api/oidc/token", func(c *gin.Context) {})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/oidc/token", nil)
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("got Access-Control-Allow-Origin %q, want %q", got, "*")
	}
}

func TestCorsMiddleware_ShouldNotSetHeadersForUnmatchedPath(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/ca", nil)

	NewCorsMiddleware().Add()(c)

	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("expected no CORS header for unmatched path, got %q", got)
	}
}

func TestCorsMiddleware_ShouldShortCircuitPreflightOptionsRequest(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodOptions, "/api/oidc/token", nil)
	r.Handle(http.MethodOptions, "/api/oidc/token", func(c *gin.Context) {})

	NewCorsMiddleware().Add()(c)

	if w.Code != http.StatusNoContent {
		t.Errorf("got status %d, want %d", w.Code, http.StatusNoContent)
	}
}

func TestCorsMiddleware_ShouldFallBackToRawURLPathWhenFullPathEmpty(t *testing.T) {
	t.Parallel()

	// When the request isn't matched to a registered route, c.FullPath()
	// returns "", so the middleware must fall back to the raw URL path
	// (this matters for preflight OPTIONS requests the router never sees).
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodOptions, "/.well-known/jwks.json", nil)

	NewCorsMiddleware().Add()(c)

	if w.Code != http.StatusNoContent {
		t.Errorf("got status %d, want %d", w.Code, http.StatusNoContent)
	}
}
