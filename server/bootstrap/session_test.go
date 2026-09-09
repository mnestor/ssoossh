package bootstrap

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/wader/gormstore/v2"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// Test methodology: table-driven over the config permutations that decide
// the session cookie's attributes, asserting the resolved sessions.Options
// rather than a served response — the store owns serialization, and what
// this code is responsible for is the decision.

// boolPtr returns a pointer to b, for the tri-state config settings.
func boolPtr(b bool) *bool { return &b }

func TestSessionCookieOptions_ShouldHardenTheSessionCookie(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		publicURL    string
		sameSite     string
		maxAge       time.Duration
		idleTimeout  time.Duration
		wantSecure   bool
		wantSameSite http.SameSite
		wantMaxAge   int
	}{
		{
			name:         "should not mark the cookie secure over plain http",
			wantSecure:   false,
			wantSameSite: http.SameSiteStrictMode,
		},
		{
			// A Secure cookie over plain HTTP is silently dropped by the
			// browser, so this must follow the deployment, not default on.
			name:         "should mark the cookie secure when tls terminates in front",
			publicURL:    "https://ssh.example.com",
			wantSecure:   true,
			wantSameSite: http.SameSiteStrictMode,
		},
		{
			name:         "should honour an explicit lax same-site",
			sameSite:     "lax",
			wantSameSite: http.SameSiteLaxMode,
		},
		{
			name:         "should honour an explicit none same-site",
			sameSite:     "NONE",
			wantSameSite: http.SameSiteNoneMode,
		},
		{
			// The cookie attribute carries the idle window (the sliding
			// refresh reissues it); the absolute cap is enforced separately
			// by SessionAuthMiddleware, so cookie_max_age must not leak
			// into the cookie header.
			name:         "should put the idle window in the cookie max age",
			maxAge:       2 * time.Hour,
			idleTimeout:  20 * time.Minute,
			wantSameSite: http.SameSiteStrictMode,
			wantMaxAge:   1200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &config.Config{}
			c.HTTP.PublicURL = tt.publicURL
			c.HTTP.CookieSameSite = tt.sameSite
			c.HTTP.CookieMaxAge = tt.maxAge
			c.HTTP.CookieIdleTimeout = tt.idleTimeout

			opts, err := sessionCookieOptions(c)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !opts.HttpOnly {
				t.Error("expected HttpOnly to always be set")
			}
			if opts.Path != "/" {
				t.Errorf("got Path %q, want %q", opts.Path, "/")
			}
			if opts.Secure != tt.wantSecure {
				t.Errorf("got Secure %v, want %v", opts.Secure, tt.wantSecure)
			}
			if opts.SameSite != tt.wantSameSite {
				t.Errorf("got SameSite %v, want %v", opts.SameSite, tt.wantSameSite)
			}
			// An unset cookie_idle_timeout yields the default rather than
			// zero; zero would write every session already expired. Covered
			// directly by TestSessionCookieOptions_ShouldNeverProduceAZeroMaxAge.
			wantMaxAge := tt.wantMaxAge
			if wantMaxAge == 0 {
				wantMaxAge = int(defaultCookieIdleTimeout.Seconds())
			}
			if opts.MaxAge != wantMaxAge {
				t.Errorf("got MaxAge %d, want %d", opts.MaxAge, wantMaxAge)
			}
		})
	}
}

func TestSessionCookieOptions_ShouldRejectAnUnknownSameSite(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.HTTP.CookieSameSite = "sometimes"

	if _, err := sessionCookieOptions(c); err == nil {
		t.Fatal("expected an error for an unrecognized http.cookie_same_site")
	}
}

// TestResolveSessionSecret_ShouldPersistAGeneratedKey is the regression test
// for "sessions survive a restart": a second call, standing in for a second
// process against the same database, must return the same key rather than
// generating a fresh one and invalidating every issued session.
func TestResolveSessionSecret_ShouldPersistAGeneratedKey(t *testing.T) {
	t.Parallel()

	a := newTestApp(t, &config.Config{})

	first, err := resolveSessionSecret(a.config, a.db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("expected a generated session secret")
	}

	second, err := resolveSessionSecret(a.config, a.db)
	if err != nil {
		t.Fatalf("unexpected error on the second resolve: %v", err)
	}
	if string(second) != string(first) {
		t.Error("expected the persisted session secret to be reused, got a different key")
	}

	var count int64
	if err := a.db.Model(&model.ServerSecret{}).Where("name = ?", model.ServerSecretSessionKey).Count(&count).Error; err != nil {
		t.Fatalf("unexpected error counting stored secrets: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d stored session secrets, want exactly 1", count)
	}
}

// TestResolveSessionSecret_ShouldPreferTheConfiguredKey pins that an
// explicit cookie_key wins and is not written to the database — an operator
// keying it from outside should not find a copy stored inside.
func TestResolveSessionSecret_ShouldPreferTheConfiguredKey(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.HTTP.CookieKey = "configured-key"
	a := newTestApp(t, c)

	got, err := resolveSessionSecret(a.config, a.db)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "configured-key" {
		t.Errorf("got %q, want the configured key", string(got))
	}

	var count int64
	if err := a.db.Model(&model.ServerSecret{}).Count(&count).Error; err != nil {
		t.Fatalf("unexpected error counting stored secrets: %v", err)
	}
	if count != 0 {
		t.Errorf("got %d stored secrets, want none when cookie_key is configured", count)
	}
}

