package middleware

import (
	"log/slog"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
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
	// sessionKeyOIDCEcho marks the in-flight OIDC round trip as a claims
	// echo rather than a login, so the callback renders the token instead
	// of establishing a session. Set only by the echo start endpoint, and
	// consumed once.
	sessionKeyOIDCEcho = "oidc_echo"
	sessionKeyReturnURL        = "return_url"
	sessionKeyIdentitySubject  = "identity_subject"
	sessionKeyIdentityUsername = "identity_username"
	sessionKeyIdentityEmail    = "identity_email"
	// sessionKeyIdentityDisplayName carries the human-readable name so a
	// deployment with no identity refresher still renders it. Display
	// only, and re-read from the database on every refreshed request, so a
	// stale value here is corrected rather than acted on.
	sessionKeyIdentityDisplayName = "identity_display_name"
	// sessionKeyIdentityGroups is comma-joined. Group names sourced from
	// OIDC/LDAP aren't expected to contain commas; revisit if that changes.
	sessionKeyIdentityGroups = "identity_groups"
	// sessionKeyIdentityOtherAccounts and sessionKeyIdentityServiceAccounts
	// are comma-joined too, same convention and same caveat as the groups
	// key above. Both have to survive the round trip: service certificate
	// approval is authorized against identity.ServiceAccounts (see
	// service.checkServiceAccountLinkage), and both are snapshotted into
	// the decision audit record, so an identity rebuilt without them is an
	// identity that cannot approve anything and audits as account-less.
	//
	// Cheap to carry: gormstore keeps the session payload in a database
	// column and the cookie holds only the session id, so this costs text
	// on the session row, not cookie bytes.
	sessionKeyIdentityOtherAccounts   = "identity_other_accounts"
	sessionKeyIdentityServiceAccounts = "identity_service_accounts"
	// sessionKeyIdentityRefreshedAt is the Unix time the session was last
	// written, set by SetIdentitySession and updated by the sliding-expiry
	// refresh in SessionAuthMiddleware. It exists so the middleware can
	// tell how far into its idle window a session is without a Save on
	// every request.
	sessionKeyIdentityRefreshedAt = "identity_refreshed_at"
	// sessionKeyIdentityIssuedAt is the Unix time the session was created
	// at login. Unlike refreshedAt it is never updated afterwards - it is
	// what the absolute session cap is measured against, so sliding must
	// not touch it.
	sessionKeyIdentityIssuedAt = "identity_issued_at"
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

// sessionStringSlice reads a comma-joined list written by
// SetIdentitySession, returning nil for an absent or empty value rather
// than the one-element slice strings.Split would give for "".
func sessionStringSlice(sess sessions.Session, key string) []string {
	raw := sessionString(sess, key)
	if raw == "" {
		return nil
	}
	return strings.Split(raw, ",")
}

// SetOIDCState stores state in the session for a later PopOIDCState call to
// check against the OIDC callback (CSRF protection for the login redirect).
func SetOIDCState(c *gin.Context, state string) error {
	sess := sessions.Default(c)
	sess.Set(sessionKeyOIDCState, state)
	return sess.Save()
}

// SetOIDCLoginState stores the state, nonce and PKCE verifier that a login
// redirect needs, plus returnURL when one was supplied, in a single session
// write.
//
// The individual setters below each Save(), so setting these one at a time
// cost one store round trip per value even though they are only ever
// written together at the start of a login. Writing them as a unit also
// makes the failure atomic: previously a failure partway through left the
// earlier values persisted against a login that could never complete.
//
// An empty returnURL is not stored, matching the caller's behavior of only
// setting it for a return_to that passed isSafeReturnURL.
func SetOIDCLoginState(c *gin.Context, state, nonce, verifier, returnURL string) error {
	sess := sessions.Default(c)
	sess.Set(sessionKeyOIDCState, state)
	sess.Set(sessionKeyOIDCNonce, nonce)
	sess.Set(sessionKeyOIDCVerifier, verifier)
	if returnURL != "" {
		sess.Set(sessionKeyReturnURL, returnURL)
	}
	return sess.Save()
}

// SetOIDCEchoState stores the state, nonce and PKCE verifier for a claims
// echo, and marks the round trip as an echo.
//
// A separate flag rather than a different callback route: the redirect URI
// is registered with the identity provider, and adding a second one is an
// operator task in a system the operator may not control. The flag is what
// the one callback branches on.
func SetOIDCEchoState(c *gin.Context, state, nonce, verifier string) error {
	sess := sessions.Default(c)
	sess.Set(sessionKeyOIDCState, state)
	sess.Set(sessionKeyOIDCNonce, nonce)
	sess.Set(sessionKeyOIDCVerifier, verifier)
	sess.Set(sessionKeyOIDCEcho, true)
	return sess.Save()
}

// PopOIDCEcho reports whether the in-flight round trip is a claims echo,
// and clears the flag so it can only be consumed once. A missing or
// wrong-typed value is false, which is the login path — the safe default,
// since the echo path is the one that skips establishing a session.
func PopOIDCEcho(c *gin.Context) (bool, error) {
	sess := sessions.Default(c)
	echo, _ := sess.Get(sessionKeyOIDCEcho).(bool)
	sess.Delete(sessionKeyOIDCEcho)
	return echo, sess.Save()
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

// SetReturnURL stores returnURL in the session for a later PopReturnURL call
// to redirect back to once login completes. The web UI is a JS/AJAX consumer
// of the API (see https://mnestor.github.io/ssoossh/internals/invariants/),
// so it — not this server — is what decides to redirect the browser to
// /auth/login?return_to=<url> on a 401; this just captures and replays that
// value.
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
	sess.Set(sessionKeyIdentityDisplayName, identity.DisplayName)
	sess.Set(sessionKeyIdentityGroups, strings.Join(identity.Groups, ","))
	sess.Set(sessionKeyIdentityOtherAccounts, strings.Join(identity.OtherAccounts, ","))
	sess.Set(sessionKeyIdentityServiceAccounts, strings.Join(identity.ServiceAccounts, ","))
	now := time.Now().Unix()
	sess.Set(sessionKeyIdentityIssuedAt, now)
	sess.Set(sessionKeyIdentityRefreshedAt, now)
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
// It enforces a two-tier lifetime, the same shape as a corporate SSO:
//
//   - An idle window (idleTimeout): the cookie's MaxAge. Once a request
//     arrives past half the window the middleware re-saves the session,
//     which reissues the cookie with a fresh MaxAge and pushes the store
//     row's expires_at forward. Half-life rather than every request keeps
//     the Set-Cookie header and the store write off the common path. An
//     abandoned browser expires after one quiet window; an active one
//     slides indefinitely -
//   - - until the absolute cap (maxSession), measured against the login
//     timestamp that sliding never touches. Past it, the next request is
//     unauthenticated no matter how active the session was, and the user
//     signs in again. This is also what bounds how stale the session's
//     group claims can get, since the refresh deliberately does not
//     re-check them against the identity provider.
//
// Deliberately no client-side keepalive to go with this - a poll would
// keep a session alive for an unattended browser, which is the case the
// idle window exists for.
type SessionAuthMiddleware struct {
	// idleTimeout is the sliding window (bootstrap's
	// resolvedCookieIdleTimeout), which the refresh threshold derives from.
	idleTimeout time.Duration
	// maxSession is the absolute cap (bootstrap's resolvedCookieMaxAge),
	// enforced against sessionKeyIdentityIssuedAt.
	maxSession time.Duration
	// refresher re-reads the account lists and extra fields from the
	// database on every request, so a directory sync that removed an
	// account takes effect immediately rather than at the session's next
	// login. Nil skips the refresh, which leaves the cookie's login-time
	// snapshot in place; only tests that assert on the snapshot itself
	// should pass nil.
	refresher service.IdentityRefresher
}

// NewSessionAuthMiddleware creates a SessionAuthMiddleware. idleTimeout
// must be the same lifetime the session store's cookie options were built
// with, or refreshed cookies come back with a different lifetime than
// first-issue ones; maxSession is the absolute cap from the same config.
// refresher may be nil, which leaves identities exactly as the cookie
// carries them.
func NewSessionAuthMiddleware(idleTimeout, maxSession time.Duration, refresher service.IdentityRefresher) *SessionAuthMiddleware {
	return &SessionAuthMiddleware{idleTimeout: idleTimeout, maxSession: maxSession, refresher: refresher}
}

// Add returns a gin.HandlerFunc that reads the identity persisted by
// SetIdentitySession and sets IdentityContextKey, failing closed with 401
// if the session has no identity (never logged in, logged out, or expired).
func (m *SessionAuthMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := sessions.Default(c)

		subject := sessionString(sess, sessionKeyIdentitySubject)
		if subject == "" {
			_ = c.Error(&errorresponses.UnauthorizedError{}) //nolint:errcheck // c.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
			c.Abort()
			return
		}

		// Absolute cap: a session past maxSession is over regardless of
		// activity - sliding extends the idle window below, never this.
		// A session predating sessionKeyIdentityIssuedAt (issuedAt zero)
		// has an unknown age; fail closed and make the user log in again
		// rather than granting a stale session an unbounded lifetime.
		issuedAt, hasIssuedAt := sess.Get(sessionKeyIdentityIssuedAt).(int64)
		if !hasIssuedAt || time.Since(time.Unix(issuedAt, 0)) > m.maxSession {
			// Best-effort clear so the dead session stops round-tripping;
			// the 401 stands even if the save fails.
			sess.Clear()
			_ = sess.Save()                                  //nolint:errcheck // the session is already being rejected; a failed clear changes nothing.
			_ = c.Error(&errorresponses.UnauthorizedError{}) //nolint:errcheck // c.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
			c.Abort()
			return
		}

		username := sessionString(sess, sessionKeyIdentityUsername)
		email := sessionString(sess, sessionKeyIdentityEmail)

		identity := &service.Identity{
			Subject:         subject,
			Username:        username,
			Email:           email,
			DisplayName:     sessionString(sess, sessionKeyIdentityDisplayName),
			Groups:          sessionStringSlice(sess, sessionKeyIdentityGroups),
			OtherAccounts:   sessionStringSlice(sess, sessionKeyIdentityOtherAccounts),
			ServiceAccounts: sessionStringSlice(sess, sessionKeyIdentityServiceAccounts),
		}

		// What the person holds now, not what they held at login. The
		// cookie's account lists are a snapshot of the login-time merge,
		// and a directory sync between logins can remove an account from
		// under it — which, unrefreshed, left a live session still able to
		// approve a certificate for an account the directory had taken
		// away. Groups are deliberately not refreshed: roles are read at
		// login by design, which is what makes the session lifetime the
		// revocation window.
		//
		// Fails open. A database error leaves the snapshot in place and is
		// logged rather than 500ing every authenticated request, because
		// the alternative to slightly stale principals here is no
		// principals at all.
		if m.refresher != nil {
			if err := m.refresher.RefreshIdentity(c.Request.Context(), identity); err != nil {
				slog.WarnContext(c.Request.Context(),
					"failed to refresh the session identity from the database; using the login-time values",
					slog.String("subject", subject), slog.Any("error", err))
			}
		}

		c.Set(IdentityContextKey, identity)

		// Sliding expiry: re-save once past half the idle window (see the
		// type comment). A failed save is logged-and-continued rather than
		// fatal: the session is still valid as-is, and failing the request
		// would turn a refresh optimization into an outage.
		refreshedAt, hasRefreshedAt := sess.Get(sessionKeyIdentityRefreshedAt).(int64)
		if !hasRefreshedAt || time.Since(time.Unix(refreshedAt, 0)) > m.idleTimeout/2 {
			sess.Set(sessionKeyIdentityRefreshedAt, time.Now().Unix())
			if err := sess.Save(); err != nil {
				_ = c.Error(err) //nolint:errcheck // c.Error only registers the error for logging; the request proceeds on the still-valid session.
			}
		}

		c.Next()
	}
}
