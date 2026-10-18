package middleware

// Test methodology: the NSS gate runs inside a real gin engine with the
// secret and error middleware in front of it, the way the router mounts it,
// against a fake service.NSSKeyVerifier and a recording
// service.NSSLookupObserver, so ordering, limits and what is observed are
// checked without a database. The controller tests drive the same gate
// with the real services.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// fakeNSSKey is one key the fake verifier knows: its secret, and why it is
// dead when it is.
type fakeNSSKey struct {
	secret string
	dead   string
}

// fakeNSSKeyVerifier answers VerifyFleetKey from a fixed set of keys and
// records every call, so a test can tell whether the gate reached it and
// with which secret.
type fakeNSSKeyVerifier struct {
	keys map[string]fakeNSSKey
	err  error

	mu      sync.Mutex
	calls   int
	secrets []string
}

// newFakeNSSKeyVerifier knows two live keys, a revoked one and an expired
// one.
func newFakeNSSKeyVerifier() *fakeNSSKeyVerifier {
	return &fakeNSSKeyVerifier{keys: map[string]fakeNSSKey{
		"nss_good":    {secret: "s-good"},
		"nss_other":   {secret: "s-other"},
		"nss_revoked": {secret: "s-revoked", dead: service.NSSDeadKeyRevoked},
		"nss_expired": {secret: "s-expired", dead: service.NSSDeadKeyExpired},
	}}
}

// VerifyFleetKey accepts a live key presented with its own secret.
func (f *fakeNSSKeyVerifier) VerifyFleetKey(_ context.Context, keyID, secret string) (service.NSSKeyVerdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.secrets = append(f.secrets, secret)
	if f.err != nil {
		return service.NSSKeyVerdict{}, f.err
	}
	verdict := service.NSSKeyVerdict{KeyID: keyID}
	key, ok := f.keys[keyID]
	if !ok {
		return verdict, nil
	}
	verdict.Key = &model.NSSKey{ID: keyID}
	verdict.Dead = key.dead
	if key.dead == "" && key.secret == secret {
		verdict.OK = true
		verdict.Generation = 1
	}
	return verdict, nil
}

// callCount is how many times the gate asked.
func (f *fakeNSSKeyVerifier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// recordingNSSObserver keeps every outcome the gate reports.
type recordingNSSObserver struct {
	mu       sync.Mutex
	outcomes []service.NSSLookupOutcome
}

// Observe records one outcome.
func (o *recordingNSSObserver) Observe(_ context.Context, outcome service.NSSLookupOutcome) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.outcomes = append(o.outcomes, outcome)
}

// all returns a copy of what was observed so far.
func (o *recordingNSSObserver) all() []service.NSSLookupOutcome {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]service.NSSLookupOutcome(nil), o.outcomes...)
}

// nssGateConfig is an enabled gate whose buckets are large enough never to
// get in a test's way.
func nssGateConfig() NSSFleetKeyConfig {
	return NSSFleetKeyConfig{
		Enabled:     true,
		FailedLimit: rate.Every(time.Minute),
		FailedBurst: 100,
		KeyLimit:    rate.Every(time.Minute),
		KeyBurst:    100,
	}
}

// newNSSGate builds the gate or fails the test.
func newNSSGate(t *testing.T, cfg NSSFleetKeyConfig, verifier service.NSSKeyVerifier, observer service.NSSLookupObserver) *NSSFleetKeyMiddleware {
	t.Helper()
	gate, err := NewNSSFleetKeyMiddleware(cfg, verifier, observer)
	if err != nil {
		t.Fatalf("NewNSSFleetKeyMiddleware: %v", err)
	}
	return gate
}

// nssHitHandler answers a lookup as served, echoing the verified key id.
func nssHitHandler(c *gin.Context) {
	SetNSSLookupResult(c, service.NSSResultHit, "")
	c.JSON(http.StatusOK, gin.H{"key_id": NSSVerifiedKeyID(c)})
}

// newNSSGateEngine mounts gate on the four lookup routes the way the
// lookup controller does, behind the secret and error middleware the
// router puts in front of every route, with handler answering each.
func newNSSGateEngine(gate *NSSFleetKeyMiddleware, handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(NewNSSKeySecretMiddleware().Add())
	r.Use(NewErrorHandlerMiddleware().Add())
	r.GET("/api/nss/user/name/*name", gate.Lookup(service.NSSKindUser, service.NSSByName), handler)
	r.GET("/api/nss/user/uid/*uid", gate.Lookup(service.NSSKindUser, service.NSSByID), handler)
	r.GET("/api/nss/group/name/*name", gate.Lookup(service.NSSKindGroup, service.NSSByName), handler)
	r.GET("/api/nss/group/gid/*gid", gate.Lookup(service.NSSKindGroup, service.NSSByID), handler)
	return r
}

