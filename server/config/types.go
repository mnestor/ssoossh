package config

import (
	"time"

	"github.com/mnestor/ssoossh/internal/fipsmode"
)

// Config is the root ssoosshd configuration, populated from defaults.yaml
// and the user's config file via NewConfig.
type Config struct {
	// Logging configures the main application log.
	Logging AppLogging `mapstructure:"logging"`

	// Traces enables OpenTelemetry tracing, adding the otelgin middleware to
	// the router. Exporter endpoints come from the standard OTEL_*
	// environment variables.
	Traces bool `mapstructure:"traces" default:"false"`

	// Metrics enables OpenTelemetry metrics. Exporter endpoints come from
	// the standard OTEL_* environment variables.
	Metrics bool `mapstructure:"metrics" default:"false"`

	// Production selects gin's release mode rather than debug mode, and
	// gates the development-only escape hatches: notably
	// http.rate_limit_disable_for_dev is honored only when this is false, so
	// rate limiting cannot be switched off in production.
	Production bool `mapstructure:"production" default:"true"`

	// DB configures the database connection, pooling, retries, and query
	// logging.
	DB DB `mapstructure:"db"`

	// HTTP configures the listener, TLS, sessions, and rate limiting.
	HTTP HTTPSettings `mapstructure:"http"`

	// Queue carries the log destination for the internal message queue.
	Queue QueueConfig `mapstructure:"queue"`

	// AuthConfig configures OAuth/OIDC authentication for the server. See
	// OAuthConfig for details on provider URL, scopes, and field mapping
	// from OIDC claims to ssoossh identity fields (username, groups).
	AuthConfig OAuthConfig `mapstructure:"authentication"`

	// LDAP optionally enriches the OIDC identity with directory attributes.
	// See LDAPConfig.
	LDAP LDAPConfig `mapstructure:"ldap"`

	// Admin configures administrative and auditor access. Both group names
	// are optional; empty disables the corresponding role.
	Admin AdminConfig `mapstructure:"admin"`

	// Audit configures the administrative audit stream: how long the
	// database copy is kept and where the durable log is shipped. See
	// AuditConfig.
	Audit AuditConfig `mapstructure:"audit"`

	// Signer carries everything the signer needs (the CA key and the
	// broker), squashed so its YAML keys stay top-level. `ssoosshd sign`
	// is configured entirely by this subset; the full server shares the
	// same fields. See SignerConfig.
	Signer SignerConfig `mapstructure:",squash"`

	// CertOptions holds the issuance policy for each certificate type.
	CertOptions CertificateOptions `mapstructure:"cert_options"`

	// Mail configures outbound email notifications: the relay, the sender,
	// and the local template overrides. Disabled by default; see
	// docs/operations/email-notifications.md.
	Mail MailConfig `mapstructure:"mail"`

	// Branding customizes the login page and web UI. All fields are
	// optional; empty values mean no branding is configured.
	Branding BrandingSettings `mapstructure:"branding"`

	// FIPS steers the server toward FIPS 140-3 approved algorithms: the CA
	// key (checked at startup), client-submitted public keys (checked in
	// when a request is approved and again by the signer), and the TLS
	// cipher/curve profile. A non-approved algorithm is a hard error when
	// this is in effect.
	//
	// Unset is distinguishable from an explicit false: unset follows the Go
	// runtime's own FIPS 140-3 mode, crypto/fips140.Enabled(), which is why
	// this key ships with no value rather than set to false.
	//
	//	fips: true
	FIPS *bool `mapstructure:"fips"`

	// MultiInstance declares that the server is part of a multi-instance
	// deployment (multiple ssoosshd processes sharing a database). When
	// enabled, certain checks are enforced (e.g., cookie_key must be
	// explicitly set) and behaviors adapt to account for cross-instance
	// message delivery (a client waiting on one instance is woken by another)
	// payloads). See docs/dev/multi-instance-safety-plan.md.
	MultiInstance bool `mapstructure:"multi_instance" default:"false"`
}

