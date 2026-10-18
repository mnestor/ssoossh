package middleware

import (
	"fmt"
	"math"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// Context keys the NSS middleware sets.
const (
	// nssKeySecretContextKey holds the fleet key secret, moved off the
	// request headers by NSSKeySecretMiddleware.
	nssKeySecretContextKey = "ssoossh.nss_key_secret"
	// nssOutcomeContextKey holds the *service.NSSLookupOutcome of the
	// lookup in flight, which the handler completes.
	nssOutcomeContextKey = "ssoossh.nss_outcome"
)

// NSSLookupPathPrefixes are the four lookup routes' prefixes, the paths the
// global per-IP limiter leaves to NSSFleetKeyMiddleware. The session routes
// under /api/nss (keys, shells) are not among them and keep the global
// limit.
func NSSLookupPathPrefixes() []string {
	return []string{"/api/nss/user/name/", "/api/nss/user/uid/", "/api/nss/group/name/", "/api/nss/group/gid/"}
}

// NSSKeySecretMiddleware takes the fleet key secret off every request
// before anything can log it. Registered on the engine right after the
// access logger: the logger reads the request headers after the rest of
// the chain has run (and http.access_logging.log_request_header is on by
// default), and gin's panic recovery dumps them, so a header still present
// then would be written out. Moving the value into the gin context keeps
// it from the access log, every error path and every route, NSS or not,
// and leaves the key id, which may be logged, where it was.
type NSSKeySecretMiddleware struct{}

// NewNSSKeySecretMiddleware creates an NSSKeySecretMiddleware.
func NewNSSKeySecretMiddleware() *NSSKeySecretMiddleware { return &NSSKeySecretMiddleware{} }

// Add returns the handler. The first value is kept and every value of the
// header is removed.
func (m *NSSKeySecretMiddleware) Add() gin.HandlerFunc {
	return func(c *gin.Context) {
		if values := c.Request.Header.Values(apitypes.HeaderNSSKeySecret); len(values) > 0 {
			c.Set(nssKeySecretContextKey, values[0])
			c.Request.Header.Del(apitypes.HeaderNSSKeySecret)
		}
		c.Next()
	}
}

// NSSKeySecret returns the secret NSSKeySecretMiddleware took off the
// request, or "" when it carried none.
func NSSKeySecret(c *gin.Context) string {
	value, ok := c.Get(nssKeySecretContextKey)
	if !ok {
		return ""
	}
	secret, ok := value.(string)
	if !ok {
		// not covered: only NSSKeySecretMiddleware sets the key, always
		// to a string.
		return ""
	}
	return secret
}

// NSSFleetKeyConfig is how NSSFleetKeyMiddleware gates the lookups.
type NSSFleetKeyConfig struct {
	// Enabled is nss.enabled. Off, every lookup answers nss_disabled.
	Enabled bool
	// SourceAllowList is nss.source_allow_list: CIDRs, already validated
	// by config load. Empty allows every address.
	SourceAllowList []string
	// FailedLimit and FailedBurst bound requests whose key is refused,
	// per address, from http.rate_limit. A zero burst sets no bound.
	FailedLimit rate.Limit
	FailedBurst int
	// KeyLimit and KeyBurst are nss.rate_limit and nss.rate_burst, per
	// fleet key and address.
	KeyLimit rate.Limit
	KeyBurst int
}

// NSSFleetKeyMiddleware authenticates the NSS lookups with fleet keys and
// limits them. For each lookup, in order:
//
//  1. nss.enabled off: 404 nss_disabled.
//  2. The address the server observed (ClientIP, honouring
//     http.trusted_proxies, the value the audit source_ip records) outside
//     nss.source_allow_list: 403 nss_forbidden, before the key is looked
//     at.
//  3. The key checked (service.NSSKeyVerifier). Refused: the request costs
//     a token from a per-address bucket at the http.rate_limit settings,
//     429 when it is empty, else 401 nss_unauthorized with one body for
//     every cause.
//  4. Accepted: a token from the (key id, address) bucket at nss.rate_limit
//     and nss.rate_burst, 429 when it is empty.
//
// Every 429 carries nss_rate_limited and Retry-After in whole seconds, and
// every answer the RateLimit-* headers of the bucket that decided it. The
// finished lookup, whatever stopped it, is reported to the observer once.
type NSSFleetKeyMiddleware struct {
	enabled  bool
	allow    []netip.Prefix
	verifier service.NSSKeyVerifier
	observer service.NSSLookupObserver
	failed   *nssBuckets
	keyed    *nssBuckets
}

// NewNSSFleetKeyMiddleware builds the gate. verifier and observer may be
// nil only when cfg.Enabled is false.
func NewNSSFleetKeyMiddleware(cfg NSSFleetKeyConfig, verifier service.NSSKeyVerifier, observer service.NSSLookupObserver) (*NSSFleetKeyMiddleware, error) {
	m := &NSSFleetKeyMiddleware{enabled: cfg.Enabled && verifier != nil && observer != nil, verifier: verifier, observer: observer}
	for i, cidr := range cfg.SourceAllowList {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			return nil, fmt.Errorf("nss.source_allow_list[%d]: %w", i, err)
		}
		m.allow = append(m.allow, prefix.Masked())
	}
	if !m.enabled {
		// Every lookup answers nss_disabled; no bucket is ever consulted,
		// so none is built and no eviction loop started.
		return m, nil
	}
	if cfg.FailedBurst > 0 {
		m.failed = newNSSBuckets(cfg.FailedLimit, cfg.FailedBurst)
	}
	m.keyed = newNSSBuckets(cfg.KeyLimit, cfg.KeyBurst)
	return m, nil
}