// nssRequest is one lookup: the path, the key headers (left off when
// empty) and the connection's address (httptest's default when empty).
type nssRequest struct {
	path       string
	keyID      string
	secret     string
	remoteAddr string
	forwarded  string
}

// serveNSS sends req through r.
func serveNSS(r *gin.Engine, req nssRequest) *httptest.ResponseRecorder {
	path := req.path
	if path == "" {
		path = "/api/nss/user/name/alice"
	}
	httpReq := httptest.NewRequest(http.MethodGet, path, nil)
	if req.keyID != "" {
		httpReq.Header.Set(apitypes.HeaderNSSKeyID, req.keyID)
	}
	if req.secret != "" {
		httpReq.Header.Set(apitypes.HeaderNSSKeySecret, req.secret)
	}
	if req.remoteAddr != "" {
		httpReq.RemoteAddr = req.remoteAddr
	}
	if req.forwarded != "" {
		httpReq.Header.Set("X-Forwarded-For", req.forwarded)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httpReq)
	return w
}

// good is a lookup presenting the live key nss_good from remoteAddr.
func good(remoteAddr string) nssRequest {
	return nssRequest{keyID: "nss_good", secret: "s-good", remoteAddr: remoteAddr}
}

// nssErrorCode reads the envelope's error_code.
func nssErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var envelope struct {
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode envelope: %v, body: %s", err, body)
	}
	return envelope.ErrorCode
}

// assertNSSStatus fails unless w answered status with code, an empty code
// meaning the body is not inspected.
func assertNSSStatus(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d, body = %s", w.Code, status, w.Body.String())
	}
	if code != "" {
		if got := nssErrorCode(t, w.Body.Bytes()); got != code {
			t.Errorf("error_code = %q, want %q", got, code)
		}
	}
}

// assertNSSRateLimited fails unless w is a 429 nss_rate_limited carrying a
// whole-second Retry-After of at least one and the RateLimit-* headers of
// a bucket of burst.
func assertNSSRateLimited(t *testing.T, w *httptest.ResponseRecorder, burst int) {
	t.Helper()
	assertNSSStatus(t, w, http.StatusTooManyRequests, apitypes.ErrorCodeNSSRateLimited)
	retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
	if err != nil || retry < 1 {
		t.Errorf("Retry-After = %q, want an integer of at least 1", w.Header().Get("Retry-After"))
	}
	if got := w.Header().Get("RateLimit-Limit"); got != strconv.Itoa(burst) {
		t.Errorf("RateLimit-Limit = %q, want %d", got, burst)
	}
	if got := w.Header().Get("RateLimit-Remaining"); got != "0" {
		t.Errorf("RateLimit-Remaining = %q, want 0", got)
	}
	if reset, err := strconv.Atoi(w.Header().Get("RateLimit-Reset")); err != nil || reset < 1 {
		t.Errorf("RateLimit-Reset = %q, want an integer of at least 1", w.Header().Get("RateLimit-Reset"))
	}
}

