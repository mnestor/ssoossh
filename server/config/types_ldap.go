package config

import (
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"time"
)

// Reserved field names under ldap.fields. The first three map to the
// identity fields LDAP may populate; the last three name the key identity
// fields it may never touch.
const (
	LDAPFieldOtherAccounts   = "other_accounts"
	LDAPFieldServiceAccounts = "service_accounts"
	LDAPFieldGroups          = "groups"
)

// ldapReservedFields are the field names with defined meanings, so they
// cannot double as extra template fields.
var ldapReservedFields = []string{LDAPFieldOtherAccounts, LDAPFieldServiceAccounts, LDAPFieldGroups}

// ldapForbiddenFields are the key identity fields LDAP may never write.
// Subject keys the user row, the username is what LDAP lookups are keyed
// *by*, and the OIDC email claim is the source of truth for users.email.
// Configuring any of them could only read as an attempt to override
// identity, so it is a startup error rather than a silently ignored key.
var ldapForbiddenFields = []string{"username", "email", "subject"}

// LDAPField is one destination and every source that populates it.
// Everything feeding a field is declared under it, so reading one block
// shows the whole picture for that value.
//
// A bare string in YAML is shorthand for `attribute: <string>`; see
// UnmarshalText-style handling in the decode hook.
type LDAPField struct {
	// Attribute reads the value from the person's own entry — a forward
	// list, the simplest of the linking topologies.
	Attribute string `mapstructure:"attribute"`

	// Searches resolve linked accounts that are their own directory
	// entries. They run after the primary lookup and are keyed by filter
	// rather than by DN, so they must re-run at sync time: a reverse link
	// can change without the person's own entry changing, which is much of
	// what the sync exists to catch.
	Searches []LDAPFieldSearch `mapstructure:"searches"`
}

// IsZero reports whether the field configures no source at all, which is
// what leaves the OIDC value untouched.
func (f *LDAPField) IsZero() bool {
	return f == nil || (f.Attribute == "" && len(f.Searches) == 0)
}

// LDAPFieldSearch is one secondary search feeding the field it is declared
// under.
type LDAPFieldSearch struct {
	// Name labels the search in logs and errors.
	Name string `mapstructure:"name"`

	// BaseDN is the search base. Empty inherits ldap.base_dn.
	BaseDN string `mapstructure:"base_dn"`

	// Filter is a Go template over the OIDC identity plus {{.DN}} and
	// {{.Attr.<name>}} from the primary entry. Values are RFC 4515 escaped
	// during rendering and the operator cannot opt out: a
	// preferred_username containing * or ) is otherwise filter injection.
	Filter string `mapstructure:"filter"`

	// Value names the attribute on each matched entry that contributes to
	// the field, e.g. "uid".
	Value string `mapstructure:"value"`
}

// LDAPSync configures the background directory sync.
type LDAPSync struct {
	// Interval is how often the sync runs. Zero disables the job entirely.
	Interval time.Duration `mapstructure:"interval,string" default:"15m"`

	// DisableAfter is how many consecutive *successful* searches that find
	// no entry it takes before the user is auto-disabled. A directory
	// outage is never a miss: only a search that succeeds and finds nothing
	// counts.
	DisableAfter int `mapstructure:"disable_after" default:"3"`

	// Reenable lets the sync clear its own disables when a directory entry
	// reappears. It only ever clears disables whose source is exactly
	// ldap_sync; a user disabled by an admin or SOC operator is never
	// touched by the sync, in either direction.
	Reenable bool `mapstructure:"reenable" default:"true"`

	// ExtraGroups are group names to persist in addition to those the rest
	// of the configuration references — a future notification target, say.
	// See the allowlist rule on user_groups.
	ExtraGroups []string `mapstructure:"extra_groups"`
}

