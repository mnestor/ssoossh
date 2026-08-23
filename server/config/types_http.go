package config

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
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
	Address string `mapstructure:"address"`

	// Port is the TCP port to listen on, e.g. 80 for HTTP or 443 for HTTPS.
	// Port 0 is reserved for testing and tells the OS to pick an unused port;
	// the actual port is logged on startup. Ignored when UnixSocket is set.
	Port int `mapstructure:"port"`

	// UnixSocket, when set, is a filesystem path to listen on a Unix domain
	// socket instead of TCP — Address and Port are ignored. Mutually
	// exclusive with ProxyProtocol (PROXY protocol is a TCP-connection
	// concept; it has nothing to prefix on a Unix socket).
	UnixSocket string `mapstructure:"unix_socket"`

	// ProxyProtocol lists the CIDR ranges of reverse proxies trusted to
	// prefix connections with a PROXY protocol v1/v2 header (so the real
	// client address survives a TCP-level proxy, as opposed to
	// TrustedProxies below which is for HTTP-level proxies setting
	// X-Forwarded-For). Empty disables PROXY protocol support entirely;
	// connections from any other source are rejected outright once this is
	// set. Ignored (must be empty) when UnixSocket is set.
	ProxyProtocol []string `mapstructure:"proxy_protocol"`

	// TrustedProxies lists the CIDR ranges of reverse proxies trusted to
	// set X-Forwarded-For/X-Forwarded-Proto, passed to gin's
	// SetTrustedProxies. Empty means no proxy is trusted, so c.ClientIP()
	// always reports the direct connection's address and a client-supplied
	// X-Forwarded-For is ignored.
	//
	// That only holds because the router passes this to SetTrustedProxies
	// unconditionally — gin's own default is to trust every proxy, so
	// skipping the call for an empty list would mean the opposite.
	TrustedProxies []string `mapstructure:"trusted_proxies"`

	// ServerName, when set, is the host name this server answers to:
	// requests addressed to anything else (by Host header, or SNI on TLS
	// connections) are rejected with 421 Misdirected Request by
	// middleware.ServerNameMiddleware. The health endpoints are registered
	// ahead of the check so probes can reach the server by IP. Empty
	// disables the check. It plays no role in the TLS handshake itself,
	// which is why it does not live in TLSConfig.
	ServerName string `mapstructure:"server_name"`

	// PublicURL is the scheme and host browsers actually reach this
	// deployment at, e.g. "https://ssh.example.com". Set it whenever that
	// differs from what Address/Port/TLS describe — which is every
	// reverse-proxy deployment, since the proxy terminates TLS on 443 while
	// this process listens on plain HTTP somewhere else.
	//
	// Two things are derived from it and cannot be got right without it: the
	// OIDC redirect URI handed to the identity provider, and the origin
	// CsrfMiddleware compares the browser's Origin header against. Both used
	// to be reconstructed from ServerName plus the *listen* port, which is
	// only the public port when nothing sits in front.
	//
	// Its scheme also settles IsTLS, so a deployment behind a TLS-terminating
	// proxy needs this or IsHTTPS, not both.
	//
	// Origin only: a path, query, or fragment is rejected at startup. Serving
	// the app under a sub-path would need the frontend's base to move with it,
	// which is not supported, so accepting one here would only produce a
	// redirect URI that silently does not work.
	PublicURL string `mapstructure:"public_url"`

	// If we don't configure TLS but instead of a reverse proxy setup
	// that is terminating TLS then we need to set this to true
	IsHTTPS bool `mapstructure:"is_https"`

	// CookieKey is the secret used to sign and encrypt session cookies. If
	// empty, a key is generated once and persisted in the server_secrets
	// table, so sessions survive a restart and instances sharing a database
	// share the key. Configure an explicit value to key it from outside the
	// database.
	CookieKey string `mapstructure:"cookie_key"`

	// CookieSecure marks the session cookie Secure, so browsers only send it
	// over HTTPS. Unset derives it from whether the deployment is HTTPS at
	// all (see IsTLS), which keeps plain-HTTP local development working
	// while defaulting to on everywhere else. Set it explicitly only to
	// override that inference.
	CookieSecure *bool `mapstructure:"cookie_secure"`

	// CookieSameSite controls the session cookie's SameSite attribute:
	// "strict" (default), "lax", or "none". Strict is right for this server
	// because nothing legitimately navigates into it from another site — the
	// OIDC callback is a redirect the browser follows to a URL this server
	// issued, not a cross-site form post.
	//
	// This is defence in depth, not the CSRF control: SameSite does nothing
	// against an attacker page on a different origin of the *same* site, so
	// state-changing routes are additionally guarded by
	// middleware.CsrfMiddleware.
	CookieSameSite string `mapstructure:"cookie_same_site"`

	// CookieMaxAge is the absolute ceiling on a session's lifetime, measured
	// from login. Activity does not extend it: once a session is this old
	// the next request is unauthenticated regardless of how recently it was
	// used, and the user signs in again. Zero uses the built-in default.
	CookieMaxAge time.Duration `mapstructure:"cookie_max_age"`

	// CookieIdleTimeout is how long a session survives without a request.
	// SessionAuthMiddleware slides this window on activity (re-saving the
	// session past half the window, which reissues the cookie), so an
	// actively-used session only ever ends at CookieMaxAge, while an
	// abandoned browser expires after this much quiet. Zero uses the
	// built-in default. Must not exceed CookieMaxAge - an idle window
	// longer than the absolute cap cannot ever be reached.
	CookieIdleTimeout time.Duration `mapstructure:"cookie_idle_timeout"`

	// RateLimit is the maximum number of requests per RateDuration allowed
	// per client IP. Zero or negative disables rate limiting entirely.
	// Localhost (127.0.0.1, ::1) bypasses the limit, so health probes and
	// tests from the local frontend are not throttled.
	RateLimit int `mapstructure:"rate_limit"`

	// RateDuration is the time window for rate limiting calculations. With
	// RateLimit=60 and RateDuration=1m, each IP is allowed 60 requests per
	// minute. Typical values are in seconds to minutes.
	RateDuration time.Duration `mapstructure:"rate_duration"`

	// RateLimitDisableForDev disables all rate limiting (global and per-endpoint)
	// when true. Only effective when Production=false, so it cannot be silently
	// enabled in production. Used for local development to avoid throttling
	// during testing.
	RateLimitDisableForDev bool `mapstructure:"rate_limit_disable_for_dev"`

	// CertRequestRateLimit holds per-endpoint rate limits for certificate
	// request creation endpoints. Zero or negative disables the specific limit.
	CertRequestRateLimit CertRequestRateLimitSettings `mapstructure:"cert_request_rate_limit"`

	// ServiceCodeRateLimit holds the rate limit configuration for service
	// enrollment code redemption, keyed on the enrollment code to protect
	// against brute-forcing. Zero or negative disables the specific limit.
	ServiceCodeRateLimit ServiceCodeRateLimitSettings `mapstructure:"service_code_rate_limit"`

	// Hsts (HTTP Strict-Transport-Security) is sent in the Strict-Transport-Security
	// header on every response, but only when the server terminates TLS itself
	// (i.e., TLS.Certificate or TLS.CertificateFile is configured). Browsers
	// ignore HSTS over plain HTTP. Empty disables the header, useful when a
	// TLS-terminating reverse proxy in front sets its own policy. Typical
	// values include "max-age=31536000; includeSubDomains".
	Hsts string `mapstructure:"hsts"`

	// TLS holds the server's TLS configuration: certificate/key material,
	// cipher suites, protocol versions, and supported curves. See TLSConfig
	// for detailed field options. If no certificate/key pair is configured,
	// the server runs without TLS, serving plain HTTP with HTTP/2 cleartext
	// (h2c) enabled.
	TLS tlsutils.TLSConfig `mapstructure:"tls"`
}