// BrandingSettings configures optional branding for the login page and web UI.
// All fields are optional; empty values mean no branding is configured.
// This endpoint is unauthenticated, so only include values that are safe for
// public display.
type BrandingSettings struct {
	// OrgName is the organization name displayed in the web UI (e.g., "Acme Corp").
	// Empty disables organization-specific branding.
	OrgName string `mapstructure:"org_name" default:""`

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
	LogoPath string `mapstructure:"logo_path" default:""`

	// LoginNotice is a plain-text message shown on the login page before authentication.
	// Empty disables the notice. Supports newlines for multi-line text.
	LoginNotice string `mapstructure:"login_notice" default:""`
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
	Provider DBProvider `mapstructure:"provider" default:"sqlite"`

	// Connection is the connection string / DSN. For SQLite this is a file
	// path (or ":memory:" for in-memory); for PostgreSQL a standard postgres://
	// URL or keyword string.
	Connection string `mapstructure:"connection_string" default:"ssoossh.db"`

	// Logging configures which database queries are logged and where they
	// are written. See DBLogging for detailed options.
	Logging GenericLogging `mapstructure:"logging" default_level:"WARN" default_add_source:"false" default_log_json:"false"`

	// RetryAttempts is the maximum number of connection attempts before giving up.
	// Zero or negative disables retries (will fail immediately on the first error).
	// Applies to both SQLite (typically succeeds immediately) and PostgreSQL
	// (may retry on transient network failures).
	RetryAttempts int `mapstructure:"retry_attempts" default:"3"`

	// RetryInterval is the time to wait between connection retry attempts.
	// Only used if RetryAttempts > 1. Applies to both SQLite and PostgreSQL.
	RetryInterval time.Duration `mapstructure:"retry_interval" default:"3s"`

	// MaxOpenConns sets the maximum number of open connections to the database.
	// Zero means Go's default (typically 2). For file-backed SQLite, keep this
	// modest: writes serialize on the write lock regardless, so high counts
	// just queue without benefit. For PostgreSQL, scale with expected
	// concurrency. This is ignored for in-memory SQLite, which is always
	// forced to 1.
	MaxOpenConns int `mapstructure:"max_open_conns" default:"10"`

	// MaxIdleConns sets the maximum number of idle connections held open.
	// Zero means Go's default (typically 2, same as MaxOpenConns). Keep this
	// proportionate to MaxOpenConns — they are not independent; a high
	// MaxOpenConns with a low MaxIdleConns causes expensive churn.
	MaxIdleConns int `mapstructure:"max_idle_conns" default:"5"`

	// ConnMaxLifetime sets the maximum amount of time a connection can be
	// reused. Zero disables the limit (connections are reused indefinitely).
	// Useful for breaking stale connections in long-running deployments, but
	// typically not necessary with a well-behaved database server.
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime" default:"0"`
}

