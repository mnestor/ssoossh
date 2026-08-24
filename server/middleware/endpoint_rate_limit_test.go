package middleware

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

func TestEndpointRateLimiter_PerIP_ShouldAllowRequestsWithinBurst(t *testing.T) {
	t.Parallel()

	handler := NewEndpointRateLimiter().PerIP(rate.Every(time.Minute), 2)

	c, w := newTestRequest("203.0.113.10:1111")
	handler(c)

	if c.IsAborted() {
		t.Fatal("expected first request within burst to not be aborted")
	}
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want default 200 (not aborted)", w.Code)
	}
}

func TestEndpointRateLimiter_PerIP_ShouldRejectRequestsBeyondBurst(t *testing.T) {
	t.Parallel()

	handler := NewEndpointRateLimiter().PerIP(rate.Every(time.Minute), 1)

	ip := "203.0.113.20:1111"

	// First request consumes the burst token
	c1, _ := newTestRequest(ip)
	handler(c1)
	if c1.IsAborted() {
		t.Fatal("expected first request to be allowed")
	}

	// Second request immediately after should exceed burst of 1
	c2, _ := newTestRequest(ip)
	handler(c2)

	if !c2.IsAborted() {
		t.Fatal("expected second request beyond burst to be aborted")
	}
	if len(c2.Errors) != 1 {
		t.Fatalf("expected exactly one error, got %d", len(c2.Errors))
	}
	tooManyRequestsError := &errorresponses.TooManyRequestsError{}
	if !errors.As(c2.Errors[0].Err, &tooManyRequestsError) {
		t.Errorf("expected TooManyRequestsError, got %T", c2.Errors[0].Err)
	}
}

func TestEndpointRateLimiter_PerIP_ShouldIsolateLimitsPerIP(t *testing.T) {
	t.Parallel()

	handler := NewEndpointRateLimiter().PerIP(rate.Every(time.Minute), 1)

	// First IP uses its burst token
	c1, _ := newTestRequest("203.0.113.30:1111")
	handler(c1)
	if c1.IsAborted() {
		t.Fatal("first IP first request should not be aborted")
	}

	// First IP's second request should be rejected
	c2, _ := newTestRequest("203.0.113.30:1111")
	handler(c2)
	if !c2.IsAborted() {
		t.Fatal("first IP second request should be aborted")
	}

	// Second IP should have its own burst token
	c3, _ := newTestRequest("203.0.113.31:1111")
	handler(c3)
	if c3.IsAborted() {
		t.Error("second IP should have its own burst token, should not be aborted")
	}
}

func TestEndpointRateLimiter_PerIP_ShouldBypassLimitForLocalhost(t *testing.T) {
	t.Parallel()

	handler := NewEndpointRateLimiter().PerIP(rate.Every(time.Minute), 0)

	c, _ := newTestRequest("127.0.0.1:1111")
	handler(c)

	if c.IsAborted() {
		t.Error("expected localhost (127.0.0.1) to bypass the limit")
	}
}

func TestEndpointRateLimiter_PerIP_ShouldBypassLimitForLocalhostIPv6(t *testing.T) {
	t.Parallel()

	handler := NewEndpointRateLimiter().PerIP(rate.Every(time.Minute), 0)

	c, _ := newTestRequest("[::1]:1111")
	handler(c)

	if c.IsAborted() {
		t.Error("expected localhost (::1) to bypass the limit")
	}
}

func TestEndpointRateLimiter_PerIP_ShouldAdvertiseRateLimitHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(NewErrorHandlerMiddleware().Add())
	r.Use(NewEndpointRateLimiter().PerIP(rate.Every(time.Minute), 1))
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
		t.Errorf("got RateLimit-Remaining %q after consuming burst, want %q", got, "0")
	}

	second := send()
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 on rejected request, got %d", second.Code)
	}
	if got := second.Header().Get("RateLimit-Reset"); got == "" || got == "0" {
		t.Errorf("expected positive RateLimit-Reset on rejected response, got %q", got)
	}
}

