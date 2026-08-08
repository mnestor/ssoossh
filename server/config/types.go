package config

import (
	"time"

	"github.com/DeRuina/timberjack"

	"github.com/mnestor/ssoossh/server/config/tlsutils"
)

// Supported values for DBProvider.
const (
	DBProviderSqlite   = "sqlite"
	DBProviderPostgres = "postgres"
)

// AppLogging configures the application's main log output.
type AppLogging struct {
	timberjack.Logger
	Level             string `mapstructure:"level"`
	CopyStdout        bool   `mapstructure:"enable_stdout"`
	IncludeAppName    bool   `mapstructure:"include_app_name"`
	IncludeAppVersion bool   `mapstructure:"include_app_version"`
	LogJSON           bool   `mapstructure:"log_json"`
}

// DBLogging configures logging of database queries.
type DBLogging struct {
	timberjack.Logger
	Level     string `mapstructure:"level"`
	AddSource bool   `mapstructure:"add_source"`
	LogJSON   bool   `mapstructure:"log_json"`
}

// AccessLogging configures which fields the HTTP access log records.
type AccessLogging struct {
	timberjack.Logger
	WithUserAgent      bool `mapstructure:"log_user_agent"`
	WithRequestHeader  bool `mapstructure:"log_request_header"`
	WithClientIP       bool `mapstructure:"log_client_ip"`
	WithRequestID      bool `mapstructure:"log_request_id"`
	WithRequestBody    bool `mapstructure:"log_request_body"`
	WithResponseBody   bool `mapstructure:"log_response_body"`
	WithResponseHeader bool `mapstructure:"log_response_header"`
	WithSpanID         bool `mapstructure:"log_span_id"`
	WithTraceID        bool `mapstructure:"log_trace_id"`
	LogJSON            bool `mapstructure:"log_json"`
}

// DBProvider identifies which database backend to use. See DBProviderSqlite
// and DBProviderPostgres.
type DBProvider string

// Config is the root ssoosshd configuration, populated from defaults.yaml
// and the user's config file via NewConfig.
type Config struct {
	Logging AppLogging `mapstructure:"logging"`

	HTTP        HTTPSettings       `mapstructure:"http"`
	CertOptions CertificateOptions `mapstructure:"cert_options"`
	Production  bool               `mapstructure:"production"`

	SSHKey string `mapstructure:"ssh_key"`

	Traces  bool `mapstructure:"traces"`
	Metrics bool `mapstructure:"metrics"`

	DB DB `mapstructure:"db"`
}

// DB configures the database connection, query logging, and connection retry behavior.
type DB struct {
	// Provider identifies which database backend to use. See DBProviderSqlite
	// and DBProviderPostgres.
	Provider DBProvider `mapstructure:"provider"`

	// Connection is the connection string / DSN. For SQLite this is a file
	// path (or ":memory:" for in-memory); for PostgreSQL a standard postgres://
	// URL or keyword string.
	Connection string `mapstructure:"connection_string"`

	// Logging configures which database queries are logged and where they
	// are written. See DBLogging for detailed options.
	Logging DBLogging `mapstructure:"logging"`

	// RetryAttempts is the maximum number of connection attempts before giving up.
	// Zero or negative disables retries (will fail immediately on the first error).
	// Applies to both SQLite (typically succeeds immediately) and PostgreSQL
	// (may retry on transient network failures).
	RetryAttempts int `mapstructure:"retry_attempts"`

	// RetryInterval is the time to wait between connection retry attempts.
	// Only used if RetryAttempts > 1. Applies to both SQLite and PostgreSQL.
	RetryInterval time.Duration `mapstructure:"retry_interval"`
}

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
	// the actual port is logged on startup.
	Port int `mapstructure:"port"`

	// ServerName, when set, is the host name this server answers to:
	// requests addressed to anything else (by Host header, or SNI on TLS
	// connections) are rejected with 421 Misdirected Request by
	// middleware.ServerNameMiddleware. The health endpoints are registered
	// ahead of the check so probes can reach the server by IP. Empty
	// disables the check. It plays no role in the TLS handshake itself,
	// which is why it does not live in TLSConfig.
	ServerName string `mapstructure:"server_name"`

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

	// AuthConfig configures OAuth/OIDC authentication for the server. See
	// OAuthConfig for details on provider URL, scopes, and field mapping
	// from OIDC claims to ssoossh identity fields (username, groups).
	AuthConfig OAuthConfig `mapstructure:"authentication"`

	// LDAP optionally enriches the identity resolved from OIDC with
	// attributes looked up by username. See LDAPConfig.
	LDAP LDAPConfig `mapstructure:"ldap"`
}

// OAuthConfig configures the OIDC provider used to authenticate users.
type OAuthConfig struct {
	ClientID     string `mapstructure:"client_id"`
	ClientSecret string `mapstructure:"client_secret"`
	ProviderURL  string `mapstructure:"provider_url"`

	// RedirectURL is the full callback URL registered with the OIDC
	// provider, e.g. "https://sso.example.com/auth/callback" - it must
	// match the router's /auth/callback route (see bootstrap/router.go).
	// Not derived from the incoming request's Host/scheme headers, since
	// those aren't trustworthy without a configured trusted-proxy list
	// this project doesn't have yet; set explicitly instead.
	RedirectURL string `mapstructure:"redirect_url"`

	// Scopes is a space-separated list of additional scopes to request
	// alongside the always-included "openid" scope, e.g. "profile email".
	Scopes string      `mapstructure:"scopes"`
	Fields OAuthFields `mapstructure:"fields"`
}

// OAuthFields maps ssoossh identity fields to claim names in the OIDC
// provider's ID token.
type OAuthFields struct {
	Username string `mapstructure:"username"`
	Groups   string `mapstructure:"groups"`
}

// LDAPConfig configures optional LDAP identity enrichment, looked up by the
// username resolved from OIDC (see docs/ssoossh-context.md — "Which LDAP
// attributes become principals" is still an open question). Enabled false
// (the default) skips LDAP entirely; OIDC claims alone are sufficient.
//
// TODO: not yet consumed by service.AuthService — fields are a starting
// guess at what a reference deployment (lldap) needs.
type LDAPConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	URL          string `mapstructure:"url"`
	BindDN       string `mapstructure:"bind_dn"`
	BindPassword string `mapstructure:"bind_password"`
	BaseDN       string `mapstructure:"base_dn"`
	UserFilter   string `mapstructure:"user_filter"`
}

// CertificateOptions groups the certificate-issuance options for each
// SSH certificate type ssoosshd can sign.
type CertificateOptions struct {
	User    CertOptionsUser    `mapstructure:"user"`
	Service CertOptionsService `mapstructure:"service"`
	Host    CertOptions        `mapstructure:"host"`
}

// CertOptions configures issuance of host certificates: the OIDC group
// required to request one, and how long they're valid for.
type CertOptions struct {
	RequireGroup  string        `mapstructure:"require_group"`
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
}

// CertOptionsUser configures issuance of user certificates: how long
// they're valid for, and which SSH certificate extensions to grant.
type CertOptionsUser struct {
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
	Extensions    []string      `mapstructure:"extensions"`
}

// CertOptionsService configures issuance of service certificates: the OIDC
// group required to request one, how long they're valid for, and which SSH
// certificate extensions to grant.
type CertOptionsService struct {
	RequireGroup  string        `mapstructure:"require_group"`
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
	Extensions    []string      `mapstructure:"extensions"`
}