func TestNSSKeySecretMiddleware(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		secrets    []string
		wantSecret string
	}{
		{name: "should move the secret into the context when one value is sent", secrets: []string{"s-one"}, wantSecret: "s-one"},
		{name: "should keep the first value and remove every value when several are sent", secrets: []string{"s-first", "s-second"}, wantSecret: "s-first"},
		{name: "should report an empty secret when the header is absent", secrets: nil, wantSecret: ""},
		{name: "should keep an empty value as an empty secret when the header is empty", secrets: []string{""}, wantSecret: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotSecret string
			var gotHeader http.Header
			r := gin.New()
			r.Use(NewNSSKeySecretMiddleware().Add())
			r.GET("/x", func(c *gin.Context) {
				gotSecret = NSSKeySecret(c)
				gotHeader = c.Request.Header.Clone()
				c.Status(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			for _, secret := range tc.secrets {
				req.Header.Add(apitypes.HeaderNSSKeySecret, secret)
			}
			req.Header.Set(apitypes.HeaderNSSKeyID, "nss_keep")
			req.Header.Set("X-Other", "kept")
			r.ServeHTTP(httptest.NewRecorder(), req)

			if gotSecret != tc.wantSecret {
				t.Errorf("NSSKeySecret = %q, want %q", gotSecret, tc.wantSecret)
			}
			if values := gotHeader.Values(apitypes.HeaderNSSKeySecret); len(values) != 0 {
				t.Errorf("secret header still on the request: %q", values)
			}
			if got := gotHeader.Get(apitypes.HeaderNSSKeyID); got != "nss_keep" {
				t.Errorf("key id header = %q, want it untouched", got)
			}
			if got := gotHeader.Get("X-Other"); got != "kept" {
				t.Errorf("X-Other = %q, want it untouched", got)
			}
		})
	}
}

func TestNSSKeySecret_ShouldBeEmptyWhenTheMiddlewareDidNotRun(t *testing.T) {
	t.Parallel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	c.Request.Header.Set(apitypes.HeaderNSSKeySecret, "s-on-the-header")

	if got := NSSKeySecret(c); got != "" {
		t.Errorf("NSSKeySecret = %q, want empty: only the middleware puts a secret in the context", got)
	}
}

func TestNewNSSFleetKeyMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		allow   []string
		wantErr string
	}{
		{name: "should accept an empty allow list when none is configured", allow: nil},
		{name: "should accept IPv4 and IPv6 prefixes when both are configured", allow: []string{"10.0.0.0/8", "2001:db8::/32"}},
		{name: "should accept a prefix with host bits set when it is configured", allow: []string{"10.1.2.3/8"}},
		{name: "should name the entry when one does not parse", allow: []string{"10.0.0.0/8", "not-a-cidr"}, wantErr: "nss.source_allow_list[1]"},
		{name: "should refuse a bare address when no prefix length is given", allow: []string{"10.0.0.1"}, wantErr: "nss.source_allow_list[0]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssGateConfig()
			cfg.SourceAllowList = tc.allow
			_, err := NewNSSFleetKeyMiddleware(cfg, newFakeNSSKeyVerifier(), &recordingNSSObserver{})
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("error = %v, want one naming %q", err, tc.wantErr)
			}
		})
	}
}

func TestNSSFleetKeyMiddleware_ShouldAnswerDisabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		enabled  bool
		verifier bool
		observer bool
	}{
		{name: "should answer nss_disabled when nss.enabled is false", enabled: false, verifier: true, observer: true},
		{name: "should answer nss_disabled when enabled without a verifier", enabled: true, verifier: false, observer: true},
		{name: "should answer nss_disabled when enabled without an observer", enabled: true, verifier: true, observer: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeNSSKeyVerifier()
			recorder := &recordingNSSObserver{}
			var verifier service.NSSKeyVerifier
			var observer service.NSSLookupObserver
			if tc.verifier {
				verifier = fake
			}
			if tc.observer {
				observer = recorder
			}
			cfg := nssGateConfig()
			cfg.Enabled = tc.enabled
			r := newNSSGateEngine(newNSSGate(t, cfg, verifier, observer), nssHitHandler)

			for _, path := range []string{"/api/nss/user/name/alice", "/api/nss/user/uid/1", "/api/nss/group/name/devs", "/api/nss/group/gid/1"} {
				w := serveNSS(r, nssRequest{path: path, keyID: "nss_good", secret: "s-good"})
				assertNSSStatus(t, w, http.StatusNotFound, apitypes.ErrorCodeNSSDisabled)
			}
			if got := fake.callCount(); got != 0 {
				t.Errorf("verifier called %d times, want none", got)
			}
			if got := recorder.all(); len(got) != 0 {
				t.Errorf("observed %d outcomes, want none", len(got))
			}
		})
	}
}

func TestNSSFleetKeyMiddleware_SourceAllowList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		allow      []string
		remoteAddr string
		forwarded  string
		wantStatus int
		wantCode   string
		wantCalls  int
	}{
		{
			name:       "should refuse before the key is checked when the address is outside the list",
			allow:      []string{"10.0.0.0/8"},
			remoteAddr: "192.0.2.1:1234",
			wantStatus: http.StatusForbidden,
			wantCode:   apitypes.ErrorCodeNSSForbidden,
			wantCalls:  0,
		},
		{
			name:       "should pass on to the key check when the address is inside the list",
			allow:      []string{"10.0.0.0/8"},
			remoteAddr: "10.1.2.3:1234",
			wantStatus: http.StatusOK,
			wantCalls:  1,
		},
		{
			name:       "should allow every address when the list is empty",
			allow:      nil,
			remoteAddr: "198.51.100.200:1234",
			wantStatus: http.StatusOK,
			wantCalls:  1,
		},
		{
			name:       "should match the IPv4 prefix when the address is IPv4-mapped IPv6",
			allow:      []string{"10.0.0.0/8"},
			remoteAddr: "192.0.2.1:1234",
			forwarded:  "::ffff:10.1.2.3",
			wantStatus: http.StatusOK,
			wantCalls:  1,
		},
		{
			name:       "should match an IPv6 prefix when the address is IPv6",
			allow:      []string{"2001:db8::/32"},
			remoteAddr: "[2001:db8::5]:1234",
			wantStatus: http.StatusOK,
			wantCalls:  1,
		},
		{
			name:       "should refuse an IPv4 address when only an IPv6 prefix is listed",
			allow:      []string{"2001:db8::/32"},
			remoteAddr: "10.1.2.3:1234",
			wantStatus: http.StatusForbidden,
			wantCode:   apitypes.ErrorCodeNSSForbidden,
		},
		{
			name:       "should refuse when the address cannot be read and a list is set",
			allow:      []string{"10.0.0.0/8"},
			remoteAddr: "no-port-here",
			wantStatus: http.StatusForbidden,
			wantCode:   apitypes.ErrorCodeNSSForbidden,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFakeNSSKeyVerifier()
			cfg := nssGateConfig()
			cfg.SourceAllowList = tc.allow
			r := newNSSGateEngine(newNSSGate(t, cfg, fake, &recordingNSSObserver{}), nssHitHandler)

			req := good(tc.remoteAddr)
			req.forwarded = tc.forwarded
			w := serveNSS(r, req)

			assertNSSStatus(t, w, tc.wantStatus, tc.wantCode)
			if got := fake.callCount(); got != tc.wantCalls {
				t.Errorf("verifier called %d times, want %d", got, tc.wantCalls)
			}
		})
	}
}