// TestSessionCookieOptions_ShouldNeverProduceAZeroMaxAge is a regression test
// for a bug that made every login fail after the fact. Leaving MaxAge unset
// does not fall back to the store's default — Store.Options replaces the
// whole struct — and gormstore writes each row with
// expires_at = now + MaxAge, then reads with `expires_at > now`. A zero here
// means the session is expired the instant it is written.
func TestSessionCookieOptions_ShouldNeverProduceAZeroMaxAge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		age  time.Duration
	}{
		{name: "should apply a default when unset", age: 0},
		{name: "should apply a default when negative", age: -time.Hour},
		{name: "should honor a configured value", age: 30 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &config.Config{}
			c.HTTP.CookieMaxAge = tt.age

			opts, err := sessionCookieOptions(c)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opts.MaxAge <= 0 {
				t.Fatalf("got MaxAge %d, want a positive value — a session written with this expires immediately", opts.MaxAge)
			}
			if tt.age > 0 && opts.MaxAge != int(tt.age.Seconds()) {
				t.Errorf("got MaxAge %d, want the configured %d", opts.MaxAge, int(tt.age.Seconds()))
			}
		})
	}
}

// bigGroupList returns a comma-joined group list in the shape
// middleware.SetIdentitySession writes, sized past the 47KB seen in
// production and well past securecookie's 4096-byte default.
func bigGroupList(t *testing.T) string {
	t.Helper()

	groups := make([]string, 0, 2000)
	for i := range 2000 {
		groups = append(groups, fmt.Sprintf("cn=group-%04d,ou=groups,dc=example,dc=com", i))
	}
	joined := strings.Join(groups, ",")
	if len(joined) < 60_000 {
		t.Fatalf("fixture too small to exercise the limit: %d bytes", len(joined))
	}
	return joined
}

// TestInitEngine_ShouldSaveASessionCarryingALargeGroupList pins the fix for
// the production failure: a directory handing back hundreds of group
// memberships produced a 47KB session payload, and securecookie's
// 4096-byte default — a browser cookie limit, which gormstore applies to a
// payload that lives in a database column and never reaches a cookie —
// failed the save with "securecookie: the value is too long" and 500'd
// /auth/callback.
//
// Through the engine initEngine actually builds, not a store assembled by
// the test: the thing that regressed would be forgetting to configure the
// store, which a hand-built store would hide.
func TestInitEngine_ShouldSaveASessionCarryingALargeGroupList(t *testing.T) {
	t.Parallel()

	a := newTestApp(t, &config.Config{})
	r, err := a.initEngine()
	if err != nil {
		t.Fatalf("initEngine() error = %v", err)
	}

	var saveErr error
	r.GET("/save-big-session", func(gc *gin.Context) {
		sess := sessions.Default(gc)
		sess.Set("identity_groups", bigGroupList(t))
		saveErr = sess.Save()
		gc.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/save-big-session", nil)
	req.Host = a.config.HTTP.PublicHost()
	r.ServeHTTP(httptest.NewRecorder(), req)

	if saveErr != nil {
		t.Errorf("saving a session with a large group list failed: %v", saveErr)
	}
}

// And the default really is what would have refused it, so the line in
// initEngine is load-bearing rather than decorative. A store left at
// securecookie's default takes the same payload the engine above accepted
// and rejects it.
func TestSessionStore_ShouldRefuseALargeGroupListAtTheSecurecookieDefault(t *testing.T) {
	t.Parallel()

	store, req, w := newTestSessionStore(t, 0)

	sess, err := store.New(req, "ssoossh_session")
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	sess.Values["identity_groups"] = bigGroupList(t)

	if err := store.Save(req, w, sess); err == nil {
		t.Error("a store at securecookie's 4096-byte default accepted a 60KB payload; the default this fix works around is gone, so the fix needs revisiting")
	}
}

// The cap is still a cap: a payload past it is refused rather than written,
// so an unbounded session is a visible failure and not a database row that
// quietly grows without limit.
func TestSessionStore_ShouldRefuseAPayloadPastTheCap(t *testing.T) {
	t.Parallel()

	store, req, w := newTestSessionStore(t, maxSessionPayloadBytes)

	sess, err := store.New(req, "ssoossh_session")
	if err != nil {
		t.Fatalf("store.New() error = %v", err)
	}
	sess.Values["identity_groups"] = strings.Repeat("x", maxSessionPayloadBytes+1)

	if err := store.Save(req, w, sess); err == nil {
		t.Error("saving a session past the cap returned no error, want the store to refuse it")
	}
}

// newTestSessionStore builds a gormstore against a throwaway database, with
// maxLength applied when non-zero (zero leaves securecookie's own default in
// place, which is the case one of the tests above is about).
func newTestSessionStore(t *testing.T, maxLength int) (*gormSessionStore, *http.Request, *httptest.ResponseRecorder) {
	t.Helper()

	a := newTestApp(t, &config.Config{})
	secret, err := resolveSessionSecret(a.config, a.db)
	if err != nil {
		t.Fatalf("resolveSessionSecret() error = %v", err)
	}

	gs := gormstore.New(a.db, secret)
	if maxLength > 0 {
		gs.MaxLength(maxLength)
	}
	store := &gormSessionStore{Store: gs}
	store.Options(sessions.Options{Path: "/", MaxAge: 3600})

	return store, httptest.NewRequest(http.MethodGet, "/auth/callback", nil), httptest.NewRecorder()
}
