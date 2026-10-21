package config

import (
	"fmt"
	"net/mail"
	"slices"
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

	// NSS serves POSIX accounts and groups from the directory to the
	// ssoossh-nss daemon on each host, authenticated by fleet API keys.
	// Disabled by default. See NSSConfig.
	NSS NSSConfig `mapstructure:"nss"`

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
	// https://mnestor.github.io/ssoossh/operations/email-notifications/.
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

	// Version configures what the unauthenticated /api/version endpoint
	// discloses about the running build. See VersionConfig.
	Version VersionConfig `mapstructure:"version"`

	// unknownKeys are the keys the config file set that no field above
	// declares, found while Load decoded it. Unexported, so the decoder
	// never fills it from a file. See UnknownKeys.
	unknownKeys []UnknownKey

	// file is the config file Load read, and schema the ldap.schema
	// preset it layered beneath it: what EffectiveSources needs to say
	// which layer set each value. Unexported for the same reason.
	file   string
	schema string
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

	// SupportEmail is the address of whoever answers for this deployment —
	// the local help desk, not the project. Empty leaves the web UI
	// pointing at its defaults: "contact your administrator" on the login
	// page and the account-disabled page, and the project's GitHub issue
	// tracker in the footer.
	//
	// Set it and all three become a mailto: link to this address. One
	// setting covers them because it is one question — "who do I ask about
	// this deployment?" — and a second key would let an operator answer it
	// in one place and forget the other, which is the failure this exists
	// to prevent. admin.contact_email was that second key; it is still read
	// for the account-disabled page when this is unset.
	//
	// It is served by the unauthenticated /api/branding endpoint and
	// rendered on the login page, so it is public: use the shared support
	// address, never a personal one. It is parsed at startup and a value
	// that is not an address fails the server rather than producing a dead
	// link on the one page a locked-out user can reach.
	SupportEmail string `mapstructure:"support_email" default:"" example:"\"support@example.com\""`

	// SupportLabel is the link text for SupportEmail, e.g. "the IT service
	// desk". Ignored when SupportEmail is unset.
	//
	// Empty falls back to whatever reads best in each place: the address
	// itself on the login page, where it sits in a sentence and is a
	// complete instruction on its own, and "Contact support" in the footer,
	// where a bare address would be noise in a row of links.
	SupportLabel string `mapstructure:"support_label" default:"" example:"\"the IT service desk\""`
}

// Validate checks the branding values that can be wrong in a way the web UI
// cannot recover from.
//
// Only the support address qualifies today: it becomes an href on the login
// page, which is the one page a locked-out user can reach, and a typo there
// produces a link that fails silently in their mail client rather than
// anywhere an operator would see it.
func (b *BrandingSettings) Validate() error {
	if b.SupportEmail == "" {
		return nil
	}
	if _, err := mail.ParseAddress(b.SupportEmail); err != nil {
		return fmt.Errorf("branding.support_email %q is not a valid email address: %w", b.SupportEmail, err)
	}
	return nil
}