func TestNSSFleetKeyMiddleware_ShouldRefuseEveryBadKeyWithOneBody(t *testing.T) {
	t.Parallel()

	r := newNSSGateEngine(newNSSGate(t, nssGateConfig(), newFakeNSSKeyVerifier(), &recordingNSSObserver{}), nssHitHandler)

	tests := []struct {
		name string
		req  nssRequest
	}{
		{name: "should refuse when no key headers are sent", req: nssRequest{}},
		{name: "should refuse when the key id is unknown", req: nssRequest{keyID: "nss_nobody", secret: "s-good"}},
		{name: "should refuse when the key is revoked", req: nssRequest{keyID: "nss_revoked", secret: "s-revoked"}},
		{name: "should refuse when the key is expired", req: nssRequest{keyID: "nss_expired", secret: "s-expired"}},
		{name: "should refuse when the secret is wrong", req: nssRequest{keyID: "nss_good", secret: "s-wrong"}},
		{name: "should refuse when another key's secret is sent", req: nssRequest{keyID: "nss_good", secret: "s-other"}},
		{name: "should refuse when only the id is sent", req: nssRequest{keyID: "nss_good"}},
	}
	var first string
	for i, tc := range tests {
		w := serveNSS(r, tc.req)
		assertNSSStatus(t, w, http.StatusUnauthorized, apitypes.ErrorCodeNSSUnauthorized)
		body := w.Body.String()
		if i == 0 {
			first = body
			continue
		}
		if body != first {
			t.Errorf("%s: body = %s, want it byte-identical to %s", tc.name, body, first)
		}
		if tc.req.secret != "" && strings.Contains(body, tc.req.secret) {
			t.Errorf("%s: body carries the presented secret: %s", tc.name, body)
		}
	}
}

func TestNSSFleetKeyMiddleware_ShouldHideTheVerifierErrorBehindA500(t *testing.T) {
	t.Parallel()

	fake := newFakeNSSKeyVerifier()
	fake.err = errors.New("read fleet key nss_good with s-good: database is down")
	recorder := &recordingNSSObserver{}
	r := newNSSGateEngine(newNSSGate(t, nssGateConfig(), fake, recorder), nssHitHandler)

	w := serveNSS(r, good(""))

	assertNSSStatus(t, w, http.StatusInternalServerError, apitypes.ErrorCodeInternalError)
	for _, leak := range []string{"nss_good", "s-good", "database"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("body carries %q: %s", leak, w.Body.String())
		}
	}
	if got := recorder.all(); len(got) != 1 || got[0].Result != service.NSSResultError {
		t.Errorf("observed %+v, want one outcome with result error", got)
	}
}

func TestNSSFleetKeyMiddleware_ShouldPassTheSecretFromTheContext(t *testing.T) {
	t.Parallel()

	fake := newFakeNSSKeyVerifier()
	var handlerSaw []string
	r := newNSSGateEngine(newNSSGate(t, nssGateConfig(), fake, &recordingNSSObserver{}), func(c *gin.Context) {
		handlerSaw = c.Request.Header.Values(apitypes.HeaderNSSKeySecret)
		nssHitHandler(c)
	})

	w := serveNSS(r, good(""))

	assertNSSStatus(t, w, http.StatusOK, "")
	if len(fake.secrets) != 1 || fake.secrets[0] != "s-good" {
		t.Errorf("verifier got secrets %q, want [s-good]", fake.secrets)
	}
	if len(handlerSaw) != 0 {
		t.Errorf("handler still saw the secret header: %q", handlerSaw)
	}
}

