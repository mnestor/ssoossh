package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
)

// renderDisabledPage serves the disabled page once with the given
// configuration and returns the status and body.
func renderDisabledPage(t *testing.T, c *config.Config) (int, string) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/auth/disabled", disabledPageHandler(c))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/disabled", nil))
	return w.Code, w.Body.String()
}

// adminOnly is a configuration with only the superseded admin.* contact
// settings, the shape every deployment had before branding.support_email.
func adminOnly(contactEmail, disabledMessage string) *config.Config {
	return &config.Config{Admin: config.AdminConfig{ContactEmail: contactEmail, DisabledMessage: disabledMessage}}
}

// TestDisabledPageHandler covers what the page says for each configuration,
// and that it says it without a session.
func TestDisabledPageHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  *config.Config
		want    []string
		wantNot []string
	}{
		{
			name:   "should name the service and say access is disabled",
			config: adminOnly("", ""),
			want:   []string{"Your access to ssoossh is disabled", "<title>Access disabled · ssoossh</title>"},
		},
		{
			name:   "should offer a way back to sign-in",
			config: adminOnly("", ""),
			want:   []string{`href="/login"`, "Back to sign in"},
		},
		{
			name:   "should say who to ask when no contact is configured",
			config: adminOnly("", ""),
			want:   []string{"Contact your administrator."},
		},
		{
			name:   "should offer the branding support address as a mailto",
			config: &config.Config{Branding: config.BrandingSettings{SupportEmail: "help@corp.example", SupportLabel: "the IT service desk"}},
			want:   []string{`href="mailto:help@corp.example"`, "the IT service desk"},
		},
		{
			name:    "should prefer the branding support address over the superseded one",
			config:  &config.Config{Branding: config.BrandingSettings{SupportEmail: "help@corp.example"}, Admin: config.AdminConfig{ContactEmail: "old@corp.example"}},
			want:    []string{`href="mailto:help@corp.example"`},
			wantNot: []string{"old@corp.example"},
		},
		{
			name:   "should fall back to the superseded admin.contact_email",
			config: adminOnly("it-help@corp.example", ""),
			want:   []string{`href="mailto:it-help@corp.example"`},
		},
		{
			name:   "should show the configured message",
			config: adminOnly("it-help@corp.example", "Open a ticket at go/access"),
			want:   []string{"Open a ticket at go/access"},
		},
		{
			name:   "should show a message configured without an address",
			config: adminOnly("", "Open a ticket at go/access"),
			want:   []string{"Open a ticket at go/access", "Contact your administrator."},
		},
		{
			name:   "should show the organization name beside the brand",
			config: &config.Config{Branding: config.BrandingSettings{OrgName: "Acme Corp"}},
			want:   []string{"Acme Corp"},
		},
		// Nothing about who disabled the account or why: a SOC disable after
		// a compromise must not tip off whoever holds the session.
		{
			name:    "should not say who disabled the account or why",
			config:  adminOnly("", ""),
			wantNot: []string{"administrator disabled", "directory", "SOC"},
		},
		// The operator writes these values, so this is not a defence against
		// an attacker. It is a defence against an apostrophe: an earlier
		// implementation concatenated them into markup, where "your team's
		// admin" or a stray angle bracket produced broken or executable HTML.
		{
			name:    "should escape markup in the configured message",
			config:  adminOnly("", `<script>alert(1)</script>`),
			want:    []string{"&lt;script&gt;"},
			wantNot: []string{"<script>alert(1)</script>"},
		},
		{
			name:    "should escape markup in the configured address",
			config:  adminOnly(`a@b"><script>alert(1)</script>`, ""),
			wantNot: []string{"<script>alert(1)</script>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, body := renderDisabledPage(t, tt.config)
			for _, want := range tt.want {
				if !strings.Contains(body, want) {
					t.Errorf("body does not contain %q:\n%s", want, body)
				}
			}
			for _, notWant := range tt.wantNot {
				if strings.Contains(body, notWant) {
					t.Errorf("body contains %q:\n%s", notWant, body)
				}
			}
		})
	}
}

func TestDisabledPageHandler_ShouldAnswer403WithoutASession(t *testing.T) {
	t.Parallel()

	code, _ := renderDisabledPage(t, adminOnly("", ""))
	if code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", code, http.StatusForbidden)
	}
}

// TestCallbackHandler_DisabledUserIsRedirected covers the hop the browser
// tier does not drive: a callback whose identity resolution reports the user
// disabled must send the browser to /auth/disabled rather than rendering a
// generic failure.
//
// The distinction matters because checkUserDisabled has two failure modes. A
// genuinely disabled account yields UserDisabledError and belongs on that
// page. A database fault yields UserStatusCheckError, which also denies the
// login but must NOT claim the account was disabled -- an outage would
// otherwise tell every user their account had been switched off.
func TestCallbackHandler_DisabledUserIsRedirected(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantStatus   int
		wantLocation string
	}{
		{
			name:         "should send a disabled user to the disabled page",
			err:          &errorresponses.UserDisabledError{},
			wantStatus:   http.StatusFound,
			wantLocation: "/auth/disabled",
		},
		{
			name:       "should not send a status-check failure to the disabled page",
			err:        &errorresponses.UserStatusCheckError{},
			wantStatus: http.StatusServiceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(middleware.NewErrorHandlerMiddleware().Add())

			// Stand in for the callback's post-HandleCallback branch, which
			// is the logic under test; the OIDC exchange itself is covered
			// in server/service.
			err := tt.err
			r.GET("/auth/callback", func(g *gin.Context) {
				userDisabledError := &errorresponses.UserDisabledError{}
				if errors.As(err, &userDisabledError) {
					g.Redirect(http.StatusFound, "/auth/disabled")
					return
				}
				handleError(g, err)
			})

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/callback", nil))

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d, body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantLocation != "" && w.Header().Get("Location") != tt.wantLocation {
				t.Errorf("Location = %q, want %q", w.Header().Get("Location"), tt.wantLocation)
			}
			if tt.wantLocation == "" && w.Header().Get("Location") == "/auth/disabled" {
				t.Error("a status-check failure was sent to the disabled page")
			}
		})
	}
}