// Lookup returns the gate for one lookup route, labelled with what it looks
// up (service.NSSKind*) and how (service.NSSBy*) for the metrics.
func (m *NSSFleetKeyMiddleware) Lookup(kind, by string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !m.enabled {
			abortWith(c, errorresponses.NSSDisabled())
			return
		}

		start := time.Now()
		outcome := &service.NSSLookupOutcome{
			Kind:     kind,
			By:       by,
			KeyID:    c.GetHeader(apitypes.HeaderNSSKeyID),
			SourceIP: c.ClientIP(),
		}
		defer func() {
			if outcome.Result == "" {
				// A handler that answered without reporting, or a panic
				// on its way up: either way not a lookup served.
				outcome.Result = service.NSSResultError
			}
			outcome.Duration = time.Since(start)
			m.observer.Observe(c.Request.Context(), *outcome)
		}()

		if !m.allowed(outcome.SourceIP) {
			outcome.Result = service.NSSResultForbidden
			abortWith(c, errorresponses.NSSForbidden())
			return
		}

		verdict, err := m.verifier.VerifyFleetKey(c.Request.Context(), outcome.KeyID, NSSKeySecret(c))
		if err != nil {
			outcome.Result = service.NSSResultError
			abortWith(c, err)
			return
		}
		outcome.Verdict = verdict

		if !verdict.OK {
			if m.failed != nil && !m.failed.take(c, outcome.SourceIP) {
				outcome.Result = service.NSSResultRateLimited
				abortWith(c, errorresponses.NSSRateLimited())
				return
			}
			outcome.Result = service.NSSResultUnauthorized
			abortWith(c, errorresponses.NSSUnauthorized())
			return
		}
		if !m.keyed.take(c, verdict.KeyID+"|"+outcome.SourceIP) {
			outcome.Result = service.NSSResultRateLimited
			abortWith(c, errorresponses.NSSRateLimited())
			return
		}

		c.Set(nssOutcomeContextKey, outcome)
		c.Next()
	}
}

// allowed reports whether ip may look anything up.
func (m *NSSFleetKeyMiddleware) allowed(ip string) bool {
	if len(m.allow) == 0 {
		return true
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range m.allow {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// abortWith registers err for the error handler and stops the chain.
func abortWith(c *gin.Context, err error) {
	_ = c.Error(err) //nolint:errcheck // c.Error only registers the error for the error-handler middleware and echoes it back; it never fails.
	c.Abort()
}

// NSSVerifiedKeyID is the fleet key the gate accepted for this lookup, for
// the response's key_id.
func NSSVerifiedKeyID(c *gin.Context) string {
	if outcome := nssOutcome(c); outcome != nil {
		return outcome.Verdict.KeyID
	}
	return ""
}

// SetNSSLookupResult records how the handler answered (a service.NSSResult*
// value) and, for a lookup by name, the name, so the gate reports both.
func SetNSSLookupResult(c *gin.Context, result, name string) {
	if outcome := nssOutcome(c); outcome != nil {
		outcome.Result = result
		outcome.Name = name
	}
}

// nssOutcome is the lookup in flight, or nil outside the gate.
func nssOutcome(c *gin.Context) *service.NSSLookupOutcome {
	value, ok := c.Get(nssOutcomeContextKey)
	if !ok {
		return nil
	}
	outcome, ok := value.(*service.NSSLookupOutcome)
	if !ok {
		// not covered: only the gate sets the key, always to an outcome.
		return nil
	}
	return outcome
}

// nssBuckets is a set of token buckets by key, with the same eviction as
// the other limiters in this package.
type nssBuckets struct {
	limit   rate.Limit
	burst   int
	mu      sync.Mutex
	clients map[string]*client
}

// newNSSBuckets creates the set and starts its eviction loop.
func newNSSBuckets(limit rate.Limit, burst int) *nssBuckets {
	b := &nssBuckets{limit: limit, burst: burst, clients: map[string]*client{}}
	go cleanupClients(&b.mu, b.clients)
	return b
}

// take spends a token from key's bucket, setting the RateLimit-* headers,
// and Retry-After when it is empty. It reports whether a token was there.
func (b *nssBuckets) take(c *gin.Context, key string) bool {
	limiter := getLimiter(key, b.limit, b.burst, &b.mu, b.clients)
	allowed := limiter.Allow()
	setRateLimitHeaders(c, limiter, b.burst)
	if !allowed {
		c.Header("Retry-After", strconv.Itoa(retryAfterSeconds(limiter)))
	}
	return allowed
}

// retryAfterSeconds is how long until the bucket holds a token again, in
// whole seconds and never less than one: Retry-After has no smaller unit,
// and zero would invite an immediate retry the bucket would refuse.
func retryAfterSeconds(limiter *rate.Limiter) int {
	missing := 1 - limiter.Tokens()
	perSecond := float64(limiter.Limit())
	if missing <= 0 || perSecond <= 0 {
		return 1
	}
	return max(1, int(math.Ceil(missing/perSecond)))
}
