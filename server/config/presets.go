package config

import (
	"bytes"
	"embed"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/viper"
)

// presetFiles holds one YAML file per ldap.schema value, each a partial
// configuration naming only the keys whose right value depends on the
// directory. Every file cites its sources at the top.
//
//go:embed presets/*.yaml
var presetFiles embed.FS

// LDAPSchemas lists the values ldap.schema accepts, in the order the
// documentation presents them.
func LDAPSchemas() []string {
	return []string{"rfc2307", "rfc2307bis", "active-directory", "lldap", "freeipa"}
}

// ldapSchemaPreset returns the embedded YAML for one preset, or an error
// naming every allowed value when name is not one of them.
func ldapSchemaPreset(name string) ([]byte, error) {
	if !slices.Contains(LDAPSchemas(), name) {
		return nil, fmt.Errorf("ldap.schema %q is not a known schema: use one of %s, or leave it unset", name, strings.Join(LDAPSchemas(), ", "))
	}
	data, err := presetFiles.ReadFile("presets/" + name + ".yaml")
	if err != nil {
		// not covered: unreachable. Every name LDAPSchemas returns has an
		// embedded file, which TestLDAPSchemaPresets_ShouldLoad asserts.
		return nil, fmt.Errorf("read the %s preset: %w", name, err)
	}
	return data, nil
}

// PresetSetting is one key a preset sets, as a dotted path and the value
// rendered as text.
type PresetSetting struct {
	Key   string
	Value string
}

// LDAPSchemaPresetSettings returns every key the named preset sets, sorted
// by key. The docs table is generated from this, so it shows exactly what
// the server merges.
func LDAPSchemaPresetSettings(name string) ([]PresetSetting, error) {
	data, err := ldapSchemaPreset(name)
	if err != nil {
		return nil, err
	}
	v, err := readYAML(data)
	if err != nil {
		return nil, fmt.Errorf("parse the %s preset: %w", name, err)
	}
	return settingsOf(v), nil
}

// DefaultSettingValue returns the built-in default for a dotted key, as
// defaults.yaml leaves it with no preset applied. A key defaults.yaml does
// not set renders empty, which is the zero value the server then holds.
func DefaultSettingValue(key string) string {
	v, err := readYAML([]byte(defaultconfig))
	if err != nil {
		// not covered: unreachable. defaults.yaml is embedded and parsed
		// at every start; see NewConfig.
		return ""
	}
	return renderSettingValue(v.Get(key))
}

// readYAML parses one YAML document into a viper instance of its own, the
// same decoder NewConfig layers with.
func readYAML(data []byte) (*viper.Viper, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewReader(data)); err != nil {
		return nil, err
	}
	return v, nil
}

// settingsOf flattens v into sorted dotted keys and rendered values.
func settingsOf(v *viper.Viper) []PresetSetting {
	keys := v.AllKeys()
	sort.Strings(keys)
	out := make([]PresetSetting, 0, len(keys))
	for _, k := range keys {
		out = append(out, PresetSetting{Key: k, Value: renderSettingValue(v.Get(k))})
	}
	return out
}

// renderSettingValue renders a scalar as itself and a list in YAML flow
// form, [a, b], which is how an operator would write it back.
func renderSettingValue(value any) string {
	switch t := value.(type) {
	case nil:
		return ""
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			parts = append(parts, fmt.Sprint(e))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	default:
		return fmt.Sprint(t)
	}
}
