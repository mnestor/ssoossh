package config

import (
	"fmt"
	"io"
	"net/netip"
	"reflect"
	"strconv"
	"strings"

	// text/template, not html/template: the gecos template renders a passwd
	// field for getent, never HTML, and HTML escaping would corrupt names
	// such as "O'Brien" instead of preventing anything.
	// nosemgrep: go.lang.security.audit.xss.import-text-template.import-text-template
	"text/template"
	"time"
)

// AutoPrivateGroupsMode selects how an NSS account's primary group is
// chosen: from the directory, or as a private group of its own. See
// NSSConfig.AutoPrivateGroups.
type AutoPrivateGroupsMode string

const (
	// AutoPrivateGroupsHybrid gives an account a private group only when
	// its uid and gid attributes are equal and no directory group holds
	// that gid; every other account keeps the directory's gid. The
	// default.
	AutoPrivateGroupsHybrid AutoPrivateGroupsMode = "hybrid"

	// AutoPrivateGroupsOn gives every account a private group named after
	// it with a gid equal to its uid, ignoring the directory's gid
	// attribute. An account whose uid is already a directory group's gid
	// is not served rather than shadowing that group.
	AutoPrivateGroupsOn AutoPrivateGroupsMode = "true"

	// AutoPrivateGroupsOff always uses the directory's gid attribute and
	// creates no private groups.
	AutoPrivateGroupsOff AutoPrivateGroupsMode = "false"
)

// Valid reports whether m is one of the three modes.
func (m AutoPrivateGroupsMode) Valid() bool {
	return m == AutoPrivateGroupsHybrid || m == AutoPrivateGroupsOn || m == AutoPrivateGroupsOff
}

// autoPrivateGroupsHook keeps an unquoted `auto_private_groups: true` (or
// false) meaning what it says. YAML reads the bare word as a boolean, and
// viper's weakly typed decoding would then turn it into "1" or "0", which
// matches no mode.
func autoPrivateGroupsHook(from, to reflect.Type, data any) (any, error) {
	if to != reflect.TypeFor[AutoPrivateGroupsMode]() || from.Kind() != reflect.Bool {
		return data, nil
	}
	b, ok := data.(bool)
	if !ok {
		// not covered: from.Kind() == Bool is checked above, so the type
		// assertion cannot fail.
		return data, nil
	}
	return strconv.FormatBool(b), nil
}

