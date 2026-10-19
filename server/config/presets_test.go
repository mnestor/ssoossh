package config

// Test methodology: every case loads a real config file through NewConfig,
// so the layering under test is the one the server runs: defaults.yaml,
// then the preset, then the file. The preset values themselves are pinned
// by an explicit table, so a changed preset file has to change a test too.

import (
	"strings"
	"testing"

	"github.com/go-viper/mapstructure/v2"

	"github.com/mnestor/ssoossh/test/configgolden"
)

// presetKeyPrefixes are the only keys a preset may set: the ones whose
// right value depends on the directory. Anything else belongs in
// defaults.yaml or the operator's file.
func presetKeyPrefixes() []string {
	return []string{
		"ldap.id_attribute",
		"nss.user_filter",
		"nss.group_filter",
		"nss.disabled_filter",
		"nss.attributes.",
	}
}

// should load every preset, set each value it names, and set nothing
// outside the schema-dependent keys.
func TestLDAPSchemaPresets_ShouldLoad(t *testing.T) {
	t.Parallel()

	for _, schema := range LDAPSchemas() {
		t.Run(schema, func(t *testing.T) {
			t.Parallel()

			settings, err := LDAPSchemaPresetSettings(schema)
			if err != nil {
				t.Fatalf("LDAPSchemaPresetSettings(%q) error = %v", schema, err)
			}
			if len(settings) == 0 {
				t.Fatalf("preset %q sets nothing", schema)
			}

			c, err := loadNSSConfig(t, "ssh_key: \"test-key-material\"\nldap:\n  schema: "+schema+"\n")
			if err != nil {
				t.Fatalf("NewConfig() with ldap.schema %q error = %v", schema, err)
			}
			effective := map[string]string{}
			for _, s := range c.Effective() {
				effective[s.Key] = s.Value
			}
			for _, s := range settings {
				if !hasPresetPrefix(s.Key) {
					t.Errorf("preset %q sets %s, which is not a schema-dependent key", schema, s.Key)
				}
				want := strings.Trim(s.Value, "[]")
				if got, ok := effective[s.Key]; !ok || got != want {
					t.Errorf("preset %q: %s = %q, want %q", schema, s.Key, got, want)
				}
			}
		})
	}
}

// should decode every preset into Config with no key left over, so a
// misspelt key in a preset file fails here rather than doing nothing.
func TestLDAPSchemaPresets_ShouldNameOnlyRealKeys(t *testing.T) {
	t.Parallel()

	for _, schema := range LDAPSchemas() {
		t.Run(schema, func(t *testing.T) {
			t.Parallel()

			data, err := ldapSchemaPreset(schema)
			if err != nil {
				t.Fatalf("ldapSchemaPreset(%q) error = %v", schema, err)
			}
			v, err := readYAML(data)
			if err != nil {
				t.Fatalf("parse preset %q: %v", schema, err)
			}
			var md mapstructure.Metadata
			var c Config
			dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{Result: &c, Metadata: &md, WeaklyTypedInput: true})
			if err != nil {
				t.Fatalf("NewDecoder() error = %v", err)
			}
			if err := dec.Decode(v.AllSettings()); err != nil {
				t.Fatalf("decode preset %q: %v", schema, err)
			}
			if len(md.Unused) > 0 {
				t.Errorf("preset %q names keys Config does not have: %v", schema, md.Unused)
			}
		})
	}
}

// should set exactly the values the owner decided on for each directory.
// The table is the specification; the preset files must agree with it.
func TestLDAPSchemaPresets_ShouldSetTheDecidedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		schema   string
		id       string
		user     string
		group    string
		disabled string
		name     string
		home     string
		shell    string
		gname    string
		member   string
	}{
		{"rfc2307", "entryUUID", "(objectClass=posixAccount)", "(objectClass=posixGroup)", "", "uid", "homeDirectory", "loginShell", "cn", "memberUid"},
		{"rfc2307bis", "entryUUID", "(objectClass=posixAccount)", "(objectClass=posixGroup)", "", "uid", "homeDirectory", "loginShell", "cn", "member"},
		{"active-directory", "objectGUID", "(&(objectCategory=person)(objectClass=user)(uidNumber=*))", "(&(objectClass=group)(gidNumber=*))", "(userAccountControl:1.2.840.113556.1.4.803:=2)", "sAMAccountName", "unixHomeDirectory", "loginShell", "sAMAccountName", "member"},
		{"lldap", "entryUUID", "(&(objectClass=posixAccount)(uidNumber=*))", "(&(objectClass=groupOfUniqueNames)(gidNumber=*))", "", "uid", "homeDirectory", "unixShell", "cn", "uniqueMember"},
		{"freeipa", "ipaUniqueID", "(objectClass=posixAccount)", "(objectClass=posixGroup)", "(nsAccountLock=TRUE)", "uid", "homeDirectory", "loginShell", "cn", "member"},
	}
	for _, tt := range tests {
		t.Run(tt.schema, func(t *testing.T) {
			t.Parallel()

			c, err := loadNSSConfig(t, "ssh_key: \"test-key-material\"\nldap:\n  schema: "+tt.schema+"\n")
			if err != nil {
				t.Fatalf("NewConfig() error = %v", err)
			}
			got := []string{
				c.LDAP.IDAttribute, c.NSS.UserFilter, c.NSS.GroupFilter, c.NSS.DisabledFilter,
				c.NSS.Attributes.Name, c.NSS.Attributes.Home, c.NSS.Attributes.Shell,
				c.NSS.Attributes.GroupName, c.NSS.Attributes.GroupMember,
			}
			want := []string{tt.id, tt.user, tt.group, tt.disabled, tt.name, tt.home, tt.shell, tt.gname, tt.member}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("value %d = %q, want %q", i, got[i], want[i])
				}
			}
			if c.NSS.Attributes.UID != "uidNumber" || c.NSS.Attributes.GID != "gidNumber" || c.NSS.Attributes.GroupGID != "gidNumber" {
				t.Errorf("numeric attributes = %q/%q/%q, want uidNumber/gidNumber/gidNumber",
					c.NSS.Attributes.UID, c.NSS.Attributes.GID, c.NSS.Attributes.GroupGID)
			}
		})
	}
}

