package middleware

// Test methodology: Unit tests for rate limiting middleware. Tests run in
// parallel (t.Parallel()). Verifies per-IP rate limiting, burst handling,
// and localhost exemption. Uses helper function newTestRequest to build
// gin.Context objects. Each test verifies one specific rate limit behavior.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// newTestRequest builds a gin.Context/ResponseRecorder pair for a GET
// request from remoteAddr.
func newTestRequest(remoteAddr string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.RemoteAddr = remoteAddr
	return c, w
}

func TestRateLimitMiddleware_ShouldAllowRequestsWithinBurst(t *testing.T) {
	t.Parallel()

	// Add() spawns a background cleanup goroutine; call it once per test
	// function and reuse the handler across assertions to avoid leaking
	// goroutines across the suite.
	handler := NewRateLimitMiddleware().Add(rate.Every(time.Minute), 2)

	c, w := newTestRequest("203.0.113.10:1111")
	handler(c)

	if c.IsAborted() {
		t.Fatal("expected first request within burst to not be aborted")
	}
	if w.Code != http.StatusOK {
		// gin.CreateTestContext defaults to 200 unless changed
		t.Errorf("got status %d, want default 200 (not aborted)", w.Code)
	}
}

func TestRateLimitMiddleware_ShouldRejectRequestsBeyondBurst(t *testing.T) {
	t.Parallel()

	handler := NewRateLimitMiddleware().Add(rate.Every(time.Minute), 1)

	ip := "203.0.113.20:1111"

	// First request consumes the only burst token.
	c1, _ := newTestRequest(ip)
	handler(c1)
	if c1.IsAborted() {
		t.Fatal("expected first request to be allowed")
	}

	// Second request immediately after should exceed the burst of 1.
	c2, _ := newTestRequest(ip)
	handler(c2)

	if !c2.IsAborted() {
		t.Fatal("expected second request beyond burst to be aborted")
	}
	if len(c2.Errors) != 1 {
		t.Fatalf("expected exactly one error to be attached, got %d", len(c2.Errors))
	}
	tooManyRequestsError := &errorresponses.TooManyRequestsError{}
	if !errors.As(c2.Errors[0].Err, &tooManyRequestsError) {
		t.Errorf("expected TooManyRequestsError, got %T", c2.Errors[0].Err)
	}
}

func TestRateLimitMiddleware_ShouldBypassLimitForLocalhostIPv4(t *testing.T) {
	t.Parallel()

	// Burst of 0 would reject any non-bypassed request immediately, so this
	// also proves the bypass path is taken rather than merely having spare
	// capacity.
	handler := NewRateLimitMiddleware().Add(rate.Every(time.Minute), 0)

	c, _ := newTestRequest("127.0.0.1:1111")
	handler(c)

	if c.IsAborted() {
		t.Error("expected localhost (127.0.0.1) requests to bypass the rate limit")
	}
}

func TestRateLimitMiddleware_ShouldBypassLimitForLocalhostIPv6(t *testing.T) {
	t.Parallel()

	handler := NewRateLimitMiddleware().Add(rate.Every(time.Minute), 0)

	c, _ := newTestRequest("[::1]:1111")
	handler(c)

	if c.IsAborted() {
		t.Error("expected localhost (::1) requests to bypass the rate limit")
	}
}

func TestEvictStaleClients_ShouldDeleteEntriesOlderThanMaxAge(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	clients := map[string]*client{
		"stale":     {limiter: rate.NewLimiter(1, 1), lastSeen: time.Now().Add(-5 * time.Minute)},
		"fresh":     {limiter: rate.NewLimiter(1, 1), lastSeen: time.Now()},
		"exactlyOK": {limiter: rate.NewLimiter(1, 1), lastSeen: time.Now().Add(-1 * time.Minute)},
	}

	evictStaleClients(&mu, clients, 3*time.Minute)

	if _, exists := clients["stale"]; exists {
		t.Error("expected stale client to be evicted")
	}
	if _, exists := clients["fresh"]; !exists {
		t.Error("expected fresh client to remain")
	}
	if _, exists := clients["exactlyOK"]; !exists {
		t.Error("expected client within maxAge to remain")
	}
}

func TestEvictStaleClients_ShouldNoOpOnEmptyMap(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	clients := map[string]*client{}

	evictStaleClients(&mu, clients, 3*time.Minute)

	if len(clients) != 0 {
		t.Errorf("expected empty map to remain empty, got %d entries", len(clients))
	}
}

// TestRateLimitMiddleware_ShouldAdvertiseTheRemainingBudget covers the
// rate-limit headers .claude/rules/server-api.md requires on every route.
// They are set on the rejected response too, since a client that just got a
// 429 is the one that most needs to know when to retry.
func TestRateLimitMiddleware_ShouldAdvertiseTheRemainingBudget(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(NewRateLimitMiddleware().Add(rate.Every(time.Minute), 1))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	send := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "203.0.113.77:1234"
		r.ServeHTTP(w, req)
		return w
	}

	first := send()
	if got := first.Header().Get("RateLimit-Limit"); got != "1" {
		t.Errorf("got RateLimit-Limit %q, want %q", got, "1")
	}
	if got := first.Header().Get("RateLimit-Remaining"); got != "0" {
		t.Errorf("got RateLimit-Remaining %q, want %q after consuming the burst", got, "0")
	}

	second := send()
	if second.Code != http.StatusTooManyRequests && len(second.Header().Get("RateLimit-Reset")) == 0 {
		t.Error("expected the rejected response to still advertise RateLimit-Reset")
	}
	if got := second.Header().Get("RateLimit-Reset"); got == "" || got == "0" {
		t.Errorf("got RateLimit-Reset %q, want a positive retry hint once the budget is exhausted", got)
	}
}