// NSSConfig configures the NSS directory: POSIX accounts and groups read
// from LDAP and served over /api/nss/* to the ssoosshd-nss daemon on each
// host, so a host resolves the same names and numbers the certificates
// are issued for. Disabled by default. See
// docs/proposals/nss-directory/README.md.
//
// Every account and group comes from the directory, people and shared or
// service accounts alike, so NSS requires ldap.enabled; there is no OIDC
// claim source for POSIX attributes. The directory connection, base DN and
// ldap.id_attribute are the ldap block's.
type NSSConfig struct {
	// Enabled turns the NSS directory on: the lookup endpoints, the fleet
	// key administration, the directory sync of POSIX entries and the
	// shell self-service. Requires ldap.enabled; a configuration with this
	// on and LDAP off fails at startup.
	Enabled bool `mapstructure:"enabled" default:"false"`

	// UserFilter is the LDAP filter that selects POSIX accounts under
	// ldap.base_dn. A plain RFC 4515 filter in parentheses, not a template:
	// the sync enumerates it with paged searches, so ldap.limits does not
	// cap it, and an on-demand lookup ANDs it with an equality on the name
	// or uid attribute. Required when nss.enabled is on; its syntax is
	// checked when the server starts.
	UserFilter string `mapstructure:"user_filter" default:"(objectClass=posixAccount)"`

	// GroupFilter is the LDAP filter that selects POSIX groups under
	// ldap.base_dn, used the same way as UserFilter. Required when
	// nss.enabled is on.
	GroupFilter string `mapstructure:"group_filter" default:"(objectClass=posixGroup)"`

	// DisabledFilter is an LDAP filter an account's entry is tested
	// against on every read: a match marks the account disabled by the
	// directory itself, which hosts then lock. For example
	// (nsAccountLock=TRUE) on 389-ds and FreeIPA, or
	// (userAccountControl:1.2.840.113556.1.4.803:=2) on Active Directory.
	// Empty means the directory never disables an account; a person's
	// ssoossh disable still does.
	DisabledFilter string `mapstructure:"disabled_filter" default:"" example:"\"(nsAccountLock=TRUE)\""`

	// SyncInterval is how often the incremental sync runs: it reads only
	// the accounts and groups whose modifyTimestamp is no older than five
	// minutes before the last successful run on this instance began, so
	// changes reach hosts within one interval. The first run after a
	// start, and any run the directory refuses the modifyTimestamp filter
	// for, is a full sync instead. Every instance runs its own. Zero
	// disables the incremental sync.
	SyncInterval time.Duration `mapstructure:"sync_interval,string" default:"15m"`

	// FullSyncInterval is how often the full sync runs: it reads every
	// account and group the filters select, which is the only run that
	// notices an entry has gone (missing_since, then a tombstone after
	// tombstone_after) and the only one that clears collisions whose other
	// entry has gone. POST /api/admin/ldap/sync runs one too. Zero
	// disables the scheduled full sync.
	FullSyncInterval time.Duration `mapstructure:"full_sync_interval,string" default:"24h"`

	// Attributes names the directory attribute each POSIX field is read
	// from. See NSSAttributes.
	Attributes NSSAttributes `mapstructure:"attributes"`

	// GecosTemplate renders the passwd gecos field, with the same
	// text/template engine as the key ID templates. It sees .Name (the
	// mapped name), .DisplayName (the first non-empty attribute of
	// attributes.gecos_attributes, else .Name) and .Attr.<name> for an
	// attribute listed in attributes.gecos_attributes; nothing else is
	// reachable, and an unlisted attribute renders empty. A multi-valued
	// attribute renders with join, as in {{join .Attr.mail ", "}}.
	//
	// The result is cleaned before any host sees it: invalid UTF-8 becomes
	// .Name, control characters and ":" become spaces, whitespace is
	// collapsed and trimmed, the value is capped at 256 bytes, and an
	// empty result becomes .Name. A template that does not parse, or that
	// names a field which does not exist, fails startup.
	GecosTemplate string `mapstructure:"gecos_template" default:"{{.DisplayName}}"`

	// AutoPrivateGroups chooses each account's primary group: "hybrid"
	// (a private group only for an account whose uid equals its gid and
	// whose gid no directory group holds), "true" (every account gets a
	// private group with gid equal to its uid, and an account whose uid is
	// a directory group's gid is not served) or "false" (the directory's
	// gid, no private groups). The same names and meanings as sssd's
	// option. Applied by the server when an account is read, so every host
	// sees the same gid; there is no host-side setting.
	AutoPrivateGroups AutoPrivateGroupsMode `mapstructure:"auto_private_groups" default:"hybrid"`

	// TombstoneAfter is how long an entry may stay missing from the
	// directory before its account or group becomes deleted. Measured from
	// the first successful sync that could not find it by id, DN or
	// filter; a directory outage is never a miss. A deleted group still
	// resolves by name and gid, so ls -l keeps showing a name, but is
	// served with no members, so it grants nothing. Zero tombstones an
	// entry at the first sync that misses it.
	TombstoneAfter time.Duration `mapstructure:"tombstone_after,string" default:"168h"`

	// TombstoneRetention is how long a deleted account or group stays
	// resolvable (an account served as disabled, a group with no members)
	// before it is purged and answers not found. A tombstone holds its
	// number and name the whole time, so neither can be handed to a new
	// entry, and the files it owned on hosts cannot pass to someone else.
	// Zero purges a tombstone at the next sweep and frees its number and
	// name at once.
	TombstoneRetention time.Duration `mapstructure:"tombstone_retention,string" default:"2160h"`

	// DefaultHome is the home directory served for an account whose entry
	// has no home attribute; %u is replaced by the account's name. Must be
	// an absolute path when nss.enabled is on.
	DefaultHome string `mapstructure:"default_home" default:"/home/%u"`

	// DefaultShell is the shell served for an account whose entry has no
	// shell attribute and whose person has chosen none. Empty serves no
	// shell, and the host applies its own default.
	DefaultShell string `mapstructure:"default_shell" default:"" example:"\"/bin/bash\""`

	// DisabledShell replaces the shell of every disabled or deleted
	// account, so a locked account cannot run a login shell even where a
	// host ignores the lock. Must be an absolute path when nss.enabled is
	// on.
	DisabledShell string `mapstructure:"disabled_shell" default:"/usr/sbin/nologin"`

	// AllowedShells lists the shells a person may choose for their own
	// account in the web UI, as bare names ("zsh", not "/bin/zsh"); the
	// host resolves the name to its own path. A choice outside the list is
	// refused.
	AllowedShells []string `mapstructure:"allowed_shells" default:"[sh, bash, zsh, fish]"`

	// MaxGroupMembers caps the members returned for one group. A larger
	// group is truncated and flagged as truncated in the response, and the
	// daemon logs it. The sync stores every member it can resolve to an
	// account; this caps only what one lookup returns. Must be greater
	// than zero.
	MaxGroupMembers int `mapstructure:"max_group_members" default:"2000"`

	// RateLimit is the lookups per second allowed for one fleet key from
	// one source address. The address is the one the server observes,
	// honouring forwarded headers only from http.trusted_proxies, the same
	// address the audit log records as source_ip; behind a load balancer
	// listed there each host keeps its own bucket rather than all of them
	// sharing the balancer's. Each server instance keeps its own buckets,
	// so N instances behind a load balancer allow N times this. Must be
	// greater than zero.
	RateLimit float64 `mapstructure:"rate_limit" default:"20"`

	// RateBurst is how many lookups one key and source address (as for
	// rate_limit) may make back to back before RateLimit starts holding
	// them back: a host starting up resolves many names at once. Must be
	// greater than zero, since a zero burst refuses every lookup.
	RateBurst int `mapstructure:"rate_burst" default:"200"`

	// MissAlertRate is the not-found lookups per second for one key above
	// which the server raises an alert (a log line and an audit event). It
	// never blocks anything: a host enumerating names it does not know is
	// worth hearing about, not refusing. Zero disables the alert.
	MissAlertRate float64 `mapstructure:"miss_alert_rate" default:"5"`

	// UsageRetention is how long the per-key, per-address, per-day usage
	// counts are kept for the usage and rotation reports. Zero disables
	// the prune and keeps every row.
	UsageRetention time.Duration `mapstructure:"usage_retention,string" default:"2160h"`

	// KeyMaxLifetime is the longest a new fleet key may live, and how
	// long it lives when its creator gives no expiry. A later expiry is
	// trimmed to it. A key's expiry is fixed at creation, so lowering
	// this does not shorten keys that already exist. 0 sets no limit: a
	// key created without an expiry then never expires.
	KeyMaxLifetime time.Duration `mapstructure:"key_max_lifetime,string" default:"8760h"`

	// KeyRotationGrace is how long the previous secret stays accepted
	// after a key is rotated, so every host can pick up the new one before
	// the old one stops working. Zero ends the previous secret at the
	// rotation itself.
	KeyRotationGrace time.Duration `mapstructure:"key_rotation_grace,string" default:"720h"`

	// SourceAllowList limits the lookup endpoints to requests from these
	// CIDR networks, checked before the key is: a request from anywhere
	// else is refused with an audit event that carries its address and the
	// key id it presented. The address is the one the server observes,
	// honouring forwarded headers only from http.trusted_proxies, the same
	// address the audit log records as source_ip; a forwarded header from
	// any other peer is ignored. Empty allows every address.
	SourceAllowList []string `mapstructure:"source_allow_list" default:"[]"`

	// PrivilegedNameAlert lists account names whose lookup raises an audit
	// alert, because a directory entry that answers for one of them could
	// shadow a host's own system account. Empty disables the alert.
	PrivilegedNameAlert []string `mapstructure:"privileged_name_alert" default:"[root, admin, daemon, bin, sys]"`

	// KeySourceAlert is the number of distinct source addresses one key
	// may be seen from in a day before the server raises an audit alert:
	// far more than a fleet's size suggests the key has leaked. Zero
	// disables the alert.
	KeySourceAlert int `mapstructure:"key_source_alert" default:"1000"`
}

