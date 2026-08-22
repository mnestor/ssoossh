package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestXFrameOptionsMiddleware_ShouldSetHeaderToDeny verifies X-Frame-Options
// header is set to DENY on every response.
func TestXFrameOptionsMiddleware_ShouldSetHeaderToDeny(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	NewXFrameOptionsMiddleware().Add()(c)

	if got := w.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("got %q, want %q", got, "DENY")
	}
}
