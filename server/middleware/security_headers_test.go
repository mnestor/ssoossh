package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/service"
)

// Test methodology: each security header middleware runs once on a gin test
// context, and the header it wrote must equal the value AppSecurityHeaders
// reports for it. This pins the report to the emitted bytes in both
// directions, stricter as well as weaker, which the diagnostics drift test
// cannot do on its own because the grading accepts stricter values.

func TestAppSecurityHeaders_ShouldReportTheValueEachMiddlewareSends(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		add    gin.HandlerFunc
		header string
		// want reads the reported value; c is passed so the CSP row can
		// build the policy for the nonce the middleware generated.
		want func(app service.AppSecurityHeaders, c *gin.Context) string
	}{
		{
			name:   "should report the X-Frame-Options value the middleware sends",
			add:    NewXFrameOptionsMiddleware().Add(),
			header: "X-Frame-Options",
			want:   func(app service.AppSecurityHeaders, _ *gin.Context) string { return app.FrameOptions },
		},
		{
			name:   "should report the Referrer-Policy value the middleware sends",
			add:    NewReferrerPolicyMiddleware().Add(),
			header: "Referrer-Policy",
			want:   func(app service.AppSecurityHeaders, _ *gin.Context) string { return app.ReferrerPolicy },
		},
		{
			name:   "should report the X-Content-Type-Options value the middleware sends",
			add:    NewXContentTypeOptionsMiddleware().Add(),
			header: "X-Content-Type-Options",
			want:   func(app service.AppSecurityHeaders, _ *gin.Context) string { return app.ContentTypeOptions },
		},
		{
			name:   "should report the Content-Security-Policy the middleware sends for its nonce",
			add:    NewCspMiddleware().Add(),
			header: "Content-Security-Policy",
			want: func(app service.AppSecurityHeaders, c *gin.Context) string {
				return app.ContentSecurityPolicy(GetCSPNonce(c))
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

			tt.add(c)

			if got, want := w.Header().Get(tt.header), tt.want(AppSecurityHeaders(), c); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}