func TestNSSFleetKeyMiddleware_FailedKeyBucket(t *testing.T) {
	t.Parallel()

	t.Run("should limit refused keys per address whatever key id they present", func(t *testing.T) {
		t.Parallel()

		cfg := nssGateConfig()
		cfg.FailedBurst = 2
		recorder := &recordingNSSObserver{}
		r := newNSSGateEngine(newNSSGate(t, cfg, newFakeNSSKeyVerifier(), recorder), nssHitHandler)

		addr := "203.0.113.10:1111"
		assertNSSStatus(t, serveNSS(r, nssRequest{keyID: "nss_a", secret: "x", remoteAddr: addr}), http.StatusUnauthorized, apitypes.ErrorCodeNSSUnauthorized)
		assertNSSStatus(t, serveNSS(r, nssRequest{keyID: "nss_b", secret: "x", remoteAddr: addr}), http.StatusUnauthorized, apitypes.ErrorCodeNSSUnauthorized)
		assertNSSRateLimited(t, serveNSS(r, nssRequest{keyID: "nss_c", secret: "x", remoteAddr: addr}), 2)

		outcomes := recorder.all()
		if last := outcomes[len(outcomes)-1]; last.Result != service.NSSResultRateLimited {
			t.Errorf("last result = %q, want %q", last.Result, service.NSSResultRateLimited)
		}
	})

	t.Run("should keep a separate failed bucket when the address differs", func(t *testing.T) {
		t.Parallel()

		cfg := nssGateConfig()
		cfg.FailedBurst = 1
		r := newNSSGateEngine(newNSSGate(t, cfg, newFakeNSSKeyVerifier(), &recordingNSSObserver{}), nssHitHandler)

		assertNSSStatus(t, serveNSS(r, nssRequest{keyID: "nss_a", remoteAddr: "203.0.113.11:1"}), http.StatusUnauthorized, "")
		assertNSSRateLimited(t, serveNSS(r, nssRequest{keyID: "nss_a", remoteAddr: "203.0.113.11:1"}), 1)
		assertNSSStatus(t, serveNSS(r, nssRequest{keyID: "nss_a", remoteAddr: "203.0.113.12:1"}), http.StatusUnauthorized, apitypes.ErrorCodeNSSUnauthorized)
	})

	t.Run("should not spend the failed bucket when the key is accepted", func(t *testing.T) {
		t.Parallel()

		cfg := nssGateConfig()
		cfg.FailedBurst = 1
		r := newNSSGateEngine(newNSSGate(t, cfg, newFakeNSSKeyVerifier(), &recordingNSSObserver{}), nssHitHandler)

		addr := "203.0.113.13:1"
		assertNSSStatus(t, serveNSS(r, good(addr)), http.StatusOK, "")
		assertNSSStatus(t, serveNSS(r, good(addr)), http.StatusOK, "")
		assertNSSStatus(t, serveNSS(r, nssRequest{keyID: "nss_a", remoteAddr: addr}), http.StatusUnauthorized, apitypes.ErrorCodeNSSUnauthorized)
	})

	t.Run("should never limit refused keys when the failed burst is zero", func(t *testing.T) {
		t.Parallel()

		cfg := nssGateConfig()
		cfg.FailedBurst = 0
		r := newNSSGateEngine(newNSSGate(t, cfg, newFakeNSSKeyVerifier(), &recordingNSSObserver{}), nssHitHandler)

		for i := range 10 {
			w := serveNSS(r, nssRequest{keyID: "nss_a", secret: "x", remoteAddr: "203.0.113.14:1"})
			assertNSSStatus(t, w, http.StatusUnauthorized, apitypes.ErrorCodeNSSUnauthorized)
			if got := w.Header().Get("RateLimit-Limit"); got != "" {
				t.Fatalf("request %d: RateLimit-Limit = %q, want none without a failed bucket", i, got)
			}
		}
	})
}