func TestEndpointRateLimiter_CodeBucket_ShouldAllowRequestsWithinBurst(t *testing.T) {
	t.Parallel()

	extractor := func(c *gin.Context) string {
		return "code-123"
	}

	handler := NewEndpointRateLimiter().CodeBucket(rate.Every(time.Minute), 2, extractor)

	c, w := newTestRequest("203.0.113.50:1111")
	handler(c)

	if c.IsAborted() {
		t.Fatal("expected first request within burst to not be aborted")
	}
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want default 200", w.Code)
	}
}

func TestEndpointRateLimiter_CodeBucket_ShouldRejectRequestsBeyondBurst(t *testing.T) {
	t.Parallel()

	extractor := func(c *gin.Context) string {
		return "code-456"
	}

	handler := NewEndpointRateLimiter().CodeBucket(rate.Every(time.Minute), 1, extractor)

	// First request uses the burst token
	c1, _ := newTestRequest("203.0.113.60:1111")
	handler(c1)
	if c1.IsAborted() {
		t.Fatal("expected first request to be allowed")
	}

	// Second request from different IP, same code, should be rejected
	c2, _ := newTestRequest("203.0.113.61:1111")
	handler(c2)

	if !c2.IsAborted() {
		t.Fatal("expected second request for same code to be aborted, even from different IP")
	}
}

func TestEndpointRateLimiter_CodeBucket_ShouldIsolateLimitsPerCode(t *testing.T) {
	t.Parallel()

	extractor := func(c *gin.Context) string {
		// Simulate extracting different codes from different requests
		return c.GetString("code")
	}

	handler := NewEndpointRateLimiter().CodeBucket(rate.Every(time.Minute), 1, extractor)

	// First code uses its burst token
	c1, _ := newTestRequest("203.0.113.70:1111")
	c1.Set("code", "code-A")
	handler(c1)
	if c1.IsAborted() {
		t.Fatal("first code first request should not be aborted")
	}

	// First code's second request should be rejected
	c2, _ := newTestRequest("203.0.113.70:1111")
	c2.Set("code", "code-A")
	handler(c2)
	if !c2.IsAborted() {
		t.Fatal("first code second request should be aborted")
	}

	// Second code should have its own burst token
	c3, _ := newTestRequest("203.0.113.70:1111")
	c3.Set("code", "code-B")
	handler(c3)
	if c3.IsAborted() {
		t.Error("second code should have its own burst token, should not be aborted")
	}
}

func TestEndpointRateLimiter_CodeBucket_ShouldSkipLimitWhenExtractorReturnsEmpty(t *testing.T) {
	t.Parallel()

	extractor := func(c *gin.Context) string {
		return "" // Simulate missing/invalid field
	}

	handler := NewEndpointRateLimiter().CodeBucket(rate.Every(time.Minute), 0, extractor)

	c, w := newTestRequest("203.0.113.80:1111")
	handler(c)

	if c.IsAborted() {
		t.Error("expected request to not be aborted when extractor returns empty string")
	}
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want 200", w.Code)
	}
}

func TestEndpointRateLimiter_CodeBucket_ShouldAdvertiseRateLimitHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	extractor := func(c *gin.Context) string {
		return "code-999"
	}

	r := gin.New()
	r.Use(NewErrorHandlerMiddleware().Add())
	r.Use(NewEndpointRateLimiter().CodeBucket(rate.Every(time.Minute), 1, extractor))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	send := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.RemoteAddr = "203.0.113.90:1234"
		r.ServeHTTP(w, req)
		return w
	}

	first := send()
	if got := first.Header().Get("RateLimit-Limit"); got != "1" {
		t.Errorf("got RateLimit-Limit %q, want %q", got, "1")
	}

	second := send()
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 on rejected request, got %d", second.Code)
	}
}

// TestEndpointRateLimiter_CodeBucket_ExtractorWorkingWithJSON validates that
// the extractor can parse JSON from the body without consuming it for the
// handler's own ShouldBindJSON call.
func TestEndpointRateLimiter_EvictStaleClients_ShouldDeleteEntriesOlderThanMaxAge(t *testing.T) {
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