// NSSAttributes names the directory attribute each POSIX field is read
// from, because directories differ (RFC 2307, RFC 2307bis, Active
// Directory with SFU or IdMU, FreeIPA) and operators sometimes keep POSIX
// data in attributes of their own. Every key is required when nss.enabled
// is on. A value that does not parse (a uid that is not a number, a home
// that is not absolute) skips the entry with a warning naming its DN;
// there are no partial accounts.
type NSSAttributes struct {
	// Name is the account name, stored verbatim. A person can log in by
	// their own name only when it equals their username claim.
	Name string `mapstructure:"name" default:"uid"`

	// UID is the numeric user id; it must parse as an unsigned 32-bit
	// number. Frozen once stored: a later change waits for an admin.
	UID string `mapstructure:"uid" default:"uidNumber"`

	// GID is the numeric primary group id, before auto_private_groups is
	// applied. Frozen once stored, like uid.
	GID string `mapstructure:"gid" default:"gidNumber"`

	// Home is the home directory; optional on the entry, absolute when
	// present. An entry without one is served nss.default_home.
	Home string `mapstructure:"home" default:"homeDirectory"`

	// Shell is the login shell; optional on the entry.
	Shell string `mapstructure:"shell" default:"loginShell"`

	// GroupName is a group's name.
	GroupName string `mapstructure:"group_name" default:"cn"`

	// GroupGID is a group's numeric gid. Frozen once stored, like uid.
	GroupGID string `mapstructure:"group_gid" default:"gidNumber"`

	// GroupMember lists a group's members. Whether its values are names or
	// DNs is decided by the attribute's name, case-insensitively: member
	// and uniqueMember (RFC 2307bis, groupOfNames, groupOfUniqueNames)
	// hold DNs, matched against the accounts' DNs as DNs, so case and the
	// spacing around separators do not matter (a uniqueMember's optional
	// #'...'B suffix is dropped); any other attribute, memberUid (RFC 2307)
	// included, holds account names, matched byte for byte. A member that
	// resolves to no account the sync has stored is left out.
	GroupMember string `mapstructure:"group_member" default:"memberUid"`

	// GecosAttributes are the only attributes nss.gecos_template may read,
	// in order of preference for .DisplayName. Anything else on the entry
	// (userPassword, sshPublicKey, description) cannot reach a field every
	// local user can read with getent unless it is listed here on
	// purpose. At least one is required.
	GecosAttributes []string `mapstructure:"gecos_attributes" default:"[displayName, cn]"`
}