// LDAPLimits caps what one directory can push into the database. High
// rather than tight: they exist to bound pathology, and truncation is
// logged to the LDAP destination so a real deployment hitting one can see
// it and raise it.
type LDAPLimits struct {
	// MaxValuesPerAttribute caps a single multi-valued attribute.
	MaxValuesPerAttribute int `mapstructure:"max_values_per_attribute" default:"1000"`

	// MaxEntriesPerSearch caps the matched entries one field search
	// contributes.
	MaxEntriesPerSearch int `mapstructure:"max_entries_per_search" default:"1000"`

	// MaxAttributesBytes caps the serialized user_ldap.attributes JSON.
	MaxAttributesBytes int `mapstructure:"max_attributes_bytes" default:"65536"`
}

// Validate checks the LDAP configuration that can be checked without a
// directory, failing startup rather than degrading at runtime. Skipped
// entirely when LDAP is disabled, so a half-written block in a config file
// costs nothing until it is switched on.
func (c *LDAPConfig) Validate() error {
	if !c.Enabled {
		return nil
	}

	if c.URL == "" {
		return fmt.Errorf("ldap.url is required when ldap.enabled is true")
	}
	if c.BaseDN == "" {
		return fmt.Errorf("ldap.base_dn is required when ldap.enabled is true")
	}
	if c.UserFilter == "" {
		return fmt.Errorf("ldap.user_filter is required when ldap.enabled is true: it is how a person is found in the directory")
	}

	for name, field := range c.Fields {
		if err := validateLDAPField(name, field); err != nil {
			return err
		}
	}

	if c.Sync.DisableAfter < 0 {
		return fmt.Errorf("ldap.sync.disable_after must not be negative")
	}
	// Zero would disable an account on the first miss, which turns a
	// momentary directory inconsistency into a lockout. Enabling
	// auto-disable at all is opt-in through a positive value.
	if c.Sync.Interval > 0 && c.Sync.DisableAfter == 0 {
		return fmt.Errorf("ldap.sync.disable_after must be greater than zero: a single missed search would otherwise disable the account")
	}

	// tls_insecure_skip_verify is not an error — it is a deliberate homelab
	// escape hatch — but it makes the connection trivially interceptable,
	// so it is announced rather than left to be discovered.
	if c.TLSInsecureSkipVerify {
		slog.Warn("ldap.tls_insecure_skip_verify is enabled: the directory connection is not authenticated and can be intercepted; do not run this in production")
	}
	return nil
}

// validateLDAPField checks one field mapping: a usable destination name and
// a source that can actually be executed.
func validateLDAPField(name string, field LDAPField) error {
	if slices.Contains(ldapForbiddenFields, name) {
		return fmt.Errorf("ldap.fields.%s is not configurable: the subject keys the user row, the username is what LDAP lookups are keyed by, and the OIDC email claim is the source of truth for users.email", name)
	}
	if field.IsZero() {
		return fmt.Errorf("ldap.fields.%s configures no source: give it an attribute, one or more searches, or remove it", name)
	}
	for i, search := range field.Searches {
		if search.Filter == "" {
			return fmt.Errorf("ldap.fields.%s.searches[%d] has no filter", name, i)
		}
		if search.Value == "" {
			return fmt.Errorf("ldap.fields.%s.searches[%d] has no value attribute naming what each matched entry contributes", name, i)
		}
	}
	return nil
}

// IsExtraField reports whether name is an operator-chosen extra template
// field rather than one of the reserved destinations.
func IsExtraField(name string) bool {
	return !slices.Contains(ldapReservedFields, name)
}

// ldapFieldShorthandHook lets an LDAP field be written as a bare attribute
// name instead of a one-key map:
//
//	fields:
//	  groups: memberOf            # shorthand
//	  other_accounts:             # the full form
//	    attribute: sAMAccountName
//
// The shorthand is the overwhelmingly common case (read this attribute off
// the person's entry), and requiring the map form for it would make the
// simple configuration the noisy one.
func ldapFieldShorthandHook(from, to reflect.Type, data any) (any, error) {
	if to != reflect.TypeOf(LDAPField{}) || from.Kind() != reflect.String {
		return data, nil
	}
	s, ok := data.(string)
	if !ok {
		// not covered: from.Kind() == String is checked above, so the type
		// assertion cannot fail.
		return data, nil
	}
	return map[string]any{"attribute": s}, nil
}
