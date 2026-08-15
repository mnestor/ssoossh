package bootstrap

import (
	"net/http"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// Test methodology: table-driven over the config permutations that decide
// the session cookie's attributes, asserting the resolved sessions.Options
// rather than a served response — the store owns serialization, and what
// this code is responsible for is the decision.

// boolPtr returns a pointer to b, for the tri-state cookie_secure setting.
func boolPtr(b bool) *bool { return &b }

func TestSessionCookieOptions_ShouldHardenTheSessionCookie(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		isHTTPS      bool
		cookieSecure *bool
		sameSite     string
		maxAge       time.Duration
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
			isHTTPS:      true,
			wantSecure:   true,
			wantSameSite: http.SameSiteStrictMode,
		},
		{
			name:         "should let an explicit setting override the inference",
			isHTTPS:      true,
			cookieSecure: boolPtr(false),
			wantSecure:   false,
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
			name:         "should convert max age to seconds",
			maxAge:       2 * time.Hour,
			wantSameSite: http.SameSiteStrictMode,
			wantMaxAge:   7200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := &config.Config{}
			c.HTTP.IsHTTPS = tt.isHTTPS
			c.HTTP.CookieSecure = tt.cookieSecure
			c.HTTP.CookieSameSite = tt.sameSite
			c.HTTP.CookieMaxAge = tt.maxAge

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
			// An unset cookie_max_age yields the default rather than zero;
			// zero would write every session already expired. Covered
			// directly by TestSessionCookieOptions_ShouldNeverProduceAZeroMaxAge.
			wantMaxAge := tt.wantMaxAge
			if wantMaxAge == 0 {
				wantMaxAge = int(defaultCookieMaxAge.Seconds())
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
