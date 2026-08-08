package middleware

import (
	"strings"

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
	sessionKeyIdentitySubject  = "identity_subject"
	sessionKeyIdentityUsername = "identity_username"
	sessionKeyIdentityEmail    = "identity_email"
	// sessionKeyIdentityGroups is comma-joined. Group names sourced from
	// OIDC/LDAP aren't expected to contain commas; revisit if that changes.
	sessionKeyIdentityGroups = "identity_groups"
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

// SetIdentitySession persists identity in the session, logging the browser
// in for subsequent requests to pick up via SessionAuthMiddleware.
func SetIdentitySession(c *gin.Context, identity *service.Identity) error {
	sess := sessions.Default(c)
	sess.Set(sessionKeyIdentitySubject, identity.Subject)
	sess.Set(sessionKeyIdentityUsername, identity.Username)
	sess.Set(sessionKeyIdentityEmail, identity.Email)
	sess.Set(sessionKeyIdentityGroups, strings.Join(identity.Groups, ","))
	return sess.Save()
}

// ClearIdentitySession logs the current session out.
func ClearIdentitySession(c *gin.Context) error {
	sess := sessions.Default(c)
	sess.Clear()
	return sess.Save()
}

// SessionAuthMiddleware protects the web UI approval endpoints (list/
// approve/deny pending certificate requests), which require a valid,
// logged-in session (see SetIdentitySession, written by the OIDC callback
// in controller.authController).
type SessionAuthMiddleware struct{}

// NewSessionAuthMiddleware creates a SessionAuthMiddleware.
func NewSessionAuthMiddleware() *SessionAuthMiddleware {
	return &SessionAuthMiddleware{}
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
		c.Next()
	}
}