// should let every kind of key the user's file sets beat the preset, and
// leave the preset's other keys in place beside it.
func TestLDAPSchemaPresets_ShouldYieldToTheConfigFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		file  string
		check func(*Config) (got, want string)
	}{
		{
			name: "should keep the file's string when it sets a top-level preset key",
			file: "ldap:\n  schema: active-directory\n  id_attribute: entryUUID\n",
			check: func(c *Config) (string, string) {
				return c.LDAP.IDAttribute, "entryUUID"
			},
		},
		{
			name: "should keep the file's nested key when it sets one attribute",
			file: "ldap:\n  schema: active-directory\nnss:\n  attributes:\n    shell: zshPath\n",
			check: func(c *Config) (string, string) {
				return c.NSS.Attributes.Shell, "zshPath"
			},
		},
		{
			name: "should keep the preset's sibling attributes when the file sets one",
			file: "ldap:\n  schema: active-directory\nnss:\n  attributes:\n    shell: zshPath\n",
			check: func(c *Config) (string, string) {
				return c.NSS.Attributes.Home, "unixHomeDirectory"
			},
		},
		{
			name: "should keep an explicit empty string from the file",
			file: "ldap:\n  schema: active-directory\nnss:\n  disabled_filter: \"\"\n",
			check: func(c *Config) (string, string) {
				return c.NSS.DisabledFilter, ""
			},
		},
		{
			name: "should keep the file's list when it sets one",
			file: "ldap:\n  schema: lldap\nnss:\n  attributes:\n    gecos_attributes: [cn]\n",
			check: func(c *Config) (string, string) {
				return strings.Join(c.NSS.Attributes.GecosAttributes, ","), "cn"
			},
		},
		{
			name: "should keep a default the preset leaves alone",
			file: "ldap:\n  schema: lldap\n",
			check: func(c *Config) (string, string) {
				return strings.Join(c.NSS.Attributes.GecosAttributes, ","), "displayName,cn"
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := loadNSSConfig(t, "ssh_key: \"test-key-material\"\n"+tt.file)
			if err != nil {
				t.Fatalf("NewConfig() error = %v", err)
			}
			if got, want := tt.check(c); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// should fail startup naming every allowed value when ldap.schema is not
// one of them.
func TestNewConfig_ShouldRejectAnUnknownLDAPSchema(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		schema string
	}{
		{"should reject a name no preset has", "openldap"},
		{"should reject a near miss in case", "Active-Directory"},
		{"should reject a padded name", "' lldap'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadNSSConfig(t, "ssh_key: \"test-key-material\"\nldap:\n  schema: "+tt.schema+"\n")
			if err == nil {
				t.Fatalf("NewConfig() with ldap.schema %s returned no error", tt.schema)
			}
			for _, allowed := range LDAPSchemas() {
				if !strings.Contains(err.Error(), allowed) {
					t.Errorf("error %q does not name the allowed value %q", err, allowed)
				}
			}
		})
	}
}

// should resolve exactly today's configuration when ldap.schema is unset:
// the golden was recorded before presets existed, so the one key presets
// added (ldap.schema itself, empty) is the only line left out of it.
func TestNewConfig_ShouldChangeNothingWhenTheSchemaIsUnset(t *testing.T) {
	t.Parallel()

	c, err := loadNSSConfig(t, "ssh_key: \"test-key-material\"\n")
	if err != nil {
		t.Fatalf("NewConfig() error = %v", err)
	}
	var b strings.Builder
	for _, s := range c.Effective() {
		if s.Key == "ldap.schema" || s.Key == "ssh_key" {
			continue
		}
		b.WriteString(s.Key + " = " + s.Value + "\n")
	}
	configgolden.Assert(t, "./server/config/", "effective-no-schema.golden", b.String())
}

// should leave the built-in default in place when nothing sets a key, which
// is what the docs table's default column reads.
func TestDefaultSettingValue_ShouldReadTheEmbeddedDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want string
	}{
		{"nss.attributes.group_member", "memberUid"},
		{"nss.attributes.gecos_attributes", "[displayName, cn]"},
		{"ldap.id_attribute", ""},
		{"nss.disabled_filter", ""},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()

			if got := DefaultSettingValue(tt.key); got != tt.want {
				t.Errorf("DefaultSettingValue(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

// hasPresetPrefix reports whether key is one a preset may set.
func hasPresetPrefix(key string) bool {
	for _, p := range presetKeyPrefixes() {
		if key == p || (strings.HasSuffix(p, ".") && strings.HasPrefix(key, p)) {
			return true
		}
	}
	return false
}
