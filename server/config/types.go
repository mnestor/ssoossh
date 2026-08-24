package config

import (
	"time"

	"github.com/mnestor/ssoossh/internal/fipsmode"
)

// Config is the root ssoosshd configuration, populated from defaults.yaml
// and the user's config file via NewConfig.
type Config struct {
	Logging    AppLogging `mapstructure:"logging"`
	Traces     bool       `mapstructure:"traces"`
	Metrics    bool       `mapstructure:"metrics"`
	Production bool       `mapstructure:"production"`

	DB    DB           `mapstructure:"db"`
	HTTP  HTTPSettings `mapstructure:"http"`
	Queue QueueConfig  `mapstructure:"queue"`

	// AuthConfig configures OAuth/OIDC authentication for the server. See
	// OAuthConfig for details on provider URL, scopes, and field mapping
	// from OIDC claims to ssoossh identity fields (username, groups).
	AuthConfig OAuthConfig `mapstructure:"authentication"`

	// LDAP optionally enriches the identity resolved from OIDC with
	// attributes looked up by username. See LDAPConfig.
	LDAP LDAPConfig `mapstructure:"ldap"`

	// Admin configures role-based authorization for administrative and
	// auditor-scoped operations. All admin group names are optional; empty
	// disables the corresponding role. The ssh_server_admin_group key lives
	// here rather than at top level so all three role-to-group mappings sit
	// together.
	Admin AdminConfig `mapstructure:"admin"`

	// Signer carries everything the signer needs (the CA key and the
	// broker), squashed so its YAML keys stay top-level. `ssoosshd sign`
	// is configured entirely by this subset; the full server shares the
	// same fields. See SignerConfig.
	Signer SignerConfig `mapstructure:",squash"`

	CertOptions CertificateOptions `mapstructure:"cert_options"`

	// Branding optionally customizes the login page and web UI with
	// organization-specific information. All fields are optional; empty values
	// are treated as "no branding configured". See BrandingSettings.
	Branding BrandingSettings `mapstructure:"branding"`

	// FIPS steers the server toward FIPS 140-3 approved algorithms: the CA
	// key (checked at startup), client-submitted public keys (checked in
	// CertRequestService.Approve and again in server/signer), and the TLS
	// cipher/curve profile. A non-approved algorithm is a hard error when
	// this is in effect.
	//
	// A pointer so "unset" is distinguishable from "explicitly false":
	// unset falls back to whether the Go runtime is itself in FIPS 140-3
	// mode. Nil is the correct default, so no entry is needed in
	// defaults.yaml.
	FIPS *bool `mapstructure:"fips"`

	// MultiInstance declares that the server is part of a multi-instance
	// deployment (multiple ssoosshd processes sharing a database). When
	// enabled, certain checks are enforced (e.g., cookie_key must be
	// explicitly set) and behaviors adapt to account for cross-instance
	// message delivery (e.g., CertRequestService.Wait decodes wake-message
	// payloads). See docs/dev/multi-instance-safety-plan.md.
	MultiInstance bool `mapstructure:"multi_instance"`
}

// BrandingSettings configures optional branding for the login page and web UI.
// All fields are optional; empty values mean no branding is configured.
// This endpoint is unauthenticated, so only include values that are safe for
// public display.
type BrandingSettings struct {
	// OrgName is the organization name displayed in the web UI (e.g., "Acme Corp").
	// Empty disables organization-specific branding.
	OrgName string `mapstructure:"org_name"`

	// LogoPath is the filesystem path to the organization's logo image.
	// Empty disables the logo.
	//
	// Accepted types: PNG, JPEG, GIF, WebP, SVG. Maximum size: 1 MB
	// (maxLogoSize in server/controller/logo_image.go). The type is
	// determined from the file's own bytes, not its extension.
	//
	// Read once at startup and validated then, so a bad path fails the
	// server rather than producing a broken image later; replacing the file
	// needs a restart. Served from /api/branding/logo, always same-origin,
	// so no third-party host sees the unauthenticated login page's traffic.
	LogoPath string `mapstructure:"logo_path"`

	// LoginNotice is a plain-text message shown on the login page before authentication.
	// Empty disables the notice. Supports newlines for multi-line text.
	LoginNotice string `mapstructure:"login_notice"`
}

// FIPSEnabled reports whether FIPS steering is in effect. See
// fipsmode.Enabled.
func (c *Config) FIPSEnabled() bool {
	return fipsmode.Enabled(c.FIPS)
}

type QueueConfig struct {
	Logging GenericLogging `mapstructure:"logging"`
}

// Supported values for DBProvider.
const (
	DBProviderSqlite   = "sqlite"
	DBProviderPostgres = "postgres"
)