// gecosTemplateData is the smallest data shape nss.gecos_template is
// checked against at startup. The renderer that serves accounts lives with
// the NSS service; this exists only so a template naming a field that does
// not exist fails here rather than at the first lookup.
type gecosTemplateData struct {
	Name        string
	DisplayName string
	Attr        map[string]string
}

// Validate checks the NSS configuration that can be checked without a
// directory, failing startup rather than degrading at runtime. Skipped
// when NSS is disabled, so a half-written block costs nothing until it is
// switched on. ldap is the configuration's LDAP block, which NSS requires.
//
// The filters' LDAP syntax is not checked here: this package does not
// parse LDAP filters, so that happens when the NSS service is built, which
// fails startup just the same.
func (c *NSSConfig) Validate(ldap *LDAPConfig) error {
	if !c.Enabled {
		return nil
	}

	if ldap == nil || !ldap.Enabled {
		return fmt.Errorf("nss.enabled requires ldap.enabled: every POSIX account and group is read from the directory")
	}
	if c.UserFilter == "" {
		return fmt.Errorf("nss.user_filter is required when nss.enabled is true: it is how POSIX accounts are found in the directory")
	}
	if c.GroupFilter == "" {
		return fmt.Errorf("nss.group_filter is required when nss.enabled is true: it is how POSIX groups are found in the directory")
	}
	if err := c.Attributes.validate(); err != nil {
		return err
	}
	if err := validateGecosTemplate(c.GecosTemplate); err != nil {
		return err
	}
	if !c.AutoPrivateGroups.Valid() {
		return fmt.Errorf("nss.auto_private_groups %q is not one of hybrid, true, false", c.AutoPrivateGroups)
	}
	if err := c.validateAccountDefaults(); err != nil {
		return err
	}
	if err := c.validateLimits(); err != nil {
		return err
	}
	return c.validateSourceAllowList()
}

// validate requires every attribute name, and at least one gecos
// attribute with no empty entry among them.
func (a *NSSAttributes) validate() error {
	required := []struct {
		key   string
		value string
	}{
		{"name", a.Name},
		{"uid", a.UID},
		{"gid", a.GID},
		{"home", a.Home},
		{"shell", a.Shell},
		{"group_name", a.GroupName},
		{"group_gid", a.GroupGID},
		{"group_member", a.GroupMember},
	}
	for _, r := range required {
		if strings.TrimSpace(r.value) == "" {
			return fmt.Errorf("nss.attributes.%s must name a directory attribute", r.key)
		}
	}

	if len(a.GecosAttributes) == 0 {
		return fmt.Errorf("nss.attributes.gecos_attributes must list at least one attribute: it is what nss.gecos_template may read")
	}
	for i, attr := range a.GecosAttributes {
		if strings.TrimSpace(attr) == "" {
			return fmt.Errorf("nss.attributes.gecos_attributes[%d] is empty", i)
		}
	}
	return nil
}

