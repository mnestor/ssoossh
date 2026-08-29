package config

// Test methodology: table-driven unit tests over LDAPConfig.Validate. Every
// case here is a config mistake that would otherwise surface as silently
// degraded enrichment, because LDAP fails open by design — which is exactly
// what makes catching these at startup worth doing.

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// validLDAP is the minimum configuration that passes, so each case below
// changes one thing.
func validLDAP() LDAPConfig {
	return LDAPConfig{
		Enabled:    true,
		URL:        "ldaps://directory.test",
		BaseDN:     "ou=people,dc=test",
		UserFilter: "(uid={{.Username}})",
		Sync:       LDAPSync{Interval: 15 * time.Minute, DisableAfter: 3},
	}
}

func TestLDAPConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mutate   func(*LDAPConfig)
		wantText string
	}{
		{
			name:   "the minimum valid configuration",
			mutate: func(*LDAPConfig) {},
		},
		{
			name:     "no url",
			mutate:   func(c *LDAPConfig) { c.URL = "" },
			wantText: "ldap.url",
		},
		{
			name:     "no base dn",
			mutate:   func(c *LDAPConfig) { c.BaseDN = "" },
			wantText: "ldap.base_dn",
		},
		{
			name:     "no user filter",
			mutate:   func(c *LDAPConfig) { c.UserFilter = "" },
			wantText: "ldap.user_filter",
		},
		{
			// Zero would disable an account on its first miss, turning a
			// momentary inconsistency into a lockout.
			name:     "a sync with no miss threshold",
			mutate:   func(c *LDAPConfig) { c.Sync.DisableAfter = 0 },
			wantText: "disable_after",
		},
		{
			name:     "a negative miss threshold",
			mutate:   func(c *LDAPConfig) { c.Sync.DisableAfter = -1 },
			wantText: "disable_after",
		},
		{
			// With the sync off there is nothing to threshold, so a zero is
			// simply unused rather than dangerous.
			name: "no threshold is fine with the sync disabled",
			mutate: func(c *LDAPConfig) {
				c.Sync.Interval = 0
				c.Sync.DisableAfter = 0
			},
		},
		{
			name: "a field with no source",
			mutate: func(c *LDAPConfig) {
				c.Fields = map[string]LDAPField{"groups": {}}
			},
			wantText: "configures no source",
		},
		{
			name: "a search with no filter",
			mutate: func(c *LDAPConfig) {
				c.Fields = map[string]LDAPField{
					"other_accounts": {Searches: []LDAPFieldSearch{{Value: "uid"}}},
				}
			},
			wantText: "has no filter",
		},
		{
			name: "a search with no value attribute",
			mutate: func(c *LDAPConfig) {
				c.Fields = map[string]LDAPField{
					"other_accounts": {Searches: []LDAPFieldSearch{{Filter: "(a=b)"}}},
				}
			},
			wantText: "no value attribute",
		},
		{
			name: "a valid field with both sources",
			mutate: func(c *LDAPConfig) {
				c.Fields = map[string]LDAPField{
					"other_accounts": {
						Attribute: "sAMAccountName",
						Searches:  []LDAPFieldSearch{{Filter: "(a={{.Username}})", Value: "uid"}},
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := validLDAP()
			tt.mutate(&c)

			err := c.Validate()
			if tt.wantText == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() error = nil, want one mentioning %q", tt.wantText)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("Validate() error = %q, want it to mention %q", err, tt.wantText)
			}
		})
	}
}

// The key identity fields come from OIDC and nowhere else. Configuring one
// could only read as an attempt to override identity, so it is refused
// rather than silently ignored.
func TestLDAPConfig_ShouldRejectKeyIdentityFields(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"username", "email", "subject"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := validLDAP()
			c.Fields = map[string]LDAPField{name: {Attribute: "someAttr"}}

			err := c.Validate()
			if err == nil {
				t.Fatalf("Validate() error = nil, want ldap.fields.%s refused", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("Validate() error = %q, want it to name the field", err)
			}
		})
	}
}

// A disabled directory costs nothing: a half-written block in a config file
// should not stop the server booting until it is switched on.
func TestLDAPConfig_ShouldSkipValidationWhenDisabled(t *testing.T) {
	t.Parallel()

	c := LDAPConfig{Enabled: false, Fields: map[string]LDAPField{"username": {Attribute: "x"}}}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate() on a disabled directory error = %v, want nil", err)
	}
}

func TestIsExtraField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
		want  bool
	}{
		{name: "a reserved destination", field: "other_accounts", want: false},
		{name: "another reserved destination", field: "service_accounts", want: false},
		{name: "groups is reserved", field: "groups", want: false},
		{name: "anything else is an extra template field", field: "department", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := IsExtraField(tt.field); got != tt.want {
				t.Errorf("IsExtraField(%q) = %v, want %v", tt.field, got, tt.want)
			}
		})
	}
}

// The bare-string shorthand goes through a viper decode hook rather than
// the struct, so it needs its own coverage: a regression here would make
// the common configuration silently decode to an empty field.
func TestLDAPFieldShorthandHook(t *testing.T) {
	t.Parallel()

	fieldType := reflect.TypeOf(LDAPField{})
	stringType := reflect.TypeOf("")

	t.Run("should expand a bare string into an attribute", func(t *testing.T) {
		t.Parallel()

		got, err := ldapFieldShorthandHook(stringType, fieldType, "memberOf")
		if err != nil {
			t.Fatalf("hook error = %v", err)
		}
		m, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("hook returned %T, want a map", got)
		}
		if m["attribute"] != "memberOf" {
			t.Errorf("attribute = %v, want memberOf", m["attribute"])
		}
	})

	t.Run("should leave the full map form alone", func(t *testing.T) {
		t.Parallel()

		in := map[string]any{"attribute": "sAMAccountName"}
		got, err := ldapFieldShorthandHook(reflect.TypeOf(in), fieldType, in)
		if err != nil {
			t.Fatalf("hook error = %v", err)
		}
		if _, ok := got.(map[string]any); !ok {
			t.Errorf("hook rewrote the map form: %T", got)
		}
	})

	t.Run("should not touch other destinations", func(t *testing.T) {
		t.Parallel()

		got, err := ldapFieldShorthandHook(stringType, stringType, "plain")
		if err != nil {
			t.Fatalf("hook error = %v", err)
		}
		if got != "plain" {
			t.Errorf("hook altered a plain string: %v", got)
		}
	})
}
