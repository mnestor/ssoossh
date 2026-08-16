package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// Test methodology: table-driven over the header combinations a browser (or
// a non-browser client) can present, asserting only pass/reject. The
// middleware's whole job is that decision, so anything else would be
// testing gin.

// newCsrfRouter builds a router with the middleware in front of a POST that
// records whether it ran.
func newCsrfRouter(t *testing.T, allowedOrigin string) (*gin.Engine, *bool) {
	t.Helper()

	reached := false
	r := gin.New()
	r.Use(NewCsrfMiddleware(allowedOrigin).Add())
	r.POST("/approve", func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})
	r.GET("/approve", func(c *gin.Context) {
		reached = true
		c.Status(http.StatusOK)
	})
	return r, &reached
}

func TestCsrfMiddleware_ShouldDecideByFetchMetadataAndOrigin(t *testing.T) {
	t.Parallel()

	const allowed = "https://ssh.example.com"

	tests := []struct {
		name       string
		method     string
		secFetch   string
		origin     string
		wantStatus int
	}{
		{
			name:       "should allow a same-origin request",
			method:     http.MethodPost,
			secFetch:   "same-origin",
			wantStatus: http.StatusOK,
		},
		{
			name:       "should allow a direct navigation with no initiator",
			method:     http.MethodPost,
			secFetch:   "none",
			wantStatus: http.StatusOK,
		},
		{
			name:       "should reject a cross-site request",
			method:     http.MethodPost,
			secFetch:   "cross-site",
			wantStatus: http.StatusForbidden,
		},
		{
			// The case SameSite=Strict does not cover: a sibling origin
			// under the same registrable domain.
			name:       "should reject a same-site request from another origin",
			method:     http.MethodPost,
			secFetch:   "same-site",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "should allow a matching Origin when fetch metadata is absent",
			method:     http.MethodPost,
			origin:     allowed,
			wantStatus: http.StatusOK,
		},
		{
			name:       "should reject a mismatched Origin when fetch metadata is absent",
			method:     http.MethodPost,
			origin:     "https://evil.example.com",
			wantStatus: http.StatusForbidden,
		},
		{
			// A request no browser sent carries no ambient cookie authority,
			// so it is not a CSRF vector — the CLI and curl land here.
			name:       "should allow a request carrying neither header",
			method:     http.MethodPost,
			wantStatus: http.StatusOK,
		},
		{
			name:       "should allow a safe method regardless of origin",
			method:     http.MethodGet,
			secFetch:   "cross-site",
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, reached := newCsrfRouter(t, allowed)

			req := httptest.NewRequest(tt.method, "/approve", nil)
			if tt.secFetch != "" {
				req.Header.Set("Sec-Fetch-Site", tt.secFetch)
			}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", w.Code, tt.wantStatus)
			}
			if got, want := *reached, tt.wantStatus == http.StatusOK; got != want {
				t.Errorf("handler reached = %v, want %v", got, want)
			}
		})
	}
}

// TestCsrfMiddleware_ShouldFallBackToFetchMetadataWithoutAConfiguredOrigin
// covers the deployment that has no server_name set: Origin cannot be
// matched against anything, so the decision has to rest on fetch metadata
// alone rather than failing open for everything.
func TestCsrfMiddleware_ShouldFallBackToFetchMetadataWithoutAConfiguredOrigin(t *testing.T) {
	t.Parallel()

	r, _ := newCsrfRouter(t, "")

	req := httptest.NewRequest(http.MethodPost, "/approve", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Origin", "https://evil.example.com")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestCsrfMiddleware_ShouldAllowAnyOriginWithoutAConfiguredOrigin covers
// originAllowed's own empty-allowedOrigin branch specifically: unlike the
// test above (which never reaches originAllowed at all, since Sec-Fetch-Site
// short-circuits first), this omits Sec-Fetch-Site so the Origin check
// actually runs, with nothing configured to compare it against.
func TestCsrfMiddleware_ShouldAllowAnyOriginWithoutAConfiguredOrigin(t *testing.T) {
	t.Parallel()

	r, reached := newCsrfRouter(t, "")

	req := httptest.NewRequest(http.MethodPost, "/approve", nil)
	req.Header.Set("Origin", "https://anything.example.com")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if !*reached {
		t.Error("expected the handler to run: with no configured origin, originAllowed has nothing to judge and defers")
	}
}
