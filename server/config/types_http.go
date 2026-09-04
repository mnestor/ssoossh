package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mnestor/ssoossh/server/config/tlsutils"
)

// HTTPSettings configures the HTTP(S) server: bind address, TLS,
// rate limiting, and OAuth/OIDC authentication.
type HTTPSettings struct {
	// AccessLogging configures which fields the HTTP access log records and where
	// it writes them. The access log is routed separately from the main
	// application log, so its level and format can differ. See AccessLogging for
	// detailed field options.
	AccessLogging AccessLogging `mapstructure:"access_logging"`

	// Address is the network interface to bind to, e.g. "127.0.0.1" to listen
	// only locally, or "0.0.0.0" for all interfaces. Used with Port to form
	// the server's listen address.
	Address string `mapstructure:"address" default:"127.0.0.1"`

	// Port is the TCP port to listen on, e.g. 80 for HTTP or 443 for HTTPS.
	// Port 0 is reserved for testing and tells the OS to pick an unused port;
	// the actual port is logged on startup. Ignored when UnixSocket is set.
	Port int `mapstructure:"port" default:"8080"`

	// UnixSocket, when set, is a filesystem path to listen on a Unix domain
	// socket instead of TCP — Address and Port are ignored. Mutually
	// exclusive with ProxyProtocol (PROXY protocol is a TCP-connection
	// concept; it has nothing to prefix on a Unix socket).
	UnixSocket string `mapstructure:"unix_socket" default:""`

	// ProxyProtocol lists the CIDR ranges of reverse proxies trusted to
	// prefix connections with a PROXY protocol v1/v2 header (so the real
	// client address survives a TCP-level proxy, as opposed to
	// TrustedProxies below which is for HTTP-level proxies setting
	// X-Forwarded-For). Empty disables PROXY protocol support entirely;
	// connections from any other source are rejected outright once this is
	// set. Ignored (must be empty) when UnixSocket is set.
	ProxyProtocol []string `mapstructure:"proxy_protocol" default:"[]"`

	// TrustedProxies lists the CIDR ranges of reverse proxies trusted to
	// set X-Forwarded-For/X-Forwarded-Proto, passed to gin's
	// SetTrustedProxies. Empty means no proxy is trusted, so c.ClientIP()
	// always reports the direct connection's address and a client-supplied
	// X-Forwarded-For is ignored.
	//
	// That only holds because the router passes this to SetTrustedProxies
	// unconditionally — gin's own default is to trust every proxy, so
	// skipping the call for an empty list would mean the opposite.
	TrustedProxies []string `mapstructure:"trusted_proxies" default:"[]"`

	// PublicURL is the scheme and host browsers actually reach this
	// deployment at, e.g. "https://ssh.example.com". Required: it is the one
	// place the browser-visible identity of this deployment is written down,
	// and it will differ from what Address/Port/TLS describe in every
	// reverse-proxy deployment, since the proxy terminates TLS on 443 while
	// this process listens on plain HTTP somewhere else.
	//
	// Everything that has to know how the browser sees this server is derived
	// from it: the OIDC redirect URI handed to the identity provider, the
	// origin the CSRF middleware compares the browser's Origin header
	// against, the host name requests must be addressed to (anything else
	// is rejected with 421 Misdirected Request; health endpoints are exempt
	// so probes can reach the server by IP), and whether the deployment is
	// HTTPS at all, which decides the session cookie's Secure attribute.
	//
	// Origin only: a path, query, or fragment is rejected at startup. Serving
	// the app under a sub-path would need the frontend's base to move with it,
	// which is not supported, so accepting one here would only produce a
	// redirect URI that silently does not work.
	PublicURL string `mapstructure:"public_url" default:""`

	// CookieKey is the secret used to sign and encrypt session cookies. If
	// empty, a key is generated once and persisted in the server_secrets
	// table, so sessions survive a restart and instances sharing a database
	// share the key. Configure an explicit value to key it from outside the
	// database.
	CookieKey string `mapstructure:"cookie_key" default:"" secret:"true"`

	// CookieSecure marks the session cookie Secure, so browsers only send it
	// over HTTPS. Unset derives it from whether the deployment is HTTPS at
	// all (the scheme of public_url, or a local TLS keypair), which keeps
	// plain-HTTP local development working while defaulting to on
	// everywhere else. Set it explicitly only to override that inference.
	CookieSecure *bool `mapstructure:"cookie_secure" default:"~"`

	// CookieSameSite controls the session cookie's SameSite attribute:
	// "strict" (default), "lax", or "none". Strict is right for this server
	// because nothing legitimately navigates into it from another site — the
	// OIDC callback is a redirect the browser follows to a URL this server
	// issued, not a cross-site form post.
	//
	// This is defence in depth, not the CSRF control: SameSite does nothing
	// against an attacker page on a different origin of the *same* site, so
	// state-changing routes are additionally guarded by
	// the CSRF middleware.
	CookieSameSite string `mapstructure:"cookie_same_site" default:"strict"`

	// CookieMaxAge is the absolute ceiling on a session's lifetime, measured
	// from login. Activity does not extend it: once a session is this old
	// the next request is unauthenticated regardless of how recently it was
	// used, and the user signs in again. Zero uses the built-in default.
	CookieMaxAge time.Duration `mapstructure:"cookie_max_age" default:"9h"`

	// CookieIdleTimeout is how long a session survives without a request.
	// SessionAuthMiddleware slides this window on activity (re-saving the
	// session past half the window, which reissues the cookie), so an
	// actively-used session only ever ends at CookieMaxAge, while an
	// abandoned browser expires after this much quiet. Zero uses the
	// built-in default. Must not exceed CookieMaxAge - an idle window
	// longer than the absolute cap cannot ever be reached.
	CookieIdleTimeout time.Duration `mapstructure:"cookie_idle_timeout" default:"30m"`

	// RateLimit is the maximum number of requests per RateDuration allowed
	// per client IP. Zero or negative disables rate limiting entirely.
	// Localhost (127.0.0.1, ::1) bypasses the limit, so health probes and
	// tests from the local frontend are not throttled.
	RateLimit int `mapstructure:"rate_limit" default:"60"`

	// RateDuration is the time window for rate limiting calculations. With
	// RateLimit=60 and RateDuration=1m, each IP is allowed 60 requests per
	// minute. Typical values are in seconds to minutes.
	RateDuration time.Duration `mapstructure:"rate_duration" default:"1m"`

	// RateLimitDisableForDev disables all rate limiting (global and per-endpoint)
	// when true. Only effective when Production=false, so it cannot be silently
	// enabled in production. Used for local development to avoid throttling
	// during testing.
	RateLimitDisableForDev bool `mapstructure:"rate_limit_disable_for_dev" default:"false"`

	// CertRequestRateLimit holds per-endpoint rate limits for certificate
	// request creation endpoints. Zero or negative disables the specific limit.
	CertRequestRateLimit CertRequestRateLimitSettings `mapstructure:"cert_request_rate_limit"`

	// ServiceCodeRateLimit holds the rate limit configuration for service
	// enrollment code redemption, keyed on the enrollment code to protect
	// against brute-forcing. Zero or negative disables the specific limit.
	ServiceCodeRateLimit ServiceCodeRateLimitSettings `mapstructure:"service_code_rate_limit"`

	// ConsoleCodeRateLimit holds the rate limit configuration for console
	// user-code submission, keyed on the submitting session and its source
	// address to protect against brute-forcing.
	ConsoleCodeRateLimit ConsoleCodeRateLimitSettings `mapstructure:"console_code_rate_limit"`

	// Hsts (HTTP Strict-Transport-Security) is sent in the Strict-Transport-Security
	// header on every response, but only when the server terminates TLS itself
	// (i.e. tls.certificate_file and tls.private_key_file are set). Browsers
	// ignore HSTS over plain HTTP. Empty disables the header, useful when a
	// TLS-terminating reverse proxy in front sets its own policy. Typical
	// values include "max-age=31536000; includeSubDomains".
	Hsts string `mapstructure:"hsts" default:"max-age=31536000; includeSubDomains"`

	// TLS holds the server's TLS configuration: certificate/key material,
	// cipher suites, protocol versions, and supported curves. See TLSConfig
	// for detailed field options. If no certificate/key pair is configured,
	// the server runs without TLS, serving plain HTTP with HTTP/2 cleartext
	// (h2c) enabled.
	TLS tlsutils.TLSConfig `mapstructure:"tls"`
}