// SupportContact is who a person is told to ask about this deployment:
// branding.support_email and its label, or, only when that is unset, the
// superseded admin.contact_email with no label. Both empty means no
// contact is configured, and the caller falls back to "contact your
// administrator".
func (c *Config) SupportContact() (address, label string) {
	if c.Branding.SupportEmail != "" {
		return c.Branding.SupportEmail, c.Branding.SupportLabel
	}
	return c.Admin.ContactEmail, ""
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
	Connection string `mapstructure:"connection_string" default:"ssoossh.db" secret:"true"`

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
// See https://mnestor.github.io/ssoossh/operations/ldap/.
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
	BindPassword string `mapstructure:"bind_password" default:"" secret:"true"`

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
	// The reserved names are other_accounts, service_accounts, groups and
	// name. Any other key is an extra template field, captured into the same
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
	// email claim is the source of truth for users.email. The person's
	// display name is not in that set — see the name destination above.
	Fields map[string]LDAPField `mapstructure:"fields"`

	// Schema selects a preset for the keys whose right value depends on the
	// directory: ldap.id_attribute, nss.user_filter, nss.group_filter,
	// nss.disabled_filter and nss.attributes. One of rfc2307, rfc2307bis,
	// active-directory, lldap or freeipa; any other value fails startup.
	// Empty (the default) applies no preset, and every key keeps its own
	// default. Each preset's values, and the source each came from, are
	// listed at https://mnestor.github.io/ssoossh/operations/ldap-schema-presets/.
	//
	// A preset sits between the built-in defaults and the config file: any
	// key the file sets beats the preset, so a preset is a starting point
	// rather than a lock. Setting nss.attributes.shell to an attribute of
	// your own with active-directory selected keeps every other
	// active-directory value.
	//
	// Every preset sets ldap.id_attribute. On a database that already holds
	// directory ids read from another attribute (or an earlier
	// ldap.id_attribute), set ldap.id_attribute explicitly to the attribute
	// in use, or start from a fresh database: there is no migration tool
	// yet, and the NSS directory treats an entry whose id changed at the
	// same DN as a new entry where the old one used to be.
	Schema string `mapstructure:"schema" default:"" example:"rfc2307bis"`

	// IDAttribute names the attribute holding the entry's unique, immutable
	// identifier, and is what makes a directory rename survivable. Empty
	// (the default) keeps the DN-then-filter resolution described below.
	// A preset from ldap.schema may set this.
	//
	// Every directory has one under a different name: entryUUID on OpenLDAP
	// and 389 Directory Server (RFC 4530), objectGUID on Active Directory,
	// ipaUniqueID on FreeIPA. None of them is guessable from the others, so
	// there is no useful default beyond off.
	//
	// With it set, the sync resolves an entry by ID first, falling back to
	// the stored DN and then to user_filter. That ordering is the point: a
	// DN changes when someone moves between OUs and user_filter stops
	// matching when they are renamed, so without an ID a rename looks
	// exactly like a deletion and walks the account toward the
	// sync.disable_after auto-disable. The ID is read at login, stored on
	// user_ldap.directory_id, and never used for authorization.
	//
	// Binary-valued attributes (objectGUID) are stored as their hex form
	// behind a 0x marker, and searched with the RFC 4515 escape of each
	// byte.
	//
	// Changing this on a database that already holds directory ids, by
	// hand or by picking an ldap.schema preset, makes every stored id stop
	// matching: there is no migration tool yet. Login enrichment recovers
	// by DN and logs a warning, but the NSS directory treats an entry whose
	// id changed at the same DN as a new entry. Keep the attribute the ids
	// were read from set explicitly, or start from a fresh database.
	IDAttribute string `mapstructure:"id_attribute" default:"" example:"entryUUID"`

	// GroupNameAttribute names the attribute to read a group's name from
	// when a group search is used. When groups arrive as DNs (memberOf,
	// the common case), each DN is reduced to its first RDN value — the CN
	// — and a value that does not parse as a DN is kept as-is. The reduced
	// name is what must match the configured group names.
	GroupNameAttribute string `mapstructure:"group_name_attribute" default:""`

	// AuthoritativeRoleGroups lets the directory take a role away between
	// logins. Off by default: roles then come from the OIDC groups claim
	// alone, read at login, and the session lifetime is the revocation
	// window for a role.
	//
	// With it on, a role group counts for a directory-linked user only
	// while the latest directory read for that user still lists it. The
	// role groups are exactly admin.require_group, admin.soc_group,
	// admin.auditor_group and admin.nss_key_group (fleet key admin). The
	// background sync can therefore remove someone's admin, SOC, auditor
	// or fleet key admin role between logins, effective on their next
	// request; a fresh login is narrowed the same way, and so is who
	// counts as an admin for the admin email.
	//
	// It narrows only. The directory never grants a role: a group the
	// directory lists but the OIDC claim does not is never added, so a
	// person needs the group in both places to hold the role.
	//
	// "Linked" means the server has resolved a directory entry for the
	// user (a user_ldap row with a DN or a directory id). A user with no
	// entry, one never matched, or any user while LDAP is off is never
	// narrowed. "The latest directory read" is the LDAP group rows the
	// last successful login lookup or sync pass wrote. A read that failed
	// (the directory unreachable, a search error) rewrites nothing, so an
	// outage never takes a role away. A user the sync finds missing from
	// the directory keeps the rows of their last successful read;
	// ldap.sync.disable_after is what acts on a missing entry.
	//
	// Names are compared exactly, the way the role checks compare them.
	// Directory groups are stored reduced to their CN (a memberOf DN
	// becomes its first RDN value), so each role group name must be the
	// same string in the identity provider's groups claim and as the
	// directory group's CN. An identity provider that emits full DNs
	// cannot be used with this setting: no stored CN would ever equal the
	// configured DN, and every role would be removed.
	//
	// Only the role groups are narrowed. Lifetime policy group
	// conditions, cert_options require gates and the groups that manage
	// individual NSS keys are not affected, unless they name a role group
	// itself: removing a role group from the identity removes it for
	// every check that reads the identity's groups, which can only
	// narrow.
	//
	// A truncated read counts as a successful one. Keep
	// ldap.limits.max_values_per_attribute and
	// ldap.limits.max_entries_per_search above the largest group count a
	// role holder has, or a role group cut off by the cap is removed.
	//
	// Requires ldap.enabled and a mapped ldap.fields.groups; startup fails
	// otherwise.
	AuthoritativeRoleGroups bool `mapstructure:"authoritative_role_groups" default:"false"`

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
// ldap.authoritative_role_groups adds a narrower path for these groups: the
// directory sync can take one away from a linked user on their next
// request, and never grant one.
type AdminConfig struct {
	// RequireGroup is the OIDC group a caller must belong to in order to
	// access admin-scoped operations (the effective configuration,
	// diagnostics, claims echo, the directory status, sync and probe), plus
	// everything the SOC and auditor
	// roles below can do. Empty disables admin operations entirely. Fails closed: no
	// identity, no group, or no configured group all deny.
	RequireGroup string `mapstructure:"require_group" default:""`

	// SOCGroup is the OIDC group a caller must belong to in order to access
	// SOC-scoped account operations: disabling and re-enabling users and
	// expiring enrollments. Whoever can contain an account can also release
	// it, so a SOC disable of an admin never needs an admin to undo it. SOC
	// is a child role of admin, so RequireGroup members hold SOC access
	// regardless of this setting; empty therefore narrows SOC operations to
	// admins rather than disabling them. SOC members also hold auditor
	// access (they need the directory and enrollment lists to find what to
	// contain). Fails closed: no identity, or membership in neither group,
	// denies.
	SOCGroup string `mapstructure:"soc_group" default:""`

	// AuditorGroup is the OIDC group a caller must belong to in order to
	// access auditor-scoped operations (cross-user certificate history, the
	// user and service code directories, the audit feed). Auditor is a child
	// role of both admin
	// and SOC, so RequireGroup and SOCGroup members hold auditor access
	// regardless of this setting; empty therefore narrows auditor operations
	// to those roles rather than disabling them. Fails closed: no identity,
	// or membership in no granting group, denies.
	AuditorGroup string `mapstructure:"auditor_group" default:""`

	// NSSKeyGroup is the OIDC group a caller must belong to in order to
	// administer every NSS fleet API key: create keys, rotate, revoke and
	// edit any of them, and assign the group that manages a key. Admins
	// hold this access regardless of this setting, so empty narrows key
	// administration to admins rather than disabling it. Members of a
	// key's own group manage that key without this role, and SOC and
	// auditors keep their own revoke and read access. Fails closed: no
	// identity, or membership in neither group, denies.
	NSSKeyGroup string `mapstructure:"nss_key_group" default:""`

	// ContactEmail is superseded by branding.support_email, which is the one
	// setting for who to ask about this deployment and is shown on the
	// account-disabled page along with the login page and the footer. It is
	// still read, but only when branding.support_email is unset, so an
	// existing configuration keeps its contact; the server logs a warning
	// at startup while it is the one in use.
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

// IsNSSKeyAdminEnabled reports whether a fleet key administration group
// is configured (NSSKeyGroup is non-empty).
func (a *AdminConfig) IsNSSKeyAdminEnabled() bool {
	return a.NSSKeyGroup != ""
}

// GrantsAdmin reports whether an identity holding groups has admin-level
// access. Admin is the root role: it is granted only by membership in the
// configured admin group, and nothing else confers it. Fails closed: an
// unconfigured admin group, or empty groups, denies. The single authority
// for this rule — middleware.AdminAuthMiddleware goes through it.
func (a *AdminConfig) GrantsAdmin(groups []string) bool {
	return a.IsAdminEnabled() && containsGroup(groups, a.RequireGroup)
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
	if a.GrantsAdmin(groups) {
		return true
	}
	return a.IsSOCEnabled() && containsGroup(groups, a.SOCGroup)
}

// GrantsNSSKeyAdmin reports whether an identity holding groups may
// administer every NSS fleet API key. Admins are a superset: admin group
// membership grants access even when nss_key_group is unconfigured, so
// leaving it unset narrows key administration to admins rather than
// locking everyone out. SOC and auditor membership never grant it. Fails
// closed: empty groups, or membership in neither configured group, denies.
// The single authority for this rule.
func (a *AdminConfig) GrantsNSSKeyAdmin(groups []string) bool {
	if a.GrantsAdmin(groups) {
		return true
	}
	return a.IsNSSKeyAdminEnabled() && containsGroup(groups, a.NSSKeyGroup)
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

// IsRoleGroup reports whether name is one of the configured role groups:
// require_group (admin), soc_group, auditor_group or nss_key_group (fleet
// key admin). Compared the way the role checks compare, exactly, and an
// empty name or an unconfigured role never matches.
func (a *AdminConfig) IsRoleGroup(name string) bool {
	return containsGroup([]string{a.RequireGroup, a.SOCGroup, a.AuditorGroup, a.NSSKeyGroup}, name)
}

// HoldsRoleGroup reports whether groups carries any configured role group,
// which is the only case NarrowRoleGroups can change.
func (a *AdminConfig) HoldsRoleGroup(groups []string) bool {
	return slices.ContainsFunc(groups, a.IsRoleGroup)
}

// NarrowRoleGroups returns groups without each role group that directory
// does not also list, for ldap.authoritative_role_groups. Every other group
// is kept, and nothing in directory is ever added, so the result can only
// be narrower than groups. A nil directory removes every role group, which
// is the answer for a check that could not read the directory rows.
// Always a new slice, so a caller can keep the original.
func (a *AdminConfig) NarrowRoleGroups(groups, directory []string) []string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		if a.IsRoleGroup(g) && !containsGroup(directory, g) {
			continue
		}
		out = append(out, g)
	}
	return out
}

// containsGroup reports whether needle is in haystack. An empty needle
// never matches, so an unconfigured group cannot accidentally authorize a
// caller.
//
// The comparison is exact, and haystack holds the group values the identity
// provider emitted, verbatim — see service.AuthService's groups claim
// handling, which does not rewrite them, and note that LDAP group values
// never reach this list at all (they are persisted, never merged into the
// session identity; ldap.authoritative_role_groups compares the reduced
// directory names against a role group with this same function, and can
// only remove one). So a provider that emits bare names has to be
// configured with bare names, and one that emits full DNs
// ("CN=admins,OU=Groups,DC=example,DC=com") has to be configured with the
// full DN.
//
// Deliberately not reduced to a CN here, however tempting the symmetry with
// service.reduceGroupName on the directory side. Two DNs may share a CN —
// "CN=admins,OU=Platform" and "CN=admins,OU=Contractors" are different
// groups — and matching on the CN alone would silently grant one the
// other's access. An exact comparison can only fail closed.
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
// https://mnestor.github.io/ssoossh/operations/audit-log/.
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
	//
	// One thing does read the table back: the directory sync decides who
	// to email when it disables an admin from the logins recorded here
	// over the last 60 days. A retention below that, or a MaxRows that
	// trims those logins, shortens that window, and the sync logs the
	// window it actually had.
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
	//
	// Unconditional means unconditional: audit events are emitted at INFO
	// and logging.level ships at WARN, so this destination is held at an
	// INFO floor whenever it has neither a filename nor a level of its own.
	// Setting a level here overrides that floor, which is the point —
	// turning the trail down is a deliberate choice, not one to arrive at
	// by leaving this block empty.
	Logging GenericLogging `mapstructure:"logging" default_log_json:"true"`
}
