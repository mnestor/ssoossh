package bootstrap

// Test methodology: Unit tests for router setup and middleware chain using
// httptest.ResponseRecorder to capture responses without a real listener.
// Tests run in parallel (t.Parallel()) and are fast. Each test verifies one
// specific routing behavior or middleware effect. See router_run_test.go for
// integration tests with real network listeners.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/logging"
	"github.com/mnestor/ssoossh/server/model"
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

// TestInitRouter_ShouldBindTheApprovalPageToItsFirstClient verifies the
// approval-claim middleware is actually wired into the engine: the first
// document GET of /approve/<id> sets the claim cookie, and a second client
// without it is redirected to the explanation page. The claim logic itself
// is unit tested in service and middleware; this pins the registration,
// which is the one thing those tests cannot see.
func TestInitRouter_ShouldBindTheApprovalPageToItsFirstClient(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	a := newTestApp(t, c)

	// A real CertRequestService over the same migrated DB; the wake-topic
	// broker is an unused throwaway (nothing waits on a claim).
	broker := gochannel.NewGoChannel(gochannel.Config{}, watermill.NopLogger{})
	t.Cleanup(func() { _ = broker.Close() })
	certRequests, err := service.NewCertRequestService(c, a.db, broker, broker)
	if err != nil {
		t.Fatalf("failed to build CertRequestService: %v", err)
	}
	a.svc.certRequest = certRequests

	srv, err := a.initRouter()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	seeded := model.CertificateRequest{
		ID:        "req-wired",
		Type:      model.CertificateTypeUser,
		Status:    model.CertificateRequestStatusPending,
		CreatedAt: time.Now().UTC(),
	}
	if err := a.db.Create(&seeded).Error; err != nil {
		t.Fatalf("failed to seed a certificate request: %v", err)
	}

	first := httptest.NewRecorder()
	firstReq := httptest.NewRequest(http.MethodGet, "/approve/req-wired", nil)
	firstReq.Header.Set("User-Agent", "Mozilla/5.0 (first)")
	srv.router.ServeHTTP(first, firstReq)

	claimed := false
	for _, ck := range first.Result().Cookies() {
		if ck.Name == "ssoossh_approval_claim" {
			claimed = true
		}
	}
	if !claimed {
		t.Fatal("expected the first GET of the approval page to set the claim cookie")
	}

	second := httptest.NewRecorder()
	secondReq := httptest.NewRequest(http.MethodGet, "/approve/req-wired", nil)
	secondReq.Header.Set("User-Agent", "Slackbot-LinkExpanding 1.0")
	srv.router.ServeHTTP(second, secondReq)

	if second.Code != http.StatusFound {
		t.Fatalf("got status %d for the second client, want %d", second.Code, http.StatusFound)
	}
	if got := second.Header().Get("Location"); got != "/approval-unavailable?reason=opened" {
		t.Errorf("got redirect %q, want the spent-link page", got)
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

// captureStdouterrForAccessLog swaps os.Stdout and os.Stderr for pipes, runs
// fn, and returns what each stream received. Mutates process globals, so
// callers must not run in parallel.
func captureStdouterrForAccessLog(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	fn()

	_ = outW.Close()
	_ = errW.Close()
	outB, _ := io.ReadAll(outR)
	errB, _ := io.ReadAll(errR)
	return string(outB), string(errB)
}

// TestInitRouter_ShouldLogRequestsByStatusClass drives real requests through
// the whole middleware chain and reads the log the way an operator does:
// off stdout, in a container, with the shipped logging.level of WARN.
//
// It covers both halves of what made the access log useless there. The
// access log needs a level of its own or nothing it emits clears WARN; and
// sloggin.NewWithConfig does no defaulting, so the Config literal has to set
// the three levels itself or every record -- a 500 included -- comes out at
// INFO, below WARN and never copied to stderr.
//
// Installs the process logger and swaps stdio; must not run in parallel.
func TestInitRouter_ShouldLogRequestsByStatusClass(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	c := &config.Config{}
	c.HTTP.RateLimit = 0
	c.HTTP.RateDuration = time.Minute
	// The shipped pairing: a quiet application log, a verbose access log,
	// and no filename anywhere, so stdout is the only destination.
	c.Logging.Level = "warn"
	c.HTTP.AccessLogging.Level = "info"

	// Everything inside the capture, and in this order: the middleware
	// binds its logger with logging.Tagged at initRouter time, so a logger
	// installed afterwards would never reach it -- which is why bootstrap
	// calls logging.New first. gin's own debug route dump does not land in
	// the pipes: gin.DefaultWriter captured the real os.Stdout at package
	// init, before this swap.
	stdout, stderr := captureStdouterrForAccessLog(t, func() {
		if _, err := logging.New(c); err != nil {
			t.Fatalf("logging.New() error = %v", err)
		}

		a := newTestApp(t, c)
		srv, err := a.initRouter()
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// Handlers that fail. Registered rather than found among the real
		// routes because the middleware chain is what is under test, not
		// the handlers: a real 500 needs a broken dependency to provoke,
		// and an unrouted path is not a 404 here at all -- the SPA
		// fallback serves index.html with a 200 for anything unmatched.
		srv.router.GET("/client-error", func(gc *gin.Context) { gc.Status(http.StatusBadRequest) })
		srv.router.GET("/boom", func(gc *gin.Context) { gc.Status(http.StatusInternalServerError) })

		// An INFO from the application itself, to show the two logs are
		// filtered independently rather than the level simply being lowered.
		slog.Info("app-info-marker")

		for _, path := range []string{"/api/ca", "/client-error", "/boom"} {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.RemoteAddr = "203.0.113.60:12345"
			srv.router.ServeHTTP(w, req)
		}
	})

	// Matched a line at a time, not as loose substrings of the whole
	// stream: a WRN appears in this capture whatever the access log does,
	// because initRouter warns about the unset cookie_secure. The claim is
	// that the record *for this request* carries that level.
	//
	// wantLevel is tint's three-letter token rather than slog TextHandler's
	// "level=INFO": both text destinations go through tint so that message
	// prose is not quoted and re-escaped (see logging.GetHandler).
	tests := []struct {
		name       string
		path       string
		wantLevel  string
		wantStderr bool
	}{
		{
			name:      "should log a served request at info",
			path:      "/api/ca",
			wantLevel: "INF",
		},
		{
			name:      "should log a client error at warn",
			path:      "/client-error",
			wantLevel: "WRN",
		},
		{
			name:       "should log a server error at error, and copy it to stderr",
			path:       "/boom",
			wantLevel:  "ERR",
			wantStderr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := lineContaining(stdout, "path="+tt.path)
			if line == "" {
				t.Fatalf("no access log record for %s on stdout; got:\n%s", tt.path, stdout)
			}
			if got := logLevelOf(line); got != tt.wantLevel {
				t.Errorf("access log for %s: want level %s, got %s in line:\n%s", tt.path, tt.wantLevel, got, line)
			}

			errLine := lineContaining(stderr, "path="+tt.path)
			if tt.wantStderr && errLine == "" {
				t.Errorf("expected %s to be copied to stderr as well; got:\n%s", tt.path, stderr)
			}
			if !tt.wantStderr && errLine != "" {
				t.Errorf("expected %s not to reach stderr, got line:\n%s", tt.path, errLine)
			}
		})
	}

	t.Run("should leave the application log at its own level", func(t *testing.T) {
		if strings.Contains(stdout, "app-info-marker") {
			t.Errorf("expected the application log to stay at warn; got:\n%s", stdout)
		}
	})
}

// logLevelOf returns the level token of one tint-formatted record: the
// second whitespace-separated field, after the timestamp. Read positionally
// rather than by substring because tint writes the level bare ("WRN", not
// "level=WARN"), and those three letters could otherwise be matched
// anywhere in the message or an attribute value.
func logLevelOf(line string) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// lineContaining returns the first line of s containing sub, or "" if none
// does. Access log assertions need the level and the path off the same
// record, not merely both somewhere in the stream.
func lineContaining(s, sub string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			return line
		}
	}
	return ""
}
