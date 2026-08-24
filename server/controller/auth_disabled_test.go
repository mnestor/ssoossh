package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
)

// renderDisabledPage serves the disabled page once with the given admin
// settings and returns the body.
func renderDisabledPage(t *testing.T, contactEmail, disabledMessage string) (int, string) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	c := &config.Config{Admin: config.AdminConfig{
		ContactEmail:    contactEmail,
		DisabledMessage: disabledMessage,
	}}

	r := gin.New()
	r.GET("/auth/disabled", disabledPageHandler(c))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/disabled", nil))
	return w.Code, w.Body.String()
}

// TestDisabledPageHandler covers what the page says for each configuration,
// and that it says it without a session.
func TestDisabledPageHandler(t *testing.T) {
	t.Run("should answer 403 without a session", func(t *testing.T) {
		code, _ := renderDisabledPage(t, "", "")
		if code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", code, http.StatusForbidden)
		}
	})

	t.Run("should state the account is disabled", func(t *testing.T) {
		_, body := renderDisabledPage(t, "", "")
		if !strings.Contains(body, "Account Disabled") {
			t.Errorf("body does not state the account is disabled:\n%s", body)
		}
	})

	t.Run("should offer the configured address as a mailto", func(t *testing.T) {
		_, body := renderDisabledPage(t, "it-help@corp.example", "")
		if !strings.Contains(body, `href="mailto:it-help@corp.example"`) {
			t.Errorf("body carries no mailto for the configured address:\n%s", body)
		}
	})

	t.Run("should show the configured message", func(t *testing.T) {
		_, body := renderDisabledPage(t, "it-help@corp.example", "Open a ticket at go/access")
		if !strings.Contains(body, "Open a ticket at go/access") {
			t.Errorf("body does not carry the configured message:\n%s", body)
		}
	})

	t.Run("should show a message configured without an address", func(t *testing.T) {
		_, body := renderDisabledPage(t, "", "Open a ticket at go/access")
		if !strings.Contains(body, "Open a ticket at go/access") {
			t.Errorf("a message configured alone is not shown:\n%s", body)
		}
	})

	t.Run("should mention no contact when neither is configured", func(t *testing.T) {
		_, body := renderDisabledPage(t, "", "")
		if strings.Contains(body, "Contact Support") {
			t.Errorf("body offers a contact that was never configured:\n%s", body)
		}
	})

	// The operator writes these values, so this is not a defence against an
	// attacker. It is a defence against an apostrophe: the previous
	// implementation concatenated them into markup, where "your team's admin"
	// or a stray angle bracket produced broken or executable HTML.
	t.Run("should escape markup in the configured message", func(t *testing.T) {
		_, body := renderDisabledPage(t, "", `<script>alert(1)</script>`)
		if strings.Contains(body, "<script>alert(1)</script>") {
			t.Errorf("the configured message was interpolated as live markup:\n%s", body)
		}
		if !strings.Contains(body, "&lt;script&gt;") {
			t.Errorf("the configured message was not escaped:\n%s", body)
		}
	})

	t.Run("should escape markup in the configured address", func(t *testing.T) {
		_, body := renderDisabledPage(t, `a@b"><script>alert(1)</script>`, "")
		if strings.Contains(body, "<script>alert(1)</script>") {
			t.Errorf("the configured address escaped its attribute:\n%s", body)
		}
	})
}
