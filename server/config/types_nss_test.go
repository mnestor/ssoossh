package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// validNSS is an enabled NSS block holding the shipped defaults, with the
// LDAP block it needs, so each case below can change exactly one thing and
// attribute the failure to it.
func validNSS() (NSSConfig, *LDAPConfig) {
	nss := NSSConfig{
		Enabled:          true,
		UserFilter:       "(objectClass=posixAccount)",
		GroupFilter:      "(objectClass=posixGroup)",
		SyncInterval:     15 * time.Minute,
		FullSyncInterval: 24 * time.Hour,
		Attributes: NSSAttributes{
			Name: "uid", UID: "uidNumber", GID: "gidNumber", Home: "homeDirectory", Shell: "loginShell",
			GroupName: "cn", GroupGID: "gidNumber", GroupMember: "memberUid",
			GecosAttributes: []string{"displayName", "cn"},
		},
		GecosTemplate:       "{{.DisplayName}}",
		AutoPrivateGroups:   AutoPrivateGroupsHybrid,
		TombstoneAfter:      168 * time.Hour,
		TombstoneRetention:  2160 * time.Hour,
		DefaultHome:         "/home/%u",
		DisabledShell:       "/usr/sbin/nologin",
		MaxGroupMembers:     2000,
		RateLimit:           20,
		RateBurst:           200,
		MissAlertRate:       5,
		UsageRetention:      2160 * time.Hour,
		KeyMaxLifetime:      8760 * time.Hour,
		KeyRotationGrace:    720 * time.Hour,
		PrivilegedNameAlert: []string{"root", "admin", "daemon", "bin", "sys"},
		KeySourceAlert:      1000,
	}
	ldap := &LDAPConfig{
		Enabled:    true,
		URL:        "ldaps://ldap.example.com",
		BaseDN:     "dc=example,dc=com",
		UserFilter: "(uid={{.Username}})",
	}
	return nss, ldap
}

func TestNSSConfig_ShouldSkipValidationWhenDisabled(t *testing.T) {
	t.Parallel()

	// A half-written block under enabled: false is a deployment in
	// progress, and must not stop a server without LDAP from booting.
	c := NSSConfig{Enabled: false, RateLimit: -1, GecosTemplate: "{{"}
	if err := c.Validate(&LDAPConfig{}); err != nil {
		t.Errorf("Validate on a disabled block: %v", err)
	}
}

func TestNSSConfig_ShouldAcceptTheConfigurationWhenEveryRuleHolds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*NSSConfig)
	}{
		{name: "should accept the shipped defaults when nothing is changed", mutate: func(*NSSConfig) {}},
		{name: "should accept the mode when auto_private_groups is true", mutate: func(c *NSSConfig) { c.AutoPrivateGroups = AutoPrivateGroupsOn }},
		{name: "should accept the mode when auto_private_groups is false", mutate: func(c *NSSConfig) { c.AutoPrivateGroups = AutoPrivateGroupsOff }},
		{name: "should accept the template when it reads an allowed attribute", mutate: func(c *NSSConfig) { c.GecosTemplate = "{{.Name}} ({{.Attr.cn}})" }},
		{name: "should accept the template when it joins a multi-valued attribute", mutate: func(c *NSSConfig) { c.GecosTemplate = `{{join .Attr.mail ", "}}` }},
		{name: "should accept the template when it is plain text", mutate: func(c *NSSConfig) { c.GecosTemplate = "Service account" }},
		{name: "should accept the template when it is empty", mutate: func(c *NSSConfig) { c.GecosTemplate = "" }},
		{name: "should accept the filter when disabled_filter is empty", mutate: func(c *NSSConfig) { c.DisabledFilter = "" }},
		{name: "should accept the shell when default_shell is empty", mutate: func(c *NSSConfig) { c.DefaultShell = "" }},
		{name: "should accept the list when source_allow_list holds IPv4 and IPv6 networks", mutate: func(c *NSSConfig) { c.SourceAllowList = []string{"10.20.0.0/16", "2001:db8::/32"} }},
		{name: "should accept the list when privileged_name_alert is empty", mutate: func(c *NSSConfig) { c.PrivilegedNameAlert = nil }},
		{name: "should accept the interval when sync_interval is zero", mutate: func(c *NSSConfig) { c.SyncInterval = 0 }},
		{name: "should accept the interval when full_sync_interval is zero", mutate: func(c *NSSConfig) { c.FullSyncInterval = 0 }},
		{
			name: "should accept zero when every duration and alert that allows it is zero",
			mutate: func(c *NSSConfig) {
				c.TombstoneAfter, c.TombstoneRetention, c.UsageRetention, c.KeyRotationGrace, c.KeyMaxLifetime = 0, 0, 0, 0, 0
				c.MissAlertRate, c.KeySourceAlert = 0, 0
			},
		},
		{
			name: "should accept the smallest positive values when the limits are at their floor",
			mutate: func(c *NSSConfig) {
				c.MaxGroupMembers, c.RateBurst, c.RateLimit = 1, 1, 0.5
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, ldap := validNSS()
			tt.mutate(&c)
			if err := c.Validate(ldap); err != nil {
				t.Errorf("Validate: %v", err)
			}
		})
	}
}

