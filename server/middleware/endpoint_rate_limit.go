package middleware

import (
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// EndpointRateLimiter applies per-IP rate limiting for specific endpoints.
type EndpointRateLimiter struct{} // Stateless factory struct

// NewEndpointRateLimiter creates an EndpointRateLimiter.
func NewEndpointRateLimiter() *EndpointRateLimiter {
	return &EndpointRateLimiter{}
}

// PerIP returns a gin.HandlerFunc that enforces a per-client-IP token-bucket
// rate limit for a specific endpoint, responding with TooManyRequestsError
// once a client exceeds it. Localhost requests bypass the limit. The limiter
// is reused across all calls, so multiple endpoints can each have their own
// per-IP rate limit independent of each other.
func (el *EndpointRateLimiter) PerIP(limit rate.Limit, burst int) gin.HandlerFunc {
	var clients = make(map[string]*client)
	var mu sync.Mutex

	// Start the cleanup routine
	go cleanupClients(&mu, clients)

	return func(c *gin.Context) {
		ip := c.ClientIP()

		// Skip rate limiting for localhost and test environment
		if ip == "" || ip == "127.0.0.1" || ip == "::1" {
			c.Next()
			return
		}

		limiter := getLimiter(ip, limit, burst, &mu, clients)
		allowed := limiter.Allow()
		setRateLimitHeaders(c, limiter, burst)
		if !allowed {
			_ = c.Error(&errorresponses.TooManyRequestsError{}) //nolint:errcheck
			c.Abort()
			return
		}

		c.Next()
	}
}

// PerKeys enforces one token bucket per key the extractor returns, and
// requires every one of them to allow the request. A key set that is empty
// applies no limit at all, on the same terms as CodeBucket: the handler's
// own validation is what rejects a request nothing could be keyed on.
//
// Two axes rather than one is the point where it is used. Console code
// submission is limited per session and per source address together, so a
// single compromised account cannot grind through the code space from many
// addresses and a single address cannot do it across many accounts.
// Limiting on either alone leaves the other open.
//
// A request refused by the second key has already spent a token on the
// first. That makes the limit marginally stricter than the sum of its
// parts, which is the harmless direction for a control whose whole job is
// to bound guessing.
func (el *EndpointRateLimiter) PerKeys(limit rate.Limit, burst int, extractor func(*gin.Context) []string) gin.HandlerFunc {
	var buckets = make(map[string]*client)
	var mu sync.Mutex

	go cleanupClients(&mu, buckets)

	return func(c *gin.Context) {
		keys := extractor(c)
		if len(keys) == 0 {
			c.Next()
			return
		}

		allowed := true
		var last *rate.Limiter
		for _, key := range keys {
			limiter := getLimiter(key, limit, burst, &mu, buckets)
			if !limiter.Allow() {
				allowed = false
			}
			last = limiter
		}
		setRateLimitHeaders(c, last, burst)
		if !allowed {
			_ = c.Error(&errorresponses.TooManyRequestsError{}) //nolint:errcheck
			c.Abort()
			return
		}

		c.Next()
	}
}

// CodeBucket applies rate limiting keyed on a field extracted from the request
// body (typically an enrollment code). It protects against brute-forcing a
// code across multiple IPs. The extractor function is responsible for reading
// the body field; it should return an empty string if the field is missing or
// invalid, in which case no rate limit is applied (the handler will reject it
// separately via validation).
//
// The extractor is called before JSON binding, and it must only read the body
// without consuming it — c.ShouldBindJSON still works afterward. This ensures
// the limiter survives refactors that move the JSON binding inside the handler.
//
// Cleanup runs forever, once a minute evicting entries for codes that haven't
// been seen in over 3 minutes. Per-code limiters are never cleaned up if the
// code is still being attacked, so a fixed timeout of 3 minutes bounds memory
// overhead even under sustained attack.
func (el *EndpointRateLimiter) CodeBucket(limit rate.Limit, burst int, extractor func(*gin.Context) string) gin.HandlerFunc {
	var codes = make(map[string]*client)
	var mu sync.Mutex

	// Start the cleanup routine
	go cleanupClients(&mu, codes)

	return func(c *gin.Context) {
		key := extractor(c)
		if key == "" {
			// Key extraction failed (missing/invalid field in body).
			// Let the handler's own validation catch it; do not rate-limit.
			c.Next()
			return
		}

		limiter := getLimiter(key, limit, burst, &mu, codes)
		allowed := limiter.Allow()
		setRateLimitHeaders(c, limiter, burst)
		if !allowed {
			_ = c.Error(&errorresponses.TooManyRequestsError{}) //nolint:errcheck
			c.Abort()
			return
		}

		c.Next()
	}
}
