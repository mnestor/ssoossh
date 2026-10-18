package bootstrap

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// The NSS routes through the real engine: what the access log records for
// a lookup, and that the global per-IP limiter leaves the lookups to their
// own.

// nssRouterConfig is a config with NSS on, its shipped limits, an admin
// group, and the given global rate limit.
func nssRouterConfig(rateLimit int) *config.Config {
	c := &config.Config{}
	c.HTTP.RateLimit = rateLimit
	c.HTTP.RateDuration = time.Minute
	c.HTTP.AccessLogging.WithRequestHeader = true
	c.HTTP.AccessLogging.WithClientIP = true
	c.Admin.RequireGroup = "admins"
	c.NSS = config.NSSConfig{
		Enabled: true, AutoPrivateGroups: config.AutoPrivateGroupsHybrid, TombstoneRetention: time.Hour,
		DefaultHome: "/home/%u", DisabledShell: "/usr/sbin/nologin", AllowedShells: []string{"bash"},
		MaxGroupMembers: 10, RateLimit: 20, RateBurst: 200, KeyMaxLifetime: time.Hour, KeyRotationGrace: time.Hour,
	}
	return c
}

// nssRouterApp builds the app with the NSS services, a users row for an
// admin, one posix account and one fleet key, and returns the router with
// the key's id and secret.
func nssRouterApp(t *testing.T, c *config.Config) (*Server, string, string) {
	t.Helper()

	a := newTestApp(t, c)
	a.svc.audit = service.NewAuditService(c, a.db)
	a.svc.notification = service.NewNotificationService(a.db, nil, false, false, false)
	if err := a.initNSSServices(a.svc); err != nil {
		t.Fatalf("initNSSServices: %v", err)
	}
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("initRouter: %v", err)
	}

	admin := model.User{ID: uuid.NewString(), Subject: "sub-admin", Username: "admin", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := a.db.Create(&admin).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	account := model.PosixUser{ID: uuid.NewString(), Name: "alice", UID: 10001, GID: new(uint32(10001)), DN: "uid=alice,dc=example",
		CreatedAt: time.Now(), UpdatedAt: time.Now(), LastSyncedAt: time.Now()}
	if err := a.db.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	view, secret, err := a.svc.nssKeys.Create(t.Context(), &service.Identity{Subject: "sub-admin", Groups: []string{"admins"}},
		service.NSSKeyCreate{Name: "fleet"}, "")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	return srv, view.Key.ID, secret
}

// nssLookup sends one user lookup for alice from addr with the key headers.
func nssLookup(srv *Server, addr, keyID, secret string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/nss/user/name/alice", nil)
	req.RemoteAddr = addr
	req.Header.Set(apitypes.HeaderNSSKeyID, keyID)
	req.Header.Set(apitypes.HeaderNSSKeySecret, secret)
	srv.router.ServeHTTP(w, req)
	return w
}

// TestNSSRouter_ShouldLogTheKeyIDButNeverTheSecretWhenALookupIsServed
// reads the access log the way it ships: request headers on, which is the
// default. The secret is taken off the request straight after the access
// logger, which writes the headers only once the chain has run.
//
// Installs the process logger; must not run in parallel.
func TestNSSRouter_ShouldLogTheKeyIDButNeverTheSecretWhenALookupIsServed(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))

	srv, keyID, secret := nssRouterApp(t, nssRouterConfig(0))
	wrongSecret := "ssoossh_nss_" + strings.Repeat("Z", 43)

	tests := []struct {
		name       string
		secret     string
		wantStatus int
	}{
		{name: "should serve the lookup when the key is right", secret: secret, wantStatus: http.StatusOK},
		{name: "should refuse the lookup when the secret is wrong", secret: wrongSecret, wantStatus: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := nssLookup(srv, "203.0.113.60:4000", keyID, tt.secret)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}

	logged := logs.String()
	if !strings.Contains(logged, "/api/nss/user/name/alice") {
		t.Fatalf("no access log line for the lookup in:\n%s", logged)
	}
	if !strings.Contains(logged, keyID) {
		t.Errorf("the access log does not carry the key id %s:\n%s", keyID, logged)
	}
	for _, s := range []string{secret, wrongSecret, strings.TrimPrefix(secret, "ssoossh_nss_")} {
		if strings.Contains(logged, s) {
			t.Errorf("the log carries a secret:\n%s", logged)
		}
	}
	if strings.Contains(strings.ToLower(logged), strings.ToLower(apitypes.HeaderNSSKeySecret)) {
		t.Errorf("the log names the secret header at all:\n%s", logged)
	}
}

func TestNSSRouter_ShouldLeaveLookupsToTheirOwnLimiterWhenTheGlobalOneIsTight(t *testing.T) {
	t.Parallel()

	srv, keyID, secret := nssRouterApp(t, nssRouterConfig(1))
	addr := "203.0.113.61:4000"

	for i := range 5 {
		w := nssLookup(srv, addr, keyID, secret)
		if w.Code != http.StatusOK {
			t.Fatalf("lookup %d = %d, want 200: the global limit of 1 must not apply, body %s", i, w.Code, w.Body.String())
		}
		if w.Header().Get("RateLimit-Limit") != "200" {
			t.Errorf("lookup %d RateLimit-Limit = %q, want the fleet key bucket's 200", i, w.Header().Get("RateLimit-Limit"))
		}
	}

	// The session routes under /api/nss keep the global limit.
	codes := []int{}
	for range 2 {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/nss/shells", nil)
		req.RemoteAddr = addr
		srv.router.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	if codes[1] != http.StatusTooManyRequests {
		t.Errorf("shells codes = %v, want the second refused by the global limit", codes)
	}
}

func TestNSSRouter_ShouldAnswerNSSDisabledOnEveryNSSRouteWhenNSSIsOff(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	srv, err := newTestApp(t, c).initRouter()
	if err != nil {
		t.Fatalf("initRouter: %v", err)
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/nss/user/name/alice"},
		{http.MethodGet, "/api/nss/user/uid/1"},
		{http.MethodGet, "/api/nss/group/name/eng"},
		{http.MethodGet, "/api/nss/group/gid/1"},
		{http.MethodGet, "/api/nss/keys"},
		{http.MethodGet, "/api/nss/shells"},
		{http.MethodPut, "/api/users/me/shell"},
	} {
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), apitypes.ErrorCodeNSSDisabled) {
			t.Errorf("%s %s = %d %s, want 404 nss_disabled", route.method, route.path, w.Code, w.Body.String())
		}
	}
}