func TestNSSConfig_ShouldRejectTheConfigurationWhenARuleIsBroken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*NSSConfig, *LDAPConfig)
		noLDAP  bool
		wantErr string
	}{
		{name: "should reject nss when ldap is disabled", mutate: func(_ *NSSConfig, l *LDAPConfig) { l.Enabled = false }, wantErr: "ldap.enabled"},
		{name: "should reject nss when there is no ldap block", mutate: func(*NSSConfig, *LDAPConfig) {}, noLDAP: true, wantErr: "ldap.enabled"},
		{name: "should reject the filter when user_filter is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.UserFilter = "" }, wantErr: "nss.user_filter"},
		{name: "should reject the filter when group_filter is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.GroupFilter = "" }, wantErr: "nss.group_filter"},
		{name: "should reject the mapping when attributes.name is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.Attributes.Name = "" }, wantErr: "nss.attributes.name"},
		{name: "should reject the mapping when attributes.uid is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.Attributes.UID = "" }, wantErr: "nss.attributes.uid"},
		{name: "should reject the mapping when attributes.gid is blank", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.Attributes.GID = "  " }, wantErr: "nss.attributes.gid"},
		{name: "should reject the mapping when attributes.home is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.Attributes.Home = "" }, wantErr: "nss.attributes.home"},
		{name: "should reject the mapping when attributes.shell is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.Attributes.Shell = "" }, wantErr: "nss.attributes.shell"},
		{name: "should reject the mapping when attributes.group_name is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.Attributes.GroupName = "" }, wantErr: "nss.attributes.group_name"},
		{name: "should reject the mapping when attributes.group_gid is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.Attributes.GroupGID = "" }, wantErr: "nss.attributes.group_gid"},
		{name: "should reject the mapping when attributes.group_member is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.Attributes.GroupMember = "" }, wantErr: "nss.attributes.group_member"},
		{name: "should reject the mapping when gecos_attributes is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.Attributes.GecosAttributes = nil }, wantErr: "nss.attributes.gecos_attributes"},
		{name: "should reject the mapping when a gecos attribute is blank", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.Attributes.GecosAttributes = []string{"cn", ""} }, wantErr: "nss.attributes.gecos_attributes[1]"},
		{name: "should reject the template when it does not parse", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.GecosTemplate = "{{.DisplayName" }, wantErr: "nss.gecos_template"},
		{name: "should reject the template when it calls an unknown function", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.GecosTemplate = "{{upper .Name}}" }, wantErr: "nss.gecos_template"},
		{name: "should reject the template when it names a field that does not exist", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.GecosTemplate = "{{.DisplyName}}" }, wantErr: "nss.gecos_template"},
		{name: "should reject the template when it reaches below an attribute value", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.GecosTemplate = "{{.Attr.cn.first}}" }, wantErr: "nss.gecos_template"},
		{name: "should reject the mode when auto_private_groups is unknown", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.AutoPrivateGroups = "sometimes" }, wantErr: "nss.auto_private_groups"},
		{name: "should reject the mode when auto_private_groups is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.AutoPrivateGroups = "" }, wantErr: "nss.auto_private_groups"},
		{name: "should reject the mode when auto_private_groups was decoded from a bare boolean", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.AutoPrivateGroups = "1" }, wantErr: "nss.auto_private_groups"},
		{name: "should reject the home when default_home is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.DefaultHome = "" }, wantErr: "nss.default_home"},
		{name: "should reject the home when default_home is relative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.DefaultHome = "home/%u" }, wantErr: "nss.default_home"},
		{name: "should reject the shell when disabled_shell is empty", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.DisabledShell = "" }, wantErr: "nss.disabled_shell"},
		{name: "should reject the shell when disabled_shell is relative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.DisabledShell = "nologin" }, wantErr: "nss.disabled_shell"},
		{name: "should reject the duration when tombstone_after is negative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.TombstoneAfter = -time.Hour }, wantErr: "nss.tombstone_after"},
		{name: "should reject the duration when tombstone_retention is negative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.TombstoneRetention = -time.Hour }, wantErr: "nss.tombstone_retention"},
		{name: "should reject the duration when usage_retention is negative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.UsageRetention = -time.Hour }, wantErr: "nss.usage_retention"},
		{name: "should reject the duration when key_rotation_grace is negative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.KeyRotationGrace = -time.Hour }, wantErr: "nss.key_rotation_grace"},
		{name: "should reject the duration when sync_interval is negative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.SyncInterval = -time.Minute }, wantErr: "nss.sync_interval"},
		{name: "should reject the duration when full_sync_interval is negative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.FullSyncInterval = -time.Hour }, wantErr: "nss.full_sync_interval"},
		{name: "should reject the duration when key_max_lifetime is negative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.KeyMaxLifetime = -time.Hour }, wantErr: "nss.key_max_lifetime"},
		{name: "should reject the rate when miss_alert_rate is negative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.MissAlertRate = -1 }, wantErr: "nss.miss_alert_rate"},
		{name: "should reject the count when key_source_alert is negative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.KeySourceAlert = -1 }, wantErr: "nss.key_source_alert"},
		{name: "should reject the cap when max_group_members is zero", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.MaxGroupMembers = 0 }, wantErr: "nss.max_group_members"},
		{name: "should reject the cap when max_group_members is negative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.MaxGroupMembers = -5 }, wantErr: "nss.max_group_members"},
		{name: "should reject the rate when rate_limit is zero", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.RateLimit = 0 }, wantErr: "nss.rate_limit"},
		{name: "should reject the rate when rate_limit is negative", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.RateLimit = -20 }, wantErr: "nss.rate_limit"},
		{name: "should reject the burst when rate_burst is zero", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.RateBurst = 0 }, wantErr: "nss.rate_burst"},
		{name: "should reject the list when a source is not a CIDR", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.SourceAllowList = []string{"10.0.0.0/8", "10.20.0.1"} }, wantErr: "nss.source_allow_list[1]"},
		{name: "should reject the list when a source is a hostname", mutate: func(c *NSSConfig, _ *LDAPConfig) { c.SourceAllowList = []string{"hosts.example.com"} }, wantErr: "nss.source_allow_list[0]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, ldap := validNSS()
			tt.mutate(&c, ldap)
			ldapArg := ldap
			if tt.noLDAP {
				ldapArg = nil
			}

			err := c.Validate(ldapArg)
			if err == nil {
				t.Fatal("Validate accepted the configuration")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not name %q", err, tt.wantErr)
			}
		})
	}
}

func TestAutoPrivateGroupsMode_ShouldReportValidityWhenChecked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode AutoPrivateGroupsMode
		want bool
	}{
		{name: "should accept hybrid when it is the mode", mode: "hybrid", want: true},
		{name: "should accept true when it is the mode", mode: "true", want: true},
		{name: "should accept false when it is the mode", mode: "false", want: true},
		{name: "should refuse the empty string when no mode is set", mode: "", want: false},
		{name: "should refuse a different case when the mode is capitalised", mode: "Hybrid", want: false},
		{name: "should refuse 1 when a boolean was decoded weakly", mode: "1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.mode.Valid(); got != tt.want {
				t.Errorf("Valid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAutoPrivateGroupsHook_ShouldSpellABooleanAsTheModeWhenDecoding(t *testing.T) {
	t.Parallel()

	modeType := reflect.TypeFor[AutoPrivateGroupsMode]()
	tests := []struct {
		name string
		from reflect.Type
		to   reflect.Type
		data any
		want any
	}{
		{name: "should turn true into the true mode when the target is the mode", from: reflect.TypeFor[bool](), to: modeType, data: true, want: "true"},
		{name: "should turn false into the false mode when the target is the mode", from: reflect.TypeFor[bool](), to: modeType, data: false, want: "false"},
		{name: "should pass a string through when the value is already a word", from: reflect.TypeFor[string](), to: modeType, data: "hybrid", want: "hybrid"},
		{name: "should leave a boolean alone when the target is another type", from: reflect.TypeFor[bool](), to: reflect.TypeFor[string](), data: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := autoPrivateGroupsHook(tt.from, tt.to, tt.data)
			if err != nil {
				t.Fatalf("hook: %v", err)
			}
			if got != tt.want {
				t.Errorf("hook = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// loadNSSConfig runs NewConfig over contents written to a temp file, the way
// the server loads its configuration, so the nss block's wiring (defaults,
// the decode hook and the Validate call) is what is under test.
func loadNSSConfig(t *testing.T, contents string) (*Config, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ssoosshd.yaml")
	writeFile(t, path, contents)
	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}
	return NewConfig(cc)
}

// nssLDAPBlock is the smallest LDAP block NSS can be switched on beside.
const nssLDAPBlock = `
ssh_key: "test-key-material"
ldap:
  enabled: true
  url: ldaps://ldap.example.com
  base_dn: dc=example,dc=com
  user_filter: (uid={{.Username}})
`

func TestNewConfig_ShouldShipTheNSSDefaultsWhenTheBlockIsAbsent(t *testing.T) {
	t.Parallel()

	c, err := loadNSSConfig(t, `ssh_key: "test-key-material"`)
	if err != nil {
		t.Fatalf("NewConfig: %v", err)
	}
	want, _ := validNSS()
	want.Enabled = false
	if !reflect.DeepEqual(c.NSS, want) {
		t.Errorf("nss defaults =\n %+v\nwant\n %+v", c.NSS, want)
	}
}

func TestNewConfig_ShouldApplyTheNSSRulesWhenTheBlockIsLoaded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		contents string
		wantErr  string
		wantMode AutoPrivateGroupsMode
	}{
		{
			name:     "should refuse to start when nss is on and ldap is off",
			contents: "ssh_key: \"test-key-material\"\nnss:\n  enabled: true\n",
			wantErr:  "ldap.enabled",
		},
		{
			name:     "should read the true mode when auto_private_groups is an unquoted true",
			contents: nssLDAPBlock + "nss:\n  enabled: true\n  auto_private_groups: true\n",
			wantMode: AutoPrivateGroupsOn,
		},
		{
			name:     "should read the false mode when auto_private_groups is an unquoted false",
			contents: nssLDAPBlock + "nss:\n  enabled: true\n  auto_private_groups: false\n",
			wantMode: AutoPrivateGroupsOff,
		},
		{
			name:     "should refuse to start when the gecos template names a missing field",
			contents: nssLDAPBlock + "nss:\n  enabled: true\n  gecos_template: \"{{.Nmae}}\"\n",
			wantErr:  "nss.gecos_template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := loadNSSConfig(t, tt.contents)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("NewConfig error = %v, want one naming %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewConfig: %v", err)
			}
			if c.NSS.AutoPrivateGroups != tt.wantMode {
				t.Errorf("auto_private_groups = %q, want %q", c.NSS.AutoPrivateGroups, tt.wantMode)
			}
		})
	}
}