// IsTLS reports whether browsers reach this deployment over HTTPS — because
// PublicURL says so, because this process terminates TLS itself, or because a
// reverse proxy in front of it does and the operator said so via IsHTTPS.
//
// Shared so everything that has to reason about the browser-visible scheme
// agrees: the OIDC redirect URL and the session cookie's Secure attribute
// must not be able to disagree about it.
//
// PublicURL wins when set. It describes what the browser sees, which is the
// question being asked; the other two are proxies for it.
func (h *HTTPSettings) IsTLS() bool {
	if u, err := h.parsePublicURL(); err == nil && u != nil {
		return u.Scheme == "https"
	}
	return h.TLS.HasKeyPair() || h.IsHTTPS
}

// PublicOrigin returns the scheme://host origin browsers use to reach this
// deployment: PublicURL when configured, otherwise inferred from ServerName,
// Port, and IsTLS.
//
// Returns "" when neither is available — PublicURL unset and ServerName
// empty. Callers treat that as "the public origin is unknown": CsrfMiddleware
// falls back to Sec-Fetch-Site alone rather than comparing against a guess.
//
// The inference is kept for deployments with nothing in front, where the
// listen port really is the public port. It is wrong the moment a proxy is
// involved, which is what PublicURL exists to fix.
func (h *HTTPSettings) PublicOrigin() string {
	if u, err := h.parsePublicURL(); err == nil && u != nil {
		return u.Scheme + "://" + u.Host
	}

	if h.ServerName == "" {
		return ""
	}

	scheme, defaultPort := "http", 80
	if h.IsTLS() {
		scheme, defaultPort = "https", 443
	}

	host := h.ServerName
	if h.Port != 0 && h.Port != defaultPort {
		host = net.JoinHostPort(h.ServerName, strconv.Itoa(h.Port))
	}

	return scheme + "://" + host
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
	User float64 `mapstructure:"user"`

	// ServiceEnroll is the limit for POST /api/certs/service/enroll (service
	// enrollment, unattended).
	ServiceEnroll float64 `mapstructure:"service_enroll"`

	// PAM is the limit for POST /api/certs/pam (short-lived PAM cert, gated on
	// local interaction).
	PAM float64 `mapstructure:"pam"`
}

// ServiceCodeRateLimitSettings holds the rate limit configuration for service
// enrollment code redemption (POST /api/certs/service/retrieve). The limit is
// applied per code to protect against brute-forcing across multiple IPs.
type ServiceCodeRateLimitSettings struct {
	// Limit is requests per second per enrollment code. Zero or negative
	// disables this limit.
	Limit float64 `mapstructure:"limit"`
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