func TestNSSFleetKeyMiddleware_KeyBucket(t *testing.T) {
	t.Parallel()

	t.Run("should limit an accepted key once its bucket is drained", func(t *testing.T) {
		t.Parallel()

		cfg := nssGateConfig()
		cfg.KeyBurst = 2
		r := newNSSGateEngine(newNSSGate(t, cfg, newFakeNSSKeyVerifier(), &recordingNSSObserver{}), nssHitHandler)

		addr := "203.0.113.20:1"
		assertNSSStatus(t, serveNSS(r, good(addr)), http.StatusOK, "")
		assertNSSStatus(t, serveNSS(r, good(addr)), http.StatusOK, "")
		assertNSSRateLimited(t, serveNSS(r, good(addr)), 2)
	})

	t.Run("should advertise the key bucket when a lookup is served", func(t *testing.T) {
		t.Parallel()

		cfg := nssGateConfig()
		cfg.KeyBurst = 5
		r := newNSSGateEngine(newNSSGate(t, cfg, newFakeNSSKeyVerifier(), &recordingNSSObserver{}), nssHitHandler)

		w := serveNSS(r, good("203.0.113.21:1"))
		assertNSSStatus(t, w, http.StatusOK, "")
		if got := w.Header().Get("RateLimit-Limit"); got != "5" {
			t.Errorf("RateLimit-Limit = %q, want 5", got)
		}
		if got := w.Header().Get("RateLimit-Remaining"); got != "4" {
			t.Errorf("RateLimit-Remaining = %q, want 4", got)
		}
		if got := w.Header().Get("Retry-After"); got != "" {
			t.Errorf("Retry-After = %q, want none on a served lookup", got)
		}
	})

	t.Run("should keep separate buckets when two keys share an address", func(t *testing.T) {
		t.Parallel()

		cfg := nssGateConfig()
		cfg.KeyBurst = 1
		r := newNSSGateEngine(newNSSGate(t, cfg, newFakeNSSKeyVerifier(), &recordingNSSObserver{}), nssHitHandler)

		addr := "203.0.113.22:1"
		other := nssRequest{keyID: "nss_other", secret: "s-other", remoteAddr: addr}
		assertNSSStatus(t, serveNSS(r, good(addr)), http.StatusOK, "")
		assertNSSStatus(t, serveNSS(r, other), http.StatusOK, "")
		assertNSSRateLimited(t, serveNSS(r, good(addr)), 1)
		assertNSSRateLimited(t, serveNSS(r, other), 1)
	})

	t.Run("should keep separate buckets when one key is used from two addresses", func(t *testing.T) {
		t.Parallel()

		cfg := nssGateConfig()
		cfg.KeyBurst = 1
		r := newNSSGateEngine(newNSSGate(t, cfg, newFakeNSSKeyVerifier(), &recordingNSSObserver{}), nssHitHandler)

		assertNSSStatus(t, serveNSS(r, good("203.0.113.23:1")), http.StatusOK, "")
		assertNSSStatus(t, serveNSS(r, good("203.0.113.24:1")), http.StatusOK, "")
		assertNSSRateLimited(t, serveNSS(r, good("203.0.113.23:1")), 1)
	})
}

func TestNSSFleetKeyMiddleware_ForwardedFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		trusted      []string
		wantStatuses []int
		wantSources  []string
	}{
		{
			name:         "should bucket by the connection when the peer is not a trusted proxy",
			trusted:      nil,
			wantStatuses: []int{http.StatusOK, http.StatusTooManyRequests, http.StatusTooManyRequests},
			wantSources:  []string{"198.51.100.7", "198.51.100.7", "198.51.100.7"},
		},
		{
			name:         "should bucket by the forwarded address when the peer is a trusted proxy",
			trusted:      []string{"198.51.100.7"},
			wantStatuses: []int{http.StatusOK, http.StatusOK, http.StatusTooManyRequests},
			wantSources:  []string{"203.0.113.1", "203.0.113.2", "203.0.113.1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssGateConfig()
			cfg.KeyBurst = 1
			recorder := &recordingNSSObserver{}
			r := newNSSGateEngine(newNSSGate(t, cfg, newFakeNSSKeyVerifier(), recorder), nssHitHandler)
			if err := r.SetTrustedProxies(tc.trusted); err != nil {
				t.Fatalf("SetTrustedProxies: %v", err)
			}

			for i, forwarded := range []string{"203.0.113.1", "203.0.113.2", "203.0.113.1"} {
				req := good("198.51.100.7:4444")
				req.forwarded = forwarded
				if w := serveNSS(r, req); w.Code != tc.wantStatuses[i] {
					t.Errorf("request %d: status = %d, want %d", i, w.Code, tc.wantStatuses[i])
				}
			}
			outcomes := recorder.all()
			if len(outcomes) != len(tc.wantSources) {
				t.Fatalf("observed %d outcomes, want %d", len(outcomes), len(tc.wantSources))
			}
			for i, outcome := range outcomes {
				if outcome.SourceIP != tc.wantSources[i] {
					t.Errorf("request %d: source = %q, want %q", i, outcome.SourceIP, tc.wantSources[i])
				}
			}
		})
	}
}

