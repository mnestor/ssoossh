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
// html/template rather than string concatenation: the contact and
// DisabledMessage are operator-supplied, and pasting them into markup
// unescaped makes an apostrophe in "Contact your team's admin" produce
// broken HTML and a stray angle bracket produce something worse. The
// escaping is contextual, so the address inside href="mailto:..." is escaped
// as a URL while the same value in the link text is escaped as text.
//
// It is served by this server rather than the web app, so it carries the
// app's own colour tokens (frontend/DESIGN.md) for both themes inline, and
// the default brand mark, rather than looking like a different site at the
// moment someone is least sure where they are.
//
// It names the service and says nothing about who disabled the account or
// why: a SOC disable after a compromise must not tell whoever holds the
// identity provider session that they were noticed, and a directory
// disable can say something HR has not. An operator who wants to add
// guidance has DisabledMessage.
var disabledPage = template.Must(template.New("disabled").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Access disabled · ssoossh</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <style>
    :root {
      --ground: oklch(95.5% 0.006 75); --surface: #ffffff; --surface-muted: oklch(97.2% 0.005 75);
      --ink: oklch(22% 0.01 60); --ink-muted: oklch(46% 0.012 65); --accent: oklch(50% 0.19 264);
      --accent-hover: oklch(43% 0.19 264); --danger: oklch(50% 0.18 27);
      color-scheme: light;
    }
    @media (prefers-color-scheme: dark) {
      :root {
        --ground: oklch(14.5% 0.006 70); --surface: oklch(22.5% 0.008 70); --surface-muted: oklch(26.5% 0.008 70);
        --ink: oklch(93% 0.006 75); --ink-muted: oklch(71% 0.01 70); --accent: oklch(74% 0.13 264);
        --accent-hover: oklch(80% 0.11 264); --danger: oklch(74% 0.15 27);
        color-scheme: dark;
      }
    }
    body { margin: 0; padding: 48px 16px; background: var(--ground); color: var(--ink);
      font-family: "IBM Plex Sans", system-ui, -apple-system, "Segoe UI", sans-serif; font-size: 15px; line-height: 1.55; }
    .container { max-width: 520px; margin: 0 auto; background: var(--surface); padding: 28px; border-radius: 6px; }
    .brand { display: flex; align-items: center; gap: 8px; font-weight: 600; margin-bottom: 24px; }
    .brand svg { color: var(--accent); }
    .brand .org { font-weight: 400; font-size: 12px; color: var(--ink-muted); border-left: 1px solid var(--surface-muted); padding-left: 8px; }
    h1 { font-family: "IBM Plex Mono", ui-monospace, monospace; font-size: 20px; line-height: 1.25; margin: 0 0 8px; }
    p { margin: 0 0 12px; color: var(--ink-muted); }
    .contact { background: var(--surface-muted); color: var(--ink); padding: 12px 14px; border-radius: 4px; margin: 16px 0; }
    .contact p { color: var(--ink); margin: 0; }
    .contact p + p { margin-top: 8px; color: var(--ink-muted); }
    a { color: var(--accent); }
    a:hover { color: var(--accent-hover); }
    a:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
    .back { display: inline-flex; align-items: center; min-height: 36px; font-weight: 500; font-size: 14px; }
  </style>
</head>
<body>
  <main class="container" data-testid="account-disabled">
    <div class="brand">
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <path d="M3.6 19 A6 6 0 0 1 4.2 9.6 A4.4 4.4 0 0 1 11.5 6 A4.1 4.1 0 0 1 17.8 8.8 A6.6 6.6 0 0 1 20.8 19 Z"/>
        <polyline points="7 11.2 9.5 13.1 7 15"/><circle cx="13.2" cy="15" r="1.5"/>
        <line x1="14.7" y1="15" x2="19.2" y2="15"/><line x1="17.6" y1="15" x2="17.6" y2="12.8"/>
      </svg>
      <span>ssoossh</span>
{{- if .OrgName}}
      <span class="org">{{.OrgName}}</span>
{{- end}}
    </div>
    <h1>Your access to ssoossh is disabled</h1>
    <p>You can't sign in to ssoossh until an administrator restores your access.</p>
    <div class="contact">
{{- if .ContactEmail}}
      <p>Contact <a href="mailto:{{.ContactEmail}}" data-testid="disabled-contact">{{if .ContactLabel}}{{.ContactLabel}}{{else}}{{.ContactEmail}}{{end}}</a>.</p>
{{- else}}
      <p data-testid="disabled-contact-default">Contact your administrator.</p>
{{- end}}
{{- if .DisabledMessage}}
      <p data-testid="disabled-message">{{.DisabledMessage}}</p>
{{- end}}
    </div>
    <a class="back" href="/login" data-testid="disabled-back">Back to sign in</a>
  </main>
</body>
</html>`))

// disabledPageHandler renders a page telling a disabled user their access
// is disabled, with the deployment's support contact and message. Requires
// no session, since the disabled user has none.
//
// Rendered when HandleCallback returns UserDisabledError, or directly when a
// signed-out visitor accesses /auth/disabled. It deliberately says nothing
// about whether the account exists: a visitor who was never a user sees the
// same page, so this cannot be used to enumerate accounts.
//
// The contact is config.Config.SupportContact: branding.support_email, the
// same address the login page and footer show, falling back to the
// superseded admin.contact_email. With neither set the page still says who
// to ask, because a locked-out person has no other page to read it on.
func disabledPageHandler(c *config.Config) gin.HandlerFunc {
	if c.Admin.ContactEmail != "" && c.Branding.SupportEmail == "" {
		slog.Warn("admin.contact_email is superseded by branding.support_email; set branding.support_email so the login page, the footer and the disabled page all name the same contact",
			"admin.contact_email", c.Admin.ContactEmail)
	}
	contact, label := c.SupportContact()

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
			ContactLabel    string
			DisabledMessage string
			OrgName         string
		}{
			ContactEmail:    contact,
			ContactLabel:    label,
			DisabledMessage: c.Admin.DisabledMessage,
			OrgName:         c.Branding.OrgName,
		}); err != nil {
			slog.Error("failed to render the disabled-account page", slog.Any("error", err))
		}
	}
}
