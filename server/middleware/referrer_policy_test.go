package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestReferrerPolicyMiddleware_ShouldSetHeaderToStrictOriginWhenCrossOrigin
// verifies Referrer-Policy header is set to strict-origin-when-cross-origin
// on every response.
func TestReferrerPolicyMiddleware_ShouldSetHeaderToStrictOriginWhenCrossOrigin(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	NewReferrerPolicyMiddleware().Add()(c)

	if got := w.Header().Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("got %q, want %q", got, "strict-origin-when-cross-origin")
	}
}
