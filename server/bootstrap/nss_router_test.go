package bootstrap

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
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
		DefaultHome: "/home/%u", DisabledShell: "/usr/sbin/nologin",
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
		req := httptest.NewRequest(http.MethodGet, "/api/nss/key-settings", nil)
		req.RemoteAddr = addr
		srv.router.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	if codes[1] != http.StatusTooManyRequests {
		t.Errorf("key-settings codes = %v, want the second refused by the global limit", codes)
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
	} {
		w := httptest.NewRecorder()
		srv.router.ServeHTTP(w, httptest.NewRequest(route.method, route.path, nil))
		if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), apitypes.ErrorCodeNSSDisabled) {
			t.Errorf("%s %s = %d %s, want 404 nss_disabled", route.method, route.path, w.Code, w.Body.String())
		}
	}
}

// signInAs adds a route to srv that writes identity into a session, as the
// OIDC callback does, and returns the session cookie it set: every admin
// route then runs its real session and role middleware for identity.
func signInAs(t *testing.T, srv *Server, identity *service.Identity) *http.Cookie {
	t.Helper()

	srv.router.GET("/test-sign-in", func(c *gin.Context) {
		if err := middleware.SetIdentitySession(c, identity); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusNoContent)
	})
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/test-sign-in", nil))
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "ssoossh_session" {
			return cookie
		}
	}
	t.Fatalf("sign-in set no session cookie: %d %s", w.Code, w.Body.String())
	return nil
}

// concretePath fills a route's parameters with placeholder values.
func concretePath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			segments[i] = "1"
		}
	}
	return strings.Join(segments, "/")
}

// TestNSSRouter_ShouldOpenOnlyTheKeyListToAFleetKeyAdminWhenEveryAdminRouteIsSwept
// walks every /api/admin route the real router registers as a fleet key
// admin (admin.nss_key_group) who holds no admin area role: GET
// /api/admin/nss/keys answers 200 and every other route 403, so the key
// list's wider gate widens nothing else, including routes added later.
func TestNSSRouter_ShouldOpenOnlyTheKeyListToAFleetKeyAdminWhenEveryAdminRouteIsSwept(t *testing.T) {
	t.Parallel()

	c := nssRouterConfig(0)
	c.Admin.NSSKeyGroup = "nss-key-admins"
	c.Admin.SOCGroup = "ssh-soc"
	c.Admin.AuditorGroup = "ssh-auditors"
	a := newTestApp(t, c)
	a.svc.audit = service.NewAuditService(c, a.db)
	a.svc.notification = service.NewNotificationService(a.db, nil, false, false, false)
	a.svc.identity = service.NewIdentityService(a.db, nil)
	if err := a.initNSSServices(a.svc); err != nil {
		t.Fatalf("initNSSServices: %v", err)
	}
	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("initRouter: %v", err)
	}
	routes := srv.router.Routes()
	cookie := signInAs(t, srv, &service.Identity{Subject: "sub-keyadmin", Username: "keyadmin", Groups: []string{"nss-key-admins"}})

	swept := 0
	for _, route := range routes {
		if !strings.HasPrefix(route.Path, "/api/admin/") {
			continue
		}
		swept++
		want := http.StatusForbidden
		if route.Method == http.MethodGet && route.Path == "/api/admin/nss/keys" {
			want = http.StatusOK
		}
		t.Run("should answer "+http.StatusText(want)+" when the fleet key admin sends "+route.Method+" "+route.Path, func(t *testing.T) {
			req := httptest.NewRequest(route.Method, concretePath(route.Path), nil)
			req.AddCookie(cookie)
			w := httptest.NewRecorder()
			srv.router.ServeHTTP(w, req)
			if w.Code != want {
				t.Errorf("status = %d, want %d: %s", w.Code, want, w.Body.String())
			}
		})
	}
	// A sweep that found nothing would pass vacuously.
	if swept < 10 {
		t.Fatalf("swept %d admin routes, want the whole admin API", swept)
	}
}
