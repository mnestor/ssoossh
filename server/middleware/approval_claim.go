package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/service"
)

// ApprovalClaimMiddleware binds the /approve/<id> approval page to the
// first browser that fetches it, per
// docs/proposals/gui-client-approval-flow.md section 6. The approval URL is
// a bearer capability, so a copy recovered later — shell history, a log, a
// screen recording, chat scrollback — would otherwise still open the page.
// After the first GET it no longer does.
//
// Claiming on a GET makes the GET state-changing, contrary to the usual
// expectation that GET is safe. This is intentional — do not "fix" it: when
// the link travels through anything that fetches URLs (Slack/Teams
// unfurling, Outlook Safe Links, a scanning proxy), the scanner burns the
// request before a phishing victim can ever click it, and the attempt fails
// closed. The legitimate paths have no scanner in the middle — the client
// launches the browser directly, or the user pastes the URL from their own
// terminal.
//
// Runs as engine-wide middleware rather than a route because /approve/<id>
// is an SPA route served by the frontend's NoRoute catch-all
// (server/frontend); everything not a document GET of that exact path
// passes straight through. Note the Vite dev server serves SPA documents
// itself, so in `make dev` this only runs against the embedded build.
type ApprovalClaimMiddleware struct {
	claimer service.ApprovalPageClaimer

	// secure marks the claim cookie Secure. Same resolution as the session
	// cookie's flag (see bootstrap.sessionCookieOptions): a Secure cookie
	// on plain HTTP is silently dropped, which would lock every deployment
	// without TLS into the cookie-blocked path.
	secure bool
}

// NewApprovalClaimMiddleware creates an ApprovalClaimMiddleware backed by
// claimer, marking the claim cookie Secure when secure is set.
func NewApprovalClaimMiddleware(claimer service.ApprovalPageClaimer, secure bool) *ApprovalClaimMiddleware {
	return &ApprovalClaimMiddleware{claimer: claimer, secure: secure}
}

// claimCookieName names the claim cookie. One cookie name serves every
// request because each is path-scoped to its own /approve/<id>, so two
// requests' cookies never collide or travel together.
const claimCookieName = "ssoossh_approval_claim"

// claimCookieMaxAge is the claim cookie's lifetime in seconds. It only has
// to outlive one approval flow — the pending window (cert_options
// ApprovalTTL, minutes) plus however long someone leaves the outcome page
// open — so a day is generous without being an unbounded credential.
const claimCookieMaxAge = int(24 * 60 * 60)

// approvalPathPrefix is the SPA route this middleware guards, matching
// controller.approvalURL.
const approvalPathPrefix = "/approve/"

// approvalRequestID extracts <id> from an exactly-one-segment
// /approve/<id> path. Anything else — the bare prefix, deeper paths, other
// routes — is not an approval page fetch.
func approvalRequestID(path string) (string, bool) {
	id, ok := strings.CutPrefix(path, approvalPathPrefix)
	if !ok || id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

// Add returns a gin.HandlerFunc that claims or checks the approval page
// binding on document GETs of /approve/<id>, and redirects spent or
// cookie-blocked visits to the SPA's explanation page.
func (m *ApprovalClaimMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		// HEAD included: link scanners often probe with HEAD first, and a
		// probe should burn the link exactly like a GET (see the type
		// comment — failing closed on scanned links is the point).
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Next()
			return
		}
		id, ok := approvalRequestID(c.Request.URL.Path)
		if !ok {
			c.Next()
			return
		}

		presented, err := c.Cookie(claimCookieName)
		if err != nil {
			presented = ""
		}

		res, err := m.claimer.ClaimApprovalPage(c.Request.Context(), id, presented, c.Request.UserAgent())
		if err != nil {
			// Fail closed: serving the page unclaimed would silently skip a
			// security control. The whole page depends on the same database
			// anyway.
			_ = c.Error(err) //nolint:errcheck // c.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
			c.Abort()
			return
		}

		switch res.Outcome {
		case service.ClaimPageClaimed:
			// Path-scoped to this one request, so the cookie only ever
			// travels back to the page it claims. SameSite=Lax specifically:
			// the top-level redirect back from the IdP must still present
			// it, and Strict breaks that return leg.
			http.SetCookie(c.Writer, &http.Cookie{ //nolint:gosec // G124 wants a literal Secure: true; ours is deliberately conditional — see the secure field's comment.
				Name:     claimCookieName,
				Value:    res.Token,
				Path:     approvalPathPrefix + id,
				MaxAge:   claimCookieMaxAge,
				HttpOnly: true,
				Secure:   m.secure,
				SameSite: http.SameSiteLaxMode,
			})
			c.Next()
		case service.ClaimPageMatched, service.ClaimPageUnknownRequest:
			c.Next()
		case service.ClaimPageCookieBlocked:
			c.Redirect(http.StatusFound, "/approval-unavailable?reason=cookies")
			c.Abort()
		default: // service.ClaimPageRejected
			// The request ID deliberately does not travel on the redirect:
			// the page needs no data, and the spent URL stays out of one
			// more referrer/history entry.
			c.Redirect(http.StatusFound, "/approval-unavailable?reason=opened")
			c.Abort()
		}
	}
}
