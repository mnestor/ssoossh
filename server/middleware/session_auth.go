package middleware

import (
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/service"
)

// IdentityContextKey is the gin.Context key SessionAuthMiddleware sets the
// authenticated *service.Identity under.
const IdentityContextKey = "ssoossh.identity"

// Session keys used to persist auth state in the gin-contrib/sessions
// session (see bootstrap.initRouter for the store setup). Centralized here
// so SessionAuthMiddleware (reader) and the Set*/Pop*/Clear* helpers below
// (writers, used by controller.authController) agree on the same keys.
const (
	sessionKeyOIDCState        = "oidc_state"
	sessionKeyOIDCNonce        = "oidc_nonce"
	sessionKeyOIDCVerifier     = "oidc_verifier"
	sessionKeyReturnURL        = "return_url"
	sessionKeyIdentitySubject  = "identity_subject"
	sessionKeyIdentityUsername = "identity_username"
	sessionKeyIdentityEmail    = "identity_email"
	// sessionKeyIdentityGroups is comma-joined. Group names sourced from
	// OIDC/LDAP aren't expected to contain commas; revisit if that changes.
	sessionKeyIdentityGroups = "identity_groups"
	// sessionKeyIdentityRefreshedAt is the Unix time the session was last
	// written, set by SetIdentitySession and updated by the sliding-expiry
	// refresh in SessionAuthMiddleware. It exists so the middleware can
	// tell how far into its lifetime a session is without a Save on every
	// request.
	sessionKeyIdentityRefreshedAt = "identity_refreshed_at"
)

// sessionString reads key from sess as a string, returning "" if it's
// absent or not a string (e.g. a stale session predating a key rename).
func sessionString(sess sessions.Session, key string) string {
	v, ok := sess.Get(key).(string)
	if !ok {
		return ""
	}
	return v
}

// SetOIDCState stores state in the session for a later PopOIDCState call to
// check against the OIDC callback (CSRF protection for the login redirect).
func SetOIDCState(c *gin.Context, state string) error {
	sess := sessions.Default(c)
	sess.Set(sessionKeyOIDCState, state)
	return sess.Save()
}

// PopOIDCState returns the state stored by SetOIDCState and clears it, so
// each login attempt's state value can only be consumed once.
func PopOIDCState(c *gin.Context) (string, error) {
	sess := sessions.Default(c)
	state := sessionString(sess, sessionKeyOIDCState)
	sess.Delete(sessionKeyOIDCState)
	return state, sess.Save()
}

// SetOIDCNonce stores nonce in the session for a later PopOIDCNonce call to
// pass to service.AuthProvider.HandleCallback (replay protection for the
// ID token, distinct from SetOIDCState's CSRF protection for the redirect).
func SetOIDCNonce(c *gin.Context, nonce string) error {
	sess := sessions.Default(c)
	sess.Set(sessionKeyOIDCNonce, nonce)
	return sess.Save()
}

// PopOIDCNonce returns the nonce stored by SetOIDCNonce and clears it, so
// each login attempt's nonce value can only be consumed once.
func PopOIDCNonce(c *gin.Context) (string, error) {
	sess := sessions.Default(c)
	nonce := sessionString(sess, sessionKeyOIDCNonce)
	sess.Delete(sessionKeyOIDCNonce)
	return nonce, sess.Save()
}

// SetOIDCVerifier stores verifier in the session for a later PopOIDCVerifier
// call to exchange an authorization code with PKCE (S256) protection.
func SetOIDCVerifier(c *gin.Context, verifier string) error {
	sess := sessions.Default(c)
	sess.Set(sessionKeyOIDCVerifier, verifier)
	return sess.Save()
}

// PopOIDCVerifier returns the verifier stored by SetOIDCVerifier and clears
// it, so each login attempt's verifier value can only be consumed once.
func PopOIDCVerifier(c *gin.Context) (string, error) {
	sess := sessions.Default(c)
	verifier := sessionString(sess, sessionKeyOIDCVerifier)
	sess.Delete(sessionKeyOIDCVerifier)
	return verifier, sess.Save()
}

// SetReturnURL stores returnURL in the session for a later PopReturnURL
// call to redirect back to once login completes. The web UI is a JS/AJAX
// consumer of the API (see root CLAUDE.md), so it — not this server — is
// what decides to redirect the browser to /auth/login?return_to=<url> on a
// 401; this just captures and replays that value.
func SetReturnURL(c *gin.Context, returnURL string) error {
	sess := sessions.Default(c)
	sess.Set(sessionKeyReturnURL, returnURL)
	return sess.Save()
}

