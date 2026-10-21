package config

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Origin names the configuration layer an effective value came from.
type Origin string

// Origins, lowest layer first. The server reads no configuration keys from
// the environment, so there is no environment layer.
const (
	// OriginDefault is defaults.yaml, or a field's zero value when
	// defaults.yaml leaves it out.
	OriginDefault Origin = "default"
	// OriginPreset is the preset ldap.schema names.
	OriginPreset Origin = "preset"
	// OriginFile is the config file.
	OriginFile Origin = "file"
)

// SourcedSetting is one leaf of Effective with the layer that set it.
type SourcedSetting struct {
	Setting
	// Origin is the highest layer that sets the key.
	Origin Origin
}

// EffectiveSources is Effective, secrets redacted the same way, with the
// origin of every value: the highest of the layers Load merged (the config
// file, then the ldap.schema preset, then defaults.yaml) that sets the key.
//
// A list is attributed as a whole, so cert_options.user.lifetime_policy
// .tiers[0].when.group comes from whichever layer set tiers. It reads the
// config file again, the way Load's preset step already does.
func (c *Config) EffectiveSources() ([]SourcedSetting, error) {
	layers, err := c.originLayers()
	if err != nil {
		return nil, err
	}

	settings := c.Effective()
	out := make([]SourcedSetting, 0, len(settings))
	for _, s := range settings {
		key := s.Key
		if i := strings.IndexByte(key, '['); i >= 0 {
			key = key[:i]
		}
		origin := OriginDefault
		for _, l := range layers {
			if l.v.IsSet(key) {
				origin = l.origin
				break
			}
		}
		out = append(out, SourcedSetting{Setting: s, Origin: origin})
	}
	return out, nil
}

// originLayer is one layer Load merged, read on its own.
type originLayer struct {
	origin Origin
	v      *viper.Viper
}

// originLayers reads the config file and the preset on their own, highest
// first. defaults.yaml needs no layer: it is what is left.
func (c *Config) originLayers() ([]originLayer, error) {
	var layers []originLayer
	if c.file != "" {
		v := viper.New()
		v.SetConfigFile(c.file)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("failed to read config: %w", err)
		}
		layers = append(layers, originLayer{origin: OriginFile, v: v})
	}
	if c.schema != "" {
		preset, err := ldapSchemaPreset(c.schema)
		if err != nil {
			// not covered: Load refuses an unknown schema before a Config
			// with one can exist.
			return nil, err
		}
		v := viper.New()
		v.SetConfigType("yaml")
		if err := v.ReadConfig(bytes.NewReader(preset)); err != nil {
			// not covered: unreachable, for the reason
			// withLDAPSchemaPreset gives.
			return nil, fmt.Errorf("failed to load the %s schema preset: %w", c.schema, err)
		}
		layers = append(layers, originLayer{origin: OriginPreset, v: v})
	}
	return layers, nil
}
