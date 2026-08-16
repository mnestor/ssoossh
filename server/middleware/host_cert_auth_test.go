package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// should fail closed with 501, since the host-certificate transport is not yet implemented
func TestHostCertAuthMiddleware_ShouldFailClosed(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(NewErrorHandlerMiddleware().Add())
	var reached bool
	r.GET("/renew", NewHostCertAuthMiddleware().Add(), func(c *gin.Context) {
		reached = true
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/renew", nil))

	if w.Code != http.StatusNotImplemented {
		t.Errorf("got status %d, want %d", w.Code, http.StatusNotImplemented)
	}
	if reached {
		t.Error("expected the downstream handler to never run")
	}
}
