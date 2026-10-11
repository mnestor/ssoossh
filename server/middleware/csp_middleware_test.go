package middleware

// Test methodology: Unit tests for CSP (Content-Security-Policy) middleware.
// Tests run in parallel (t.Parallel()). Verifies nonce injection into script
// tags and CSP header correctness. Uses httptest and string inspection of
// response bodies.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/secheaders"
)

func TestCspMiddleware_ShouldSetContentSecurityPolicyHeader(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	NewCspMiddleware().Add()(c)

	csp := w.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("expected Content-Security-Policy header to be set")
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("expected CSP to contain default-src directive, got: %s", csp)
	}
}

func TestCspMiddleware_ShouldEmbedNonceInScriptSrcDirective(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	NewCspMiddleware().Add()(c)

	nonce := GetCSPNonce(c)
	if nonce == "" {
		t.Fatal("expected a non-empty nonce to be set on the context")
	}

	csp := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "'nonce-"+nonce+"'") {
		t.Errorf("expected CSP to embed nonce %q, got: %s", nonce, csp)
	}
}

func TestCspMiddleware_ShouldGenerateDifferentNoncePerRequest(t *testing.T) {
	t.Parallel()

	w1 := httptest.NewRecorder()
	c1, _ := gin.CreateTestContext(w1)
	c1.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	NewCspMiddleware().Add()(c1)
	nonce1 := GetCSPNonce(c1)

	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	NewCspMiddleware().Add()(c2)
	nonce2 := GetCSPNonce(c2)

	if nonce1 == nonce2 {
		t.Errorf("expected distinct nonces per request, got the same value twice: %q", nonce1)
	}
}

func TestGetCSPNonce_ShouldReturnEmptyStringWhenNotSet(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	if got := GetCSPNonce(c); got != "" {
		t.Errorf("got %q, want empty string when nonce was never set", got)
	}
}

func TestGetCSPNonce_ShouldReturnEmptyStringWhenValueWrongType(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("csp_nonce", 12345) // wrong type: not a string

	if got := GetCSPNonce(c); got != "" {
		t.Errorf("got %q, want empty string when stored value isn't a string", got)
	}
}

// TestCspMiddleware_ShouldSendTheSecheadersPolicyForItsNonce verifies the
// header is exactly the policy secheaders builds for the request's nonce, so
// the diagnostics grade the same string the middleware sends.
func TestCspMiddleware_ShouldSendTheSecheadersPolicyForItsNonce(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	NewCspMiddleware().Add()(c)

	want := secheaders.ContentSecurityPolicy(GetCSPNonce(c))
	if got := w.Header().Get("Content-Security-Policy"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
