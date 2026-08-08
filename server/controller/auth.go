package controller

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
)

// NewAuthController registers OIDC login/callback routes on group. Unlike
// the other controllers these are browser-facing redirects, not JSON API
// calls, so they're expected to be mounted outside the /api group (see
// bootstrap/router.go).
func NewAuthController(group *gin.RouterGroup, authService service.AuthProvider) {
	a := &authController{authService: authService}
	group.GET("/login", a.loginHandler)
	group.GET("/callback", a.callbackHandler)
}

// authController handles the OIDC login/callback HTTP routes.
type authController struct {
	authService service.AuthProvider
}

// loginHandler handles GET /auth/login: starts the OIDC flow by redirecting
// to the provider's authorization URL, with a fresh CSRF state value stored
// in the session for callbackHandler to check.
func (a *authController) loginHandler(g *gin.Context) {
	state, err := randomState()
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
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
		return
	}

	g.Redirect(http.StatusFound, authURL)
}

// callbackHandler handles GET /auth/callback: checks the OIDC state value
// against the one loginHandler stored, exchanges the authorization code,
// establishes the session, and redirects into the web UI.
//
// TODO: redirect to the pending request (if any) rather than always "/".
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
		return
	}

	identity, err := a.authService.HandleCallback(g.Request.Context(), code, nonce)
	if err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	if err := middleware.SetIdentitySession(g, identity); err != nil {
		_ = g.Error(err) //nolint:errcheck // g.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
		return
	}

	g.Redirect(http.StatusFound, "/")
}

// randomState returns a random, URL-safe CSRF state value for the OIDC
// login redirect.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate OIDC state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
