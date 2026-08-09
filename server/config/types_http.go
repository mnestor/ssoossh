package config

import (
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
	// SetTrustedProxies. Empty means gin trusts no proxy headers at all
	// (c.ClientIP() always reports the direct connection's address).
	TrustedProxies []string `mapstructure:"trusted_proxies"`

	// ServerName, when set, is the host name this server answers to:
	// requests addressed to anything else (by Host header, or SNI on TLS
	// connections) are rejected with 421 Misdirected Request by
	// middleware.ServerNameMiddleware. The health endpoints are registered
	// ahead of the check so probes can reach the server by IP. Empty
	// disables the check. It plays no role in the TLS handshake itself,
	// which is why it does not live in TLSConfig.
	ServerName string `mapstructure:"server_name"`

	// If we don't configure TLS but instead of a reverse proxy setup
	// that is terminating TLS then we need to set this to true
	IsHTTPS bool `mapstructure:"is_https"`

	// CookieKey is the secret used to sign and encrypt session cookies. If
	// empty, a random key is generated at startup (suitable only for single-
	// process servers without session persistence). For production use,
	// configure an explicit key so all instances can validate each other's
	// cookies.
	CookieKey string `mapstructure:"cookie_key"`

	// RateLimit is the maximum number of requests per RateDuration allowed
	// per client IP. Zero or negative disables rate limiting entirely.
	// Localhost (127.0.0.1, ::1) bypasses the limit, so health probes and
	// tests from the local frontend are not throttled.
	RateLimit int `mapstructure:"rate_limit"`

	// RateDuration is the time window for rate limiting calculations. With
	// RateLimit=60 and RateDuration=1m, each IP is allowed 60 requests per
	// minute. Typical values are in seconds to minutes.
	RateDuration time.Duration `mapstructure:"rate_duration"`

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
