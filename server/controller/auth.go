package controller

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
)

// NewAuthController registers OIDC login/callback routes on group. Unlike
// the other controllers these are browser-facing redirects, not JSON API
// calls, so they're expected to be mounted outside the /api group (see
// bootstrap/router.go).
func NewAuthController(group *gin.RouterGroup, authService service.AuthProvider, csrfMiddleware gin.HandlerFunc) {
	a := &authController{authService: authService}
	group.GET("/login", a.loginHandler)
	group.GET("/callback", a.callbackHandler)

	// Logout is state-changing and session-authorized, so it needs the same
	// CSRF guard as approve/deny — a forced logout is a nuisance rather than
	// a compromise, but there is no reason to leave it forgeable. No session
	// check: logging out an already-logged-out caller is a no-op, and
	// demanding a session to end one is a needless failure mode.
	group.POST("/logout", csrfMiddleware, a.logoutHandler)
}

// logoutHandler handles POST /auth/logout: clears the session so the
// browser's cookie no longer authorizes anything.
//
// The cookie itself is left for the browser to expire. Clearing the
// server-side session is what actually revokes it — SessionAuthMiddleware
// fails closed on a session with no identity — so a stale cookie in the
// browser grants nothing.
//
// @Summary     End the session
// @Description Clears the server-side session, which is what actually revokes the cookie
// @Description — `SessionAuthMiddleware` fails closed without an identity, so a stale
// @Description cookie left in the browser grants nothing.
// @Description
// @Description CSRF-guarded. No session required: logging out an already-logged-out
// @Description caller is a no-op.
// @Tags        auth
// @Produce     json
// @Success     200 {object} openapidoc.LogoutEnvelope "Logged out"
// @Failure     403 {object} openapidoc.ErrorEnvelope "Cross-origin call"
// @Router      /auth/logout [post]
func (a *authController) logoutHandler(g *gin.Context) {
	if err := middleware.ClearIdentitySession(g); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return           // excluded from coverage: session.Clear() removes any oversized value used to force Save() to fail before Save() runs, see exclude-from-coverage.txt
	}

	respondData(g, gin.H{"logged_out": true})
}

// authController handles the OIDC login/callback HTTP routes.
type authController struct {
	authService service.AuthProvider
}

// loginHandler handles GET /auth/login: starts the OIDC flow by redirecting
// to the provider's authorization URL, with a fresh CSRF state value stored
// in the session for callbackHandler to check. An optional ?return_to= is
// captured for callbackHandler to redirect back to once login completes —
// the web UI (a JS/AJAX API consumer, not this server) decides when to send
// the browser here with one; see middleware.SetReturnURL.
//
// @Summary     Start the OIDC login flow
// @Description Redirects the browser to the identity provider. `return_to` is validated
// @Description as a same-site, path-only relative URL; anything else is ignored rather
// @Description than followed.
// @Tags        auth
// @Param       return_to query string false "Where to land after login" example(/approve/9f1c0b2a-...)
// @Success     302 {string} string "Redirect to the identity provider"
// @Router      /auth/login [get]
func (a *authController) loginHandler(g *gin.Context) {
	state, err := randomState()
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return           // excluded from coverage: crypto/rand.Read failure isn't reproducible in tests, see exclude-from-coverage.txt
	}

	authURL, nonce, err := a.authService.AuthorizationURL(g.Request.Context(), state)
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	if err := middleware.SetOIDCState(g, state); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}
	if err := middleware.SetOIDCNonce(g, nonce); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return           // excluded from coverage: reaching this specific Save() failure (not SetOIDCState's, tested) needs byte-precise session-size engineering, see exclude-from-coverage.txt
	}
	if returnTo := g.Query("return_to"); isSafeReturnURL(returnTo) {
		if err := middleware.SetReturnURL(g, returnTo); err != nil {
			_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
			return           // excluded from coverage: same as SetOIDCNonce above, one Save() call further down the chain
		}
	}

	g.Redirect(http.StatusFound, authURL)
}

// callbackHandler handles GET /auth/callback: checks the OIDC state value
// against the one loginHandler stored, exchanges the authorization code,
// establishes the session, and redirects to the URL loginHandler captured
// (falling back to "/" if none was set).
//
// @Summary     OIDC callback
// @Description Checks `state` against the value stored at login, exchanges the code,
// @Description establishes the session, and redirects to the captured `return_to` (or
// @Description `/`).
// @Tags        auth
// @Param       code  query string true "Authorization code from the identity provider"
// @Param       state query string true "CSRF state value stored at login"
// @Success     302 {string} string "Redirect back into the app"
// @Failure     401 {object} openapidoc.ErrorEnvelope "State mismatch, or the provider rejected the exchange"
// @Router      /auth/callback [get]
func (a *authController) callbackHandler(g *gin.Context) {
	code := g.Query("code")
	state := g.Query("state")

	expectedState, err := middleware.PopOIDCState(g)
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}
	if expectedState == "" || state != expectedState {
		_ = g.Error(&middleware.UnauthorizedError{}) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	nonce, err := middleware.PopOIDCNonce(g)
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return           // excluded from coverage: reaching this specific Save() failure (not PopOIDCState's, tested) needs byte-precise session-size engineering, see exclude-from-coverage.txt
	}

	identity, err := a.authService.HandleCallback(g.Request.Context(), code, nonce)
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	if err := middleware.SetIdentitySession(g, identity); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return           // excluded from coverage: same category as PopOIDCNonce above, further down the chain
	}

	returnURL, err := middleware.PopReturnURL(g)
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return           // excluded from coverage: same category as PopOIDCNonce above, further down the chain
	}
	if !isSafeReturnURL(returnURL) {
		returnURL = "/"
	}

	g.Redirect(http.StatusFound, returnURL)
}

// isSafeReturnURL reports whether url is safe to redirect to: a same-site,
// path-only relative URL. Rejects anything that could send the browser off
// this server — an absolute URL (has a scheme) or a protocol-relative one
// ("//evil.example.com", which browsers resolve using the current scheme
// against that host) — since return_to is untrusted, attacker-controllable
// query input.
func isSafeReturnURL(url string) bool {
	return strings.HasPrefix(url, "/") && !strings.HasPrefix(url, "//")
}

// randomState returns a random, URL-safe CSRF state value for the OIDC
// login redirect.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate OIDC state: %w", err) // excluded from coverage: crypto/rand.Read failure isn't reproducible in tests, see exclude-from-coverage.txt
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
