package middleware

import (
	"math"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimitMiddleware applies a per-client-IP token-bucket rate limit.
type RateLimitMiddleware struct{}

// NewRateLimitMiddleware creates a RateLimitMiddleware.
func NewRateLimitMiddleware() *RateLimitMiddleware {
	return &RateLimitMiddleware{}
}

// Add returns a gin.HandlerFunc that enforces a rate limit of limit events
// per second (with the given burst) per client IP, responding with
// TooManyRequestsError once a client exceeds it. Localhost
// requests bypass the limit, since they come from the local frontend.
func (m *RateLimitMiddleware) Add(limit rate.Limit, burst int) gin.HandlerFunc {
	// Map to store the rate limiters per IP
	var clients = make(map[string]*client)
	var mu sync.Mutex

	// Start the cleanup routine
	go cleanupClients(&mu, clients)

	return func(c *gin.Context) {
		ip := c.ClientIP()

		// Skip rate limiting for localhost and test environment
		// If the client ip is localhost the request comes from the frontend
		if ip == "" || ip == "127.0.0.1" || ip == "::1" /*|| IsTest()*/ {
			c.Next()
			return
		}

		limiter := getLimiter(ip, limit, burst, &mu, clients)
		allowed := limiter.Allow()
		setRateLimitHeaders(c, limiter, burst)
		if !allowed {
			_ = c.Error(&TooManyRequestsError{}) //nolint:errcheck // c.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
			c.Abort()
			return
		}

		c.Next()
	}
}

// setRateLimitHeaders advertises the caller's remaining budget, per
// .claude/rules/server-api.md. Field names follow the IETF RateLimit header
// draft.
//
// Set after Allow() so Remaining reflects this request having been counted,
// and on the rejected response too — a client that just got a 429 is
// precisely the one that needs to know when to retry.
//
// Reset is approximate: a token bucket has no window to expire, so this
// reports how long until at least one token is available again.
func setRateLimitHeaders(c *gin.Context, limiter *rate.Limiter, burst int) {
	tokens := limiter.Tokens()

	remaining := int(tokens)
	if remaining < 0 {
		remaining = 0
	}

	var reset int
	if tokens < 1 {
		if perSecond := float64(limiter.Limit()); perSecond > 0 {
			reset = int(math.Ceil((1 - tokens) / perSecond))
		}
	}

	c.Header("RateLimit-Limit", strconv.Itoa(burst))
	c.Header("RateLimit-Remaining", strconv.Itoa(remaining))
	c.Header("RateLimit-Reset", strconv.Itoa(reset))
}

// client tracks a per-IP rate limiter and when it was last used, so
// cleanupClients can evict entries for IPs that have gone quiet.
type client struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// cleanupClients runs forever, once a minute evicting entries from clients
// that haven't been seen in over 3 minutes. It is a thin real-time driver
// around evictStaleClients (which carries the actual logic and is unit
// tested directly); this loop is excluded from coverage in
// exclude-from-coverage.txt since testing it would mean sleeping a real
// minute or adding a clock/ticker abstraction solely for this call site.
func cleanupClients(mu *sync.Mutex, clients map[string]*client) {
	for {
		time.Sleep(time.Minute)
		evictStaleClients(mu, clients, 3*time.Minute)
	}
}

// evictStaleClients deletes entries from clients that haven't been seen in
// over maxAge, guarding access to clients with mu.
func evictStaleClients(mu *sync.Mutex, clients map[string]*client, maxAge time.Duration) {
	mu.Lock()
	defer mu.Unlock()

	for ip, client := range clients {
		if time.Since(client.lastSeen) > maxAge {
			delete(clients, ip)
		}
	}
}

// getLimiter retrieves the rate limiter for a given IP address, creating one if it doesn't exist.
func getLimiter(
	ip string,
	limit rate.Limit,
	burst int,
	mu *sync.Mutex,
	clients map[string]*client,
) *rate.Limiter {

	mu.Lock()
	defer mu.Unlock()

	if client, exists := clients[ip]; exists {
		client.lastSeen = time.Now()
		return client.limiter
	}

	limiter := rate.NewLimiter(limit, burst)
	clients[ip] = &client{limiter: limiter, lastSeen: time.Now()}
	return limiter
}