// LDAPConfig configures optional LDAP identity enrichment and directory
// sync. Enabled false (the default) skips LDAP entirely; OIDC claims alone
// are sufficient for every basic operation.
//
// LDAP is enrichment, never a requirement. If directory data is available a
// user gets more — extra principals, fresher groups, auto-disable coverage.
// If it is not, everything still works. Login therefore **fails open**: an
// LDAP error during the callback logs to the LDAP destination and proceeds
// with the OIDC-only identity. There is deliberately no `required` knob.
//
// See docs/operations/ldap.md.
type LDAPConfig struct {
	// Enabled turns on the login-time lookup and the background sync.
	Enabled bool `mapstructure:"enabled" default:"false"`

	// URL is the directory server to connect to, e.g.
	// ldaps://ldap.example.net.
	URL string `mapstructure:"url" default:""`

	// BindDN is the DN to bind as for the search. Together with
	// BindPassword this is the implicit "simple" bind mechanism; a
	// keytab-based SASL GSSAPI alternative is tracked separately and slots
	// in beside these keys rather than replacing them.
	BindDN string `mapstructure:"bind_dn" default:""`

	// BindPassword is the password for BindDN.
	BindPassword string `mapstructure:"bind_password" default:""`

	// BaseDN is the search base for the user lookup, and the default base
	// for field searches that do not name their own.
	BaseDN string `mapstructure:"base_dn" default:""`

	// UserFilter is a Go template over the OIDC identity, with the same
	// idiom as key ID templates: {{.Username}}, {{.Email}}, {{.Subject}},
	// and {{.Extra.<name>}}. Values are RFC 4515 escaped during rendering
	// and the operator cannot opt out, because a preferred_username
	// containing * or ) is otherwise filter injection.
	UserFilter string `mapstructure:"user_filter" default:""`

	// Fields maps destinations to the directory sources that populate them,
	// mirroring OAuthFields with attribute names instead of claim names.
	//
	// The reserved names are other_accounts, service_accounts and groups.
	// Any other key is an extra template field, captured into the same
	// contract as OAuthFields.Extra: reachable as {{.Extra.<name>}}, stored
	// empty when absent, and never a reason for login to fail. There is no
	// separate extra sub-map; LDAP enrichment is extra by definition.
	//
	// The merge rule is per field: a configured LDAP field (any attribute
	// or searches) wins over the OIDC value, and an unconfigured one leaves
	// the OIDC value untouched. Override rather than union, because union
	// makes it impossible to retire a stale principal from only one source.
	// Groups are the exception — both sources persist side by side.
	//
	// username, email and subject are rejected here: the subject keys the
	// user row, the username is what lookups are keyed by, and the OIDC
	// email claim is the source of truth for users.email.
	Fields map[string]LDAPField `mapstructure:"fields"`

	// GroupNameAttribute names the attribute to read a group's name from
	// when a group search is used. When groups arrive as DNs (memberOf,
	// the common case), each DN is reduced to its first RDN value — the CN
	// — and a value that does not parse as a DN is kept as-is. The reduced
	// name is what must match the configured group names.
	GroupNameAttribute string `mapstructure:"group_name_attribute" default:""`

	// Sync configures the background directory sync. See LDAPSync.
	Sync LDAPSync `mapstructure:"sync"`

	// Limits caps what one directory can push into the database. See
	// LDAPLimits.
	Limits LDAPLimits `mapstructure:"limits"`

	// Timeout bounds each directory operation. The login callback is on a
	// user-facing path, so this is the entire latency cost enrichment can
	// add to a login.
	Timeout time.Duration `mapstructure:"timeout,string" default:"5s"`

	// StartTLS upgrades a plain ldap:// connection to TLS. Irrelevant for
	// ldaps:// URLs, which are TLS from the start.
	StartTLS bool `mapstructure:"start_tls" default:"false"`

	// TLSCA is a PEM bundle path to verify the directory's certificate
	// against. Empty uses the system roots.
	TLSCA string `mapstructure:"tls_ca" default:""`

	// TLSInsecureSkipVerify disables certificate verification. A homelab
	// escape hatch, logged loudly at startup; it makes the connection
	// trivially interceptable and has no place in production.
	TLSInsecureSkipVerify bool `mapstructure:"tls_insecure_skip_verify" default:"false"`

	// Logging is an independent log destination for LDAP activity, routed
	// by a "type=ldap" attribute.
	Logging GenericLogging `mapstructure:"logging"`
}