func TestNSSFleetKeyMiddleware_ShouldObserveEachLookupOnce(t *testing.T) {
	t.Parallel()

	silent := func(c *gin.Context) { c.Status(http.StatusNoContent) }
	miss := func(c *gin.Context) {
		SetNSSLookupResult(c, service.NSSResultMiss, "alice")
		c.Status(http.StatusNotFound)
	}

	tests := []struct {
		name       string
		cfg        func(*NSSFleetKeyConfig)
		verifyErr  error
		handler    gin.HandlerFunc
		req        nssRequest
		wantKind   string
		wantBy     string
		wantResult string
		wantName   string
		wantOK     bool
	}{
		{
			name:       "should observe a hit when the handler reports one",
			handler:    nssHitHandler,
			req:        nssRequest{path: "/api/nss/user/name/alice", keyID: "nss_good", secret: "s-good", remoteAddr: "203.0.113.30:1"},
			wantKind:   service.NSSKindUser,
			wantBy:     service.NSSByName,
			wantResult: service.NSSResultHit,
			wantOK:     true,
		},
		{
			name:       "should observe the result and name when the handler reports a miss",
			handler:    miss,
			req:        nssRequest{path: "/api/nss/user/uid/1500", keyID: "nss_good", secret: "s-good", remoteAddr: "203.0.113.31:1"},
			wantKind:   service.NSSKindUser,
			wantBy:     service.NSSByID,
			wantResult: service.NSSResultMiss,
			wantName:   "alice",
			wantOK:     true,
		},
		{
			name:       "should observe an error when the handler reports nothing",
			handler:    silent,
			req:        nssRequest{path: "/api/nss/group/name/devs", keyID: "nss_good", secret: "s-good", remoteAddr: "203.0.113.32:1"},
			wantKind:   service.NSSKindGroup,
			wantBy:     service.NSSByName,
			wantResult: service.NSSResultError,
			wantOK:     true,
		},
		{
			name:       "should observe unauthorized when the key is refused",
			handler:    nssHitHandler,
			req:        nssRequest{path: "/api/nss/group/gid/27", keyID: "nss_revoked", secret: "s-revoked", remoteAddr: "203.0.113.33:1"},
			wantKind:   service.NSSKindGroup,
			wantBy:     service.NSSByID,
			wantResult: service.NSSResultUnauthorized,
		},
		{
			name:       "should observe forbidden when the address is refused",
			cfg:        func(c *NSSFleetKeyConfig) { c.SourceAllowList = []string{"10.0.0.0/8"} },
			handler:    nssHitHandler,
			req:        nssRequest{path: "/api/nss/user/name/alice", keyID: "nss_good", secret: "s-good", remoteAddr: "203.0.113.34:1"},
			wantKind:   service.NSSKindUser,
			wantBy:     service.NSSByName,
			wantResult: service.NSSResultForbidden,
		},
		{
			name:       "should observe rate_limited when the key bucket is empty",
			cfg:        func(c *NSSFleetKeyConfig) { c.KeyBurst = 0 },
			handler:    nssHitHandler,
			req:        nssRequest{path: "/api/nss/user/name/alice", keyID: "nss_good", secret: "s-good", remoteAddr: "203.0.113.35:1"},
			wantKind:   service.NSSKindUser,
			wantBy:     service.NSSByName,
			wantResult: service.NSSResultRateLimited,
			wantOK:     true,
		},
		{
			name:       "should observe an error when the verifier fails",
			verifyErr:  errors.New("database is down"),
			handler:    nssHitHandler,
			req:        nssRequest{path: "/api/nss/user/name/alice", keyID: "nss_good", secret: "s-good", remoteAddr: "203.0.113.36:1"},
			wantKind:   service.NSSKindUser,
			wantBy:     service.NSSByName,
			wantResult: service.NSSResultError,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssGateConfig()
			if tc.cfg != nil {
				tc.cfg(&cfg)
			}
			fake := newFakeNSSKeyVerifier()
			fake.err = tc.verifyErr
			recorder := &recordingNSSObserver{}
			r := newNSSGateEngine(newNSSGate(t, cfg, fake, recorder), tc.handler)

			serveNSS(r, tc.req)

			outcomes := recorder.all()
			if len(outcomes) != 1 {
				t.Fatalf("observed %d outcomes, want exactly one", len(outcomes))
			}
			got := outcomes[0]
			wantSource, _, _ := strings.Cut(tc.req.remoteAddr, ":")
			if got.Kind != tc.wantKind || got.By != tc.wantBy || got.Result != tc.wantResult || got.Name != tc.wantName {
				t.Errorf("kind/by/result/name = %q/%q/%q/%q, want %q/%q/%q/%q",
					got.Kind, got.By, got.Result, got.Name, tc.wantKind, tc.wantBy, tc.wantResult, tc.wantName)
			}
			if got.KeyID != tc.req.keyID {
				t.Errorf("key id = %q, want the presented %q", got.KeyID, tc.req.keyID)
			}
			if got.SourceIP != wantSource {
				t.Errorf("source = %q, want %q", got.SourceIP, wantSource)
			}
			if got.Verdict.OK != tc.wantOK {
				t.Errorf("verdict ok = %v, want %v", got.Verdict.OK, tc.wantOK)
			}
			if got.Duration < 0 {
				t.Errorf("duration = %v, want non-negative", got.Duration)
			}
		})
	}
}

