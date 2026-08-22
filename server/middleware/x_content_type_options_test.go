package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestXContentTypeOptionsMiddleware_ShouldSetHeaderToNosniff verifies
// X-Content-Type-Options header is set to nosniff on every response.
func TestXContentTypeOptionsMiddleware_ShouldSetHeaderToNosniff(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	NewXContentTypeOptionsMiddleware().Add()(c)

	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("got %q, want %q", got, "nosniff")
	}
}
