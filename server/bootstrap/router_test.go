package bootstrap

// Test methodology: Unit tests for router setup and middleware chain using
// httptest.ResponseRecorder to capture responses without a real listener.
// Tests run in parallel (t.Parallel()) and are fast. Each test verifies one
// specific routing behavior or middleware effect. See router_run_test.go for
// integration tests with real network listeners.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/service"
)

// testCAKeyRegistry is a mock implementation of CAKeyRegistry for testing.
type testCAKeyRegistry struct {
	keys []string
}

func (t *testCAKeyRegistry) ActiveKeys(ctx context.Context) ([]string, error) {
	return t.keys, nil
}

// Tests in this file are unit tests for router setup: they call initRouter to
// build a *Server, then use httptest to send requests through the router
// without starting an actual listener. These tests verify the middleware chain,
// handler registration, and request routing logic in isolation, and run fast
// with t.Parallel().
//
// Integration tests that actually start a server (Server.Run with real
// listeners) live in router_run_test.go; those verify end-to-end behavior
// including TLS, network I/O, and listener lifecycle.

// newTestApp builds a minimal *app sufficient to call initRouter: a config,
// a services struct holding a real *service.CAService built from a
// mock registry with a test key (caController.caService is a concrete type,
// not an interface, so a fake can't be substituted without changing
// production code), and an in-memory sqlite *gorm.DB (initRouter's session
// store setup needs a real *gorm.DB - it AutoMigrates its own table on
// construction).
func newTestApp(t *testing.T, c *config.Config) *app {
	t.Helper()

	// Create a mock registry with a test key
	mockReg := &testCAKeyRegistry{
		keys: []string{"ssh-ed25519 AAAA"},
	}

	caSvc, err := service.NewCAService(nil, mockReg)
	if err != nil {
		t.Fatalf("failed to build CAService: %v", err)
	}

	dbConfig := &config.Config{}
	dbConfig.DB.Provider = config.DBProviderSqlite
	dbConfig.DB.Connection = ":memory:"
	db, err := connectDatabase(dbConfig)
	if err != nil {
		t.Fatalf("failed to connect to in-memory test database: %v", err)
	}

	// Run the real embedded migrations rather than AutoMigrate: initRouter
	// reads and writes server_secrets, and a hand-rolled test schema would
	// let the migrations and the code drift apart without any test noticing.
	if err := migrateDatabase(dbConfig.DB.Provider, db); err != nil {
		t.Fatalf("failed to migrate in-memory test database: %v", err)
	}

	return &app{config: c, svc: &services{ca: caSvc}, db: db}
}

func TestInitRouter_ShouldDisableRateLimitWhenRateLimitIsZero(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.HTTP.RateLimit = 0
	c.HTTP.RateDuration = time.Minute

	a := newTestApp(t, c)

	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Fire far more requests than any positive rate limit would allow, all
	// from the same non-local IP (localhost bypasses the limiter
	// separately, so it wouldn't prove anything about the disable-at-zero
	// behavior specifically). Every one of them must reach the real
	// handler and get the full CA payload back.
	for i := 0; i < 25; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/ca", nil)
		req.RemoteAddr = "203.0.113.50:12345"

		srv.router.ServeHTTP(w, req)

		if !strings.Contains(w.Body.String(), `"ca"`) {
			t.Fatalf("request %d: expected the CA payload (rate limiting disabled), got body %q", i, w.Body.String())
		}
	}
}

func TestInitRouter_ShouldEnforceRateLimitWhenRateLimitPositive(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.HTTP.RateLimit = 1
	c.HTTP.RateDuration = time.Minute // one request per minute, burst of 1

	a := newTestApp(t, c)

	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	ip := "203.0.113.51:12345"

	w1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "/api/ca", nil)
	req1.RemoteAddr = ip
	srv.router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK || !strings.Contains(w1.Body.String(), `"ca"`) {
		t.Fatalf("expected first request to succeed with the CA payload, got status %d body %s", w1.Code, w1.Body.String())
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/ca", nil)
	req2.RemoteAddr = ip
	srv.router.ServeHTTP(w2, req2)
	// The burst of 1 was already consumed by the first request, so the
	// second, immediately-following request from the same IP must be
	// rejected by RateLimitMiddleware and translated into a 429 by
	// ErrorHandlerMiddleware, with no CA payload in the body.
	if w2.Code != http.StatusTooManyRequests {
		t.Errorf("expected second immediate request to get status %d, got %d", http.StatusTooManyRequests, w2.Code)
	}
	if strings.Contains(w2.Body.String(), `"ca"`) {
		t.Fatalf("expected second immediate request to be rejected by the rate limiter, but got the CA payload: %s", w2.Body.String())
	}
}

func TestInitRouter_ShouldRejectMismatchedHostWhenServerNameConfigured(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.HTTP.ServerName = "ssh.example.com"
	a := newTestApp(t, c)

	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/ca", nil)
	req.Host = "other.example.com"
	srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusMisdirectedRequest {
		t.Errorf("mismatched host: got status %d, want %d", w.Code, http.StatusMisdirectedRequest)
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/ca", nil)
	req2.Host = "ssh.example.com"
	srv.router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("matching host: got status %d, want %d", w2.Code, http.StatusOK)
	}
}

func TestInitRouter_ShouldKeepHealthEndpointsReachableWhenServerNameConfigured(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.HTTP.ServerName = "ssh.example.com"
	a := newTestApp(t, c)

	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Probes commonly address the server by IP; healthz and ping are
	// registered before the server-name middleware, so they must answer
	// regardless of Host.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Host = "203.0.113.9"
	srv.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
}

func TestInitRouter_ShouldRegisterHealthzRoute(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	a := newTestApp(t, c)

	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
	}
}

// TestInitEngine_ShouldIgnoreForwardedForFromAnUntrustedPeer is the
// regression test for the trusted-proxy default. gin.New() trusts every
// proxy unless SetTrustedProxies is called, so skipping the call for an
// empty list — as this router once did — made ClientIP() report whatever
// X-Forwarded-For the caller sent.
func TestInitEngine_ShouldIgnoreForwardedForFromAnUntrustedPeer(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	a := newTestApp(t, c)

	r, err := a.initEngine()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var gotClientIP string
	r.GET("/whoami", func(gc *gin.Context) {
		gotClientIP = gc.ClientIP()
		gc.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.ServeHTTP(httptest.NewRecorder(), req)

	if gotClientIP == "1.2.3.4" {
		t.Error("ClientIP() returned the caller-supplied X-Forwarded-For value; no proxy is configured, so it must report the peer address")
	}
	if gotClientIP != "203.0.113.10" {
		t.Errorf("got ClientIP %q, want the direct peer address 203.0.113.10", gotClientIP)
	}
}

// TestInitEngine_ShouldHonourForwardedForFromATrustedProxy is the other half:
// the check above must not be achieved by ignoring the header entirely.
func TestInitEngine_ShouldHonourForwardedForFromATrustedProxy(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.HTTP.TrustedProxies = []string{"203.0.113.0/24"}
	a := newTestApp(t, c)

	r, err := a.initEngine()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var gotClientIP string
	r.GET("/whoami", func(gc *gin.Context) {
		gotClientIP = gc.ClientIP()
		gc.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req.RemoteAddr = "203.0.113.10:1234"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.ServeHTTP(httptest.NewRecorder(), req)

	if gotClientIP != "1.2.3.4" {
		t.Errorf("got ClientIP %q, want 1.2.3.4 from the trusted proxy's header", gotClientIP)
	}
}