// AdminConfig configures role-based authorization for administrative and
// auditor-scoped operations. All group names are optional OIDC group
// identifiers; empty disables the corresponding role. Authorization is
// evaluated from the session identity (Identity.Groups), which means the
// session lifetime is the admin revocation window: removing someone from an
// admin group in the identity provider takes effect at their next login.
type AdminConfig struct {
	// RequireGroup is the OIDC group a caller must belong to in order to
	// access admin-scoped operations (re-enabling users, reassigning any
	// enrollment), plus everything the SOC and auditor roles below can do.
	// Empty disables admin operations entirely. Fails closed: no identity, no
	// group, or no configured group all deny.
	RequireGroup string `mapstructure:"require_group" default:""`

	// SOCGroup is the OIDC group a caller must belong to in order to access
	// SOC-scoped containment operations: disabling users and expiring
	// enrollments. SOC is a child role of admin, so RequireGroup members hold
	// SOC access regardless of this setting; empty therefore narrows SOC
	// operations to admins rather than disabling them. SOC members also hold
	// auditor access (they need the directory and enrollment lists to find
	// what to contain), but deliberately not the restorative or ownership
	// operations — re-enabling a user and reassigning an enrollment stay
	// admin-only. Fails closed: no identity, or membership in neither group,
	// denies.
	SOCGroup string `mapstructure:"soc_group" default:""`

	// AuditorGroup is the OIDC group a caller must belong to in order to
	// access auditor-scoped operations (viewing effective configuration,
	// cross-user certificate history). Auditor is a child role of both admin
	// and SOC, so RequireGroup and SOCGroup members hold auditor access
	// regardless of this setting; empty therefore narrows auditor operations
	// to those roles rather than disabling them. Fails closed: no identity,
	// or membership in no granting group, denies.
	AuditorGroup string `mapstructure:"auditor_group" default:""`

	// DisableGracePeriod is how long after a user is disabled before their
	// service enrollments expire. This gives running services time to notice
	// and rotate credentials before the certificates stop working. After this
	// duration expires (see service.SweepDisabledUserEnrollments), no new
	// certificates can be issued from the enrollment.
	DisableGracePeriod time.Duration `mapstructure:"disable_grace_period" default:"168h"`

	// ContactEmail is the email address shown on the account-disabled page
	// so a disabled user can contact support. Empty disables the display
	// (no mailto link on the page).
	ContactEmail string `mapstructure:"contact_email" default:""`

	// DisabledMessage is free-text shown on the account-disabled page below
	// the contact email. Useful for explaining why the account was disabled
	// or what the user should do next.
	DisabledMessage string `mapstructure:"disabled_message" default:""`
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

// IsSOCEnabled reports whether SOC authorization is configured
// (SOCGroup is non-empty).
func (a *AdminConfig) IsSOCEnabled() bool {
	return a.SOCGroup != ""
}

// GrantsSOC reports whether an identity holding groups has SOC-level
// access (containment operations: disabling users, expiring enrollments).
// Admins are a superset of SOC: admin group membership grants access even
// when the SOC group is unconfigured, so leaving soc_group unset narrows
// SOC operations to admins rather than locking everyone out. Fails closed:
// empty groups, or membership in neither configured group, denies. The
// single authority for this rule — middleware.SOCAuthMiddleware goes
// through it.
func (a *AdminConfig) GrantsSOC(groups []string) bool {
	if a.IsAdminEnabled() && containsGroup(groups, a.RequireGroup) {
		return true
	}
	return a.IsSOCEnabled() && containsGroup(groups, a.SOCGroup)
}

// GrantsAuditor reports whether an identity holding groups has
// auditor-level access. Admins and SOC members are supersets of auditors
// (via GrantsSOC): membership in either group grants access even when the
// auditor group is unconfigured, so leaving auditor_group unset narrows
// auditor operations to those roles rather than locking everyone out.
// Fails closed: empty groups, or membership in no configured granting
// group, denies. The single authority for this rule —
// middleware.AuditorAuthMiddleware and every auditor-visible read go
// through it.
func (a *AdminConfig) GrantsAuditor(groups []string) bool {
	if a.GrantsSOC(groups) {
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

// AuditConfig configures the append-only administrative audit stream: an
// ordered record of who did what, to whom, and when. See
// docs/proposals/audit-log.md.
//
// The stream has two sinks and they are not equals. The Logging destination
// below is the archive: one JSON line per event, unconditionally, for an
// external log system to ship and retain. The database table is a bounded
// cache that exists only to serve the web UI's recent-history views, which
// is why it is pruned and deliberately not searchable beyond the two
// indexed subject columns.
type AuditConfig struct {
	// Retention is how long an event stays in the database table. The
	// scheduled sweep deletes rows older than this. It bounds the UI's
	// history, not the deployment's: the shipped log is the archive, so a
	// short window here loses nothing that matters. Zero disables
	// age-based pruning, leaving MaxRows as the only bound.
	Retention time.Duration `mapstructure:"retention,string" default:"1440h"`

	// MaxRows caps the table as a safety valve behind Retention, so a burst
	// of events cannot grow it without bound inside the retention window.
	// The same sweep deletes oldest-first past the cap. The default is
	// deliberately high: this exists to bound pathology, not to be the
	// operative limit. Zero disables the cap.
	MaxRows int64 `mapstructure:"max_rows" default:"1000000"`

	// SweepInterval is how often the retention sweep runs. Pruning is not
	// urgent — the window is measured in weeks — so this is measured in
	// hours rather than minutes.
	SweepInterval time.Duration `mapstructure:"sweep_interval,string" default:"1h"`

	// Logging is the durable export: a dedicated destination that receives
	// one JSON line per event, routed by a type=audit attribute like the
	// LDAP and queue logs. Set its filename to split it into its own
	// rotating file; leave it unset and the events still reach the general
	// log. A deployment that configures nothing here loses the archive, not
	// the audit trail's correctness.
	Logging GenericLogging `mapstructure:"logging" default_log_json:"true"`
}