// PopReturnURL returns the URL stored by SetReturnURL and clears it, so it
// can only be consumed once. Returns "" if none was set.
func PopReturnURL(c *gin.Context) (string, error) {
	sess := sessions.Default(c)
	returnURL := sessionString(sess, sessionKeyReturnURL)
	sess.Delete(sessionKeyReturnURL)
	return returnURL, sess.Save()
}

// SetIdentitySession persists identity in the session, logging the browser
// in for subsequent requests to pick up via SessionAuthMiddleware.
func SetIdentitySession(c *gin.Context, identity *service.Identity) error {
	sess := sessions.Default(c)
	sess.Set(sessionKeyIdentitySubject, identity.Subject)
	sess.Set(sessionKeyIdentityUsername, identity.Username)
	sess.Set(sessionKeyIdentityEmail, identity.Email)
	sess.Set(sessionKeyIdentityGroups, strings.Join(identity.Groups, ","))
	sess.Set(sessionKeyIdentityRefreshedAt, time.Now().Unix())
	return sess.Save()
}

// ClearIdentitySession logs the current session out.
func ClearIdentitySession(c *gin.Context) error {
	sess := sessions.Default(c)
	sess.Clear()
	return sess.Save()
}

// Identity returns the authenticated identity SessionAuthMiddleware placed
// on the context.
//
// ok is false when the key is absent or holds an unexpected type — the
// former should be impossible behind SessionAuthMiddleware, but MustGet
// panics on a miss and a handler is the wrong place to take down the
// process over a routing mistake.
func Identity(c *gin.Context) (*service.Identity, bool) {
	v, exists := c.Get(IdentityContextKey)
	if !exists {
		return nil, false
	}
	identity, ok := v.(*service.Identity)
	return identity, ok
}

// SessionAuthMiddleware protects the web UI approval endpoints (list/
// approve/deny pending certificate requests), which require a valid,
// logged-in session (see SetIdentitySession, written by the OIDC callback
// in controller.authController).
//
// It also slides the session's expiry: cookie MaxAge is otherwise absolute
// — written once at login and never touched again — so an actively-clicking
// user was logged out mid-task when it ran down. Once a request arrives
// past half the session's lifetime, the middleware re-saves the session,
// which reissues the cookie with a fresh MaxAge and pushes the store row's
// expires_at forward. Half-life rather than every request keeps the
// Set-Cookie header and the session-store write off the common path.
// The result is an idle timeout: activity extends the session, absence
// ends it. Deliberately no client-side keepalive to go with this — a poll
// would keep a session alive for an unattended browser, which is the case
// the timeout exists for.
type SessionAuthMiddleware struct {
	// maxAge is the session lifetime in force (bootstrap's
	// resolvedCookieMaxAge), which the refresh threshold derives from.
	maxAge time.Duration
}

// NewSessionAuthMiddleware creates a SessionAuthMiddleware. maxAge must be
// the same lifetime the session store's cookie options were built with, or
// refreshed cookies come back with a different lifetime than first-issue
// ones.
func NewSessionAuthMiddleware(maxAge time.Duration) *SessionAuthMiddleware {
	return &SessionAuthMiddleware{maxAge: maxAge}
}

// Add returns a gin.HandlerFunc that reads the identity persisted by
// SetIdentitySession and sets IdentityContextKey, failing closed with 401
// if the session has no identity (never logged in, logged out, or expired).
func (m *SessionAuthMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := sessions.Default(c)

		subject := sessionString(sess, sessionKeyIdentitySubject)
		if subject == "" {
			_ = c.Error(&UnauthorizedError{}) //nolint:errcheck // c.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
			c.Abort()
			return
		}

		username := sessionString(sess, sessionKeyIdentityUsername)
		email := sessionString(sess, sessionKeyIdentityEmail)

		var groups []string
		if groupsCSV := sessionString(sess, sessionKeyIdentityGroups); groupsCSV != "" {
			groups = strings.Split(groupsCSV, ",")
		}

		c.Set(IdentityContextKey, &service.Identity{
			Subject:  subject,
			Username: username,
			Email:    email,
			Groups:   groups,
		})

		// Sliding expiry: re-save once past half the lifetime (see the type
		// comment). A session predating sessionKeyIdentityRefreshedAt has an
		// unknown age, so refresh it immediately rather than guessing. A
		// failed save is logged-and-continued rather than fatal: the session
		// is still valid as-is, and failing the request would turn a refresh
		// optimization into an outage.
		refreshedAt, _ := sess.Get(sessionKeyIdentityRefreshedAt).(int64)
		if time.Since(time.Unix(refreshedAt, 0)) > m.maxAge/2 {
			sess.Set(sessionKeyIdentityRefreshedAt, time.Now().Unix())
			if err := sess.Save(); err != nil {
				_ = c.Error(err) //nolint:errcheck // c.Error only registers the error for logging; the request proceeds on the still-valid session.
			}
		}

		c.Next()
	}
}