// validateGecosTemplate parses src the way the renderer will and executes
// it once against zero data. Parse checks only syntax; field names are
// resolved at execution, so the execute is what catches a typo such as
// {{.DisplyName}}. missingkey=zero makes an .Attr lookup of any name render
// empty instead of failing, and join is a stand-in with the renderer's
// name, accepting whatever it is handed.
func validateGecosTemplate(src string) error {
	tmpl, err := template.New("gecos_template").
		Option("missingkey=zero").
		Funcs(template.FuncMap{"join": func(any, string) string { return "" }}).
		Parse(src)
	if err != nil {
		return fmt.Errorf("nss.gecos_template does not parse: %w", err)
	}
	if err := tmpl.Execute(io.Discard, gecosTemplateData{}); err != nil {
		return fmt.Errorf("nss.gecos_template does not render: %w", err)
	}
	return nil
}

// validateAccountDefaults checks the values served in place of missing or
// locked attributes, and the shells a person may choose.
func (c *NSSConfig) validateAccountDefaults() error {
	if !strings.HasPrefix(c.DefaultHome, "/") {
		return fmt.Errorf("nss.default_home %q must be an absolute path, such as /home/%%u", c.DefaultHome)
	}
	if !strings.HasPrefix(c.DisabledShell, "/") {
		return fmt.Errorf("nss.disabled_shell %q must be an absolute path, such as /usr/sbin/nologin: it is what a locked account is served", c.DisabledShell)
	}
	for i, shell := range c.AllowedShells {
		if strings.TrimSpace(shell) == "" {
			return fmt.Errorf("nss.allowed_shells[%d] is empty", i)
		}
		if strings.Contains(shell, "/") {
			return fmt.Errorf("nss.allowed_shells[%d] %q must be a bare name such as zsh, not a path: the host resolves it", i, shell)
		}
	}
	return nil
}

// validateLimits checks the durations and counts. Negatives are rejected
// rather than clamped, because zero already has a stated meaning for each
// and a negative is a typo whose intent cannot be guessed. The few that
// must be positive say why.
func (c *NSSConfig) validateLimits() error {
	durations := []struct {
		key   string
		value time.Duration
	}{
		{"tombstone_after", c.TombstoneAfter},
		{"tombstone_retention", c.TombstoneRetention},
		{"usage_retention", c.UsageRetention},
		{"key_rotation_grace", c.KeyRotationGrace},
		{"sync_interval", c.SyncInterval},
		{"full_sync_interval", c.FullSyncInterval},
	}
	for _, d := range durations {
		if d.value < 0 {
			return fmt.Errorf("nss.%s %s is negative", d.key, d.value)
		}
	}
	if c.MissAlertRate < 0 {
		return fmt.Errorf("nss.miss_alert_rate %g is negative: use 0 to disable the alert", c.MissAlertRate)
	}
	if c.KeySourceAlert < 0 {
		return fmt.Errorf("nss.key_source_alert %d is negative: use 0 to disable the alert", c.KeySourceAlert)
	}

	if c.KeyMaxLifetime < 0 {
		return fmt.Errorf("nss.key_max_lifetime %s is negative: use 0 for keys that never expire", c.KeyMaxLifetime)
	}
	if c.MaxGroupMembers <= 0 {
		return fmt.Errorf("nss.max_group_members must be greater than zero (the default is 2000)")
	}
	if c.RateLimit <= 0 {
		return fmt.Errorf("nss.rate_limit must be greater than zero (the default is 20): it is the lookups per second one key may make from one address")
	}
	if c.RateBurst <= 0 {
		return fmt.Errorf("nss.rate_burst must be greater than zero (the default is 200): a zero burst refuses every lookup")
	}
	return nil
}

// validateSourceAllowList refuses an entry that is not a CIDR network. A
// list that silently matched nothing would refuse every host, and one read
// the other way would admit everyone, so the only safe place to catch a
// typo is startup, where the error can name the setting.
func (c *NSSConfig) validateSourceAllowList() error {
	for i, cidr := range c.SourceAllowList {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return fmt.Errorf("nss.source_allow_list[%d]: %q is not a CIDR network (e.g. 10.20.0.0/16): %w", i, cidr, err)
		}
	}
	return nil
}