func TestNSSLookupResult_ShouldRoundTripThroughTheGate(t *testing.T) {
	t.Parallel()

	recorder := &recordingNSSObserver{}
	r := newNSSGateEngine(newNSSGate(t, nssGateConfig(), newFakeNSSKeyVerifier(), recorder), func(c *gin.Context) {
		SetNSSLookupResult(c, service.NSSResultBadRequest, "first")
		SetNSSLookupResult(c, service.NSSResultMiss, "bob")
		c.JSON(http.StatusOK, gin.H{"key_id": NSSVerifiedKeyID(c)})
	})

	w := serveNSS(r, good(""))

	var body struct {
		KeyID string `json:"key_id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v, body: %s", err, w.Body.String())
	}
	if body.KeyID != "nss_good" {
		t.Errorf("NSSVerifiedKeyID = %q, want nss_good", body.KeyID)
	}
	outcomes := recorder.all()
	if len(outcomes) != 1 || outcomes[0].Result != service.NSSResultMiss || outcomes[0].Name != "bob" {
		t.Errorf("observed %+v, want one miss for bob, the last value set", outcomes)
	}
}

func TestNSSLookupResult_ShouldDoNothingOutsideTheGate(t *testing.T) {
	t.Parallel()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)

	SetNSSLookupResult(c, service.NSSResultHit, "alice")

	if got := NSSVerifiedKeyID(c); got != "" {
		t.Errorf("NSSVerifiedKeyID = %q, want empty outside the gate", got)
	}
}

func TestNSSRetryAfterSeconds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		limiter func() *rate.Limiter
		want    int
	}{
		{
			name:    "should be one when a token is already there",
			limiter: func() *rate.Limiter { return rate.NewLimiter(rate.Every(time.Minute), 1) },
			want:    1,
		},
		{
			name: "should be the refill interval when the bucket was just drained",
			limiter: func() *rate.Limiter {
				l := rate.NewLimiter(rate.Every(time.Minute), 1)
				l.Allow()
				return l
			},
			want: 60,
		},
		{
			name: "should round up to the next whole second when the bucket is part refilled",
			limiter: func() *rate.Limiter {
				l := rate.NewLimiter(rate.Every(time.Minute), 1)
				l.AllowN(time.Now().Add(-30*time.Second), 1)
				return l
			},
			want: 30,
		},
		{
			name: "should be one when the wait is under a second",
			limiter: func() *rate.Limiter {
				l := rate.NewLimiter(10, 1)
				l.Allow()
				return l
			},
			want: 1,
		},
		{
			name: "should be two when the bucket refills every two seconds",
			limiter: func() *rate.Limiter {
				l := rate.NewLimiter(0.5, 3)
				l.AllowN(time.Now(), 3)
				return l
			},
			want: 2,
		},
		{
			name: "should be one when the bucket never refills",
			limiter: func() *rate.Limiter {
				l := rate.NewLimiter(0, 1)
				l.Allow()
				return l
			},
			want: 1,
		},
		{
			name:    "should be one when the burst is zero and the rate infinite",
			limiter: func() *rate.Limiter { return rate.NewLimiter(rate.Inf, 0) },
			want:    1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := retryAfterSeconds(tc.limiter()); got != tc.want {
				t.Errorf("retryAfterSeconds = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNSSLookupPathPrefixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path string
		want bool
	}{
		{path: "/api/nss/user/name/alice", want: true},
		{path: "/api/nss/user/uid/1500", want: true},
		{path: "/api/nss/group/name/devs", want: true},
		{path: "/api/nss/group/gid/27", want: true},
		{path: "/api/nss/keys", want: false},
		{path: "/api/nss/keys/nss_abc/rotate", want: false},
		{path: "/api/nss/shells", want: false},
		{path: "/api/nss/user", want: false},
		{path: "/api/users/me", want: false},
	}
	for _, tc := range tests {
		t.Run("should match "+strconv.FormatBool(tc.want)+" when the path is "+tc.path, func(t *testing.T) {
			t.Parallel()

			got := false
			for _, prefix := range NSSLookupPathPrefixes() {
				if strings.HasPrefix(tc.path, prefix) {
					got = true
				}
			}
			if got != tc.want {
				t.Errorf("a lookup prefix matches %q: %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