// IsTLS reports whether browsers reach this deployment over HTTPS — because
// PublicURL says so, or because this process terminates TLS itself.
//
// Shared so everything that has to reason about the browser-visible scheme
// agrees: the OIDC redirect URL and the session cookie's Secure attribute
// must not be able to disagree about it.
//
// PublicURL wins when set. It describes what the browser sees, which is the
// question being asked; a local keypair is only a proxy for it.
func (h *HTTPSettings) IsTLS() bool {
	if u, err := h.parsePublicURL(); err == nil && u != nil {
		return u.Scheme == "https"
	}
	return h.TLS.HasKeyPair()
}

// PublicOrigin returns the scheme://host origin browsers use to reach this
// deployment, taken from PublicURL with any trailing slash removed.
//
// Returns "" when PublicURL is unset or unparseable. Callers treat that as
// "the public origin is unknown": the CSRF middleware falls back to
// Sec-Fetch-Site alone rather than comparing against a guess. An
// unparseable value is rejected at startup by Validate, so at runtime ""
// only ever means unset.
func (h *HTTPSettings) PublicOrigin() string {
	if u, err := h.parsePublicURL(); err == nil && u != nil {
		return u.Scheme + "://" + u.Host
	}
	return ""
}

// PublicHost returns the host name from PublicURL without any port, which is
// the name requests must be addressed to: middleware.ServerNameMiddleware
// rejects anything else (by Host header, or SNI on TLS connections) with
// 421 Misdirected Request. The port is dropped because the middleware
// compares names only — behind a proxy the browser's port and this
// listener's port differ, and neither says anything about which host the
// request was meant for.
//
// Returns "" when PublicURL is unset or unparseable, which disables the
// check.
func (h *HTTPSettings) PublicHost() string {
	if u, err := h.parsePublicURL(); err == nil && u != nil {
		return u.Hostname()
	}
	return ""
}