// DBProvider identifies which database backend to use. See DBProviderSqlite
// and DBProviderPostgres.
type DBProvider string

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
	Logging GenericLogging `mapstructure:"logging"`

	// RetryAttempts is the maximum number of connection attempts before giving up.
	// Zero or negative disables retries (will fail immediately on the first error).
	// Applies to both SQLite (typically succeeds immediately) and PostgreSQL
	// (may retry on transient network failures).
	RetryAttempts int `mapstructure:"retry_attempts"`

	// RetryInterval is the time to wait between connection retry attempts.
	// Only used if RetryAttempts > 1. Applies to both SQLite and PostgreSQL.
	RetryInterval time.Duration `mapstructure:"retry_interval"`

	// MaxOpenConns sets the maximum number of open connections to the database.
	// Zero means Go's default (typically 2). For file-backed SQLite, keep this
	// modest: writes serialize on the write lock regardless, so high counts
	// just queue without benefit. For PostgreSQL, scale with expected
	// concurrency. This is ignored for in-memory SQLite, which is always
	// forced to 1 (see bootstrap/db.go's onConnFn).
	MaxOpenConns int `mapstructure:"max_open_conns"`

	// MaxIdleConns sets the maximum number of idle connections held open.
	// Zero means Go's default (typically 2, same as MaxOpenConns). Keep this
	// proportionate to MaxOpenConns — they are not independent; a high
	// MaxOpenConns with a low MaxIdleConns causes expensive churn.
	MaxIdleConns int `mapstructure:"max_idle_conns"`

	// ConnMaxLifetime sets the maximum amount of time a connection can be
	// reused. Zero disables the limit (connections are reused indefinitely).
	// Useful for breaking stale connections in long-running deployments, but
	// typically not necessary with a well-behaved database server.
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

// LDAPConfig configures optional LDAP identity enrichment, looked up by the
// username resolved from OIDC (see docs/dev/ssoossh-context.md — "Which LDAP
// attributes become principals" is still an open question). Enabled false
// (the default) skips LDAP entirely; OIDC claims alone are sufficient.
//
// TODO: not yet consumed by service.AuthService — fields are a starting
// guess at what a reference deployment (lldap) needs.
type LDAPConfig struct {
	Enabled      bool           `mapstructure:"enabled"`
	URL          string         `mapstructure:"url"`
	BindDN       string         `mapstructure:"bind_dn"`
	BindPassword string         `mapstructure:"bind_password"`
	BaseDN       string         `mapstructure:"base_dn"`
	UserFilter   string         `mapstructure:"user_filter"`
	Logging      GenericLogging `mapstructure:"logging"`
}

// AdminConfig configures role-based authorization for administrative and
// auditor-scoped operations. All group names are optional OIDC group
// identifiers; empty disables the corresponding role. Authorization is
// evaluated from the session identity (Identity.Groups), which means the
// session lifetime is the admin revocation window: removing someone from an
// admin group in the identity provider takes effect at their next login.
type AdminConfig struct {
	// RequireGroup is the OIDC group a caller must belong to in order to
	// access admin-scoped operations (expiring enrollments, disabling users).
	// Empty disables admin operations entirely. Fails closed: no identity, no
	// group, or no configured group all deny.
	RequireGroup string `mapstructure:"require_group"`

	// AuditorGroup is the OIDC group a caller must belong to in order to
	// access auditor-scoped operations (viewing effective configuration,
	// cross-user certificate history). Auditor is a child role of admin, so
	// RequireGroup members hold auditor access regardless of this setting;
	// empty therefore narrows auditor operations to admins rather than
	// disabling them. Fails closed: no identity, or membership in neither
	// group, denies.
	AuditorGroup string `mapstructure:"auditor_group"`
}

// IsAdminEnabled reports whether admin authorization is configured
// (RequireGroup is non-empty).
func (a *AdminConfig) IsAdminEnabled() bool {
	return a.RequireGroup != ""
}

// IsAuditorEnabled reports whether auditor authorization is configured
// (AuditorGroup is non-empty).
func (a *AdminConfig) IsAuditorEnabled() bool {
	return a.AuditorGroup != ""
}

// GrantsAuditor reports whether an identity holding groups has
// auditor-level access. Admins are a superset of auditors: admin group
// membership grants access even when the auditor group is unconfigured, so
// leaving auditor_group unset narrows auditor operations to admins rather
// than locking everyone out. Fails closed: empty groups, or membership in
// neither configured group, denies. The single authority for this rule —
// middleware.AuditorAuthMiddleware and every auditor-visible read go
// through it.
func (a *AdminConfig) GrantsAuditor(groups []string) bool {
	if a.IsAdminEnabled() && containsGroup(groups, a.RequireGroup) {
		return true
	}
	return a.IsAuditorEnabled() && containsGroup(groups, a.AuditorGroup)
}

// containsGroup reports whether needle is in haystack. An empty needle
// never matches, so an unconfigured group cannot accidentally authorize a
// caller.
func containsGroup(haystack []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
