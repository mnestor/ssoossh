package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
)

// disabledPageHandler renders a page telling a disabled user their account
// is disabled, with the configured contact email and message. Requires no
// session (the disabled user has none).
//
// Rendered when HandleCallback returns UserDisabledError, or directly
// when a signed-out visitor accesses /auth/disabled.
func disabledPageHandler(c *config.Config) gin.HandlerFunc {
	return func(g *gin.Context) {
		// Render HTML page with configured contact email and message.
		// Uses a template to avoid hardcoding HTML.
		html := `<!DOCTYPE html>
<html>
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
  <div class="container">
    <h1>Account Disabled</h1>
    <p>Your account has been disabled and you cannot log in at this time.</p>
`
		if c.Admin.ContactEmail != "" {
			html += `    <div class="contact">
      <p><strong>Contact Support:</strong><br>
      <a href="mailto:` + c.Admin.ContactEmail + `" class="mailto">` + c.Admin.ContactEmail + `</a></p>
`
			if c.Admin.DisabledMessage != "" {
				html += `      <p>` + c.Admin.DisabledMessage + `</p>
`
			}
			html += `    </div>
`
		} else if c.Admin.DisabledMessage != "" {
			html += `    <div class="contact">
      <p>` + c.Admin.DisabledMessage + `</p>
    </div>
`
		}
		html += `  </div>
</body>
</html>`

		g.Header("Content-Type", "text/html; charset=utf-8")
		g.String(http.StatusForbidden, html)
	}
}