// parsePublicURL parses PublicURL, returning (nil, nil) when it is unset.
// Unexported because callers want the resolved origin, not the URL.
func (h *HTTPSettings) parsePublicURL() (*url.URL, error) {
	raw := strings.TrimSpace(h.PublicURL)
	if raw == "" {
		return nil, nil
	}

	// A trailing slash is what everyone types and what url.Parse reports as
	// Path "/", so trim it before the no-path check rather than rejecting the
	// most natural spelling of a correct value.
	u, err := url.Parse(strings.TrimSuffix(raw, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid http.public_url %q: %w", h.PublicURL, err)
	}

	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return nil, fmt.Errorf(`invalid http.public_url %q: needs an http:// or https:// scheme, e.g. "https://ssh.example.com"`, h.PublicURL)
	case u.Host == "":
		return nil, fmt.Errorf(`invalid http.public_url %q: needs a host, e.g. "https://ssh.example.com"`, h.PublicURL)
	case u.Path != "" || u.RawQuery != "" || u.Fragment != "":
		return nil, fmt.Errorf("invalid http.public_url %q: must be an origin only, with no path, query, or fragment — serving under a sub-path is not supported", h.PublicURL)
	}

	return u, nil
}

// CertRequestRateLimitSettings holds per-endpoint rate limits for certificate
// request creation. Each value is requests per second per client IP. Zero or
// negative disables that endpoint's limit (not recommended; use global limit
// instead). These are independent of the global per-IP rate limit — both apply.
type CertRequestRateLimitSettings struct {
	// User is the limit for POST /api/certs/user (interactive user cert).
	User float64 `mapstructure:"user" default:"10"`

	// ServiceEnroll is the limit for POST /api/certs/service/enroll (service
	// enrollment, unattended).
	ServiceEnroll float64 `mapstructure:"service_enroll" default:"5"`

	// PAM is the limit for POST /api/certs/pam (short-lived PAM cert, gated on
	// local interaction).
	PAM float64 `mapstructure:"pam" default:"10"`

	// Console is the limit for POST /api/certs/console (console login,
	// gated on someone standing at a keyboard).
	Console float64 `mapstructure:"console" default:"10"`
}

// ServiceCodeRateLimitSettings holds the rate limit configuration for service
// enrollment code redemption (POST /api/certs/service/retrieve). The limit is
// applied per code to protect against brute-forcing across multiple IPs.
type ServiceCodeRateLimitSettings struct {
	// Limit is requests per second per enrollment code. Zero or negative
	// disables this limit.
	Limit float64 `mapstructure:"limit" default:"1"`
}

// ConsoleCodeRateLimitSettings holds the rate limit configuration for
// console user-code submission (POST /api/certs/requests/resolve-code).
//
// Keyed on the submitting session rather than the source address, and on
// the address as well, so a single compromised account cannot grind through
// the code space from many addresses and a single address cannot do it
// across many accounts. At 40 bits with a handful of live codes even a
// generous limit leaves an infeasible search; this exists to make that
// margin independent of how many requests happen to be pending.
type ConsoleCodeRateLimitSettings struct {
	// Limit is submissions per second per session and per source address.
	// Zero or negative disables this limit.
	Limit float64 `mapstructure:"limit" default:"1"`

	// Burst is how many submissions may arrive back to back before the
	// per-second rate starts holding them back. Small on purpose: a human
	// typing a code they misread retries once or twice, not ten times.
	Burst int `mapstructure:"burst" default:"5"`
}

// Validate reports configuration that would fail later, at a point where the
// failure is harder to read. Called once at startup by NewConfig.
//
// http.public_url is the case worth catching early: a bad value does not stop
// the server starting, it produces an OIDC redirect URI the identity provider
// rejects, which surfaces as a login failure on the provider's side with
// nothing in this server's logs.
func (h *HTTPSettings) Validate() error {
	if _, err := h.parsePublicURL(); err != nil {
		return err
	}
	// Only checkable when both are set explicitly; when either falls back
	// to its built-in default the bootstrap resolvers keep the pair
	// consistent (30m inside 9h).
	if h.CookieIdleTimeout > 0 && h.CookieMaxAge > 0 && h.CookieIdleTimeout > h.CookieMaxAge {
		return fmt.Errorf("http.cookie_idle_timeout (%s) must not exceed http.cookie_max_age (%s): the idle window can never outlive the absolute session cap", h.CookieIdleTimeout, h.CookieMaxAge)
	}
	return nil
}
