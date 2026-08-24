package controller

import (
	"html/template"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
)

// disabledPage is the page a disabled user lands on. It is parsed once at
// package scope so a malformed template fails the build's tests rather than
// the first request that reaches it.
//
// html/template rather than string concatenation: ContactEmail and
// DisabledMessage are operator-supplied, and pasting them into markup
// unescaped makes an apostrophe in "Contact your team's admin" produce
// broken HTML and a stray angle bracket produce something worse. The
// escaping is contextual, so the address inside href="mailto:..." is escaped
// as a URL while the same value in the link text is escaped as text.
var disabledPage = template.Must(template.New("disabled").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <title>Account Disabled</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; line-height: 1.6; margin: 0; padding: 20px; background: #f5f5f5; }
    .container { max-width: 600px; margin: 0 auto; background: white; padding: 40px; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,0.1); }
    h1 { color: #c83c23; margin-top: 0; }
    p { color: #666; }
    .contact { background: #f9f9f9; padding: 15px; border-radius: 4px; margin: 20px 0; border-left: 4px solid #c83c23; }
    .mailto { color: #0066cc; text-decoration: none; }
    .mailto:hover { text-decoration: underline; }
  </style>
</head>
<body>
  <div class="container" data-testid="account-disabled">
    <h1>Account Disabled</h1>
    <p>Your account has been disabled and you cannot log in at this time.</p>
{{- if or .ContactEmail .DisabledMessage}}
    <div class="contact">
{{- if .ContactEmail}}
      <p><strong>Contact Support:</strong><br>
      <a href="mailto:{{.ContactEmail}}" class="mailto" data-testid="disabled-contact">{{.ContactEmail}}</a></p>
{{- end}}
{{- if .DisabledMessage}}
      <p data-testid="disabled-message">{{.DisabledMessage}}</p>
{{- end}}
    </div>
{{- end}}
  </div>
</body>
</html>`))

// disabledPageHandler renders a page telling a disabled user their account
// is disabled, with the configured contact email and message. Requires no
// session, since the disabled user has none.
//
// Rendered when HandleCallback returns UserDisabledError, or directly when a
// signed-out visitor accesses /auth/disabled. It deliberately says nothing
// about whether the account exists: a visitor who was never a user sees the
// same page, so this cannot be used to enumerate accounts.
//
// The message is shown without a contact address when only the message is
// configured, which is the case for a deployment that routes support through
// a ticketing system rather than an inbox.
func disabledPageHandler(c *config.Config) gin.HandlerFunc {
	return func(g *gin.Context) {
		g.Header("Content-Type", "text/html; charset=utf-8")
		g.Status(http.StatusForbidden)

		// The status and headers are already written, so a failure here can
		// only be logged: there is no longer a response to turn into a 500,
		// and the caller has a partial page either way. In practice Execute
		// fails only on a template/data mismatch, which the package-scope
		// Must and this struct literal rule out.
		if err := disabledPage.Execute(g.Writer, struct {
			ContactEmail    string
			DisabledMessage string
		}{
			ContactEmail:    c.Admin.ContactEmail,
			DisabledMessage: c.Admin.DisabledMessage,
		}); err != nil {
			slog.Error("failed to render the disabled-account page", slog.Any("error", err))
		}
	}
}
