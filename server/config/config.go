// Package config defines application configuration and sets up Viper.
//
// This package centralizes configuration loading and access for the server.
package config

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// defaultconfig holds the embedded contents of defaults.yaml, loaded into
// Viper before any config file is merged in.
//
//go:embed _defaults.yaml
var defaultconfig string

// NewConfig loads the ssoosshd configuration using Viper.
//
// Configuration is built by layering: defaults.yaml is loaded first, then
// a config file is located and merged on top.
//
// If the command's --config/-c flag is set, that file is used. Otherwise,
// the following locations are searched in order, and the first one found is used:
//  1. ./ssoosshd.yaml (current directory)
//  2. /etc/ssoosshd.yaml (system root)
//  3. /etc/ssoossh/ssoosshd.yaml (ssoossh-specific directory)
func NewConfig(cmd *cobra.Command) (*Config, error) {
	v := viper.New()

	// set defaults
	//
	// The error branch below is unreachable and excluded from coverage
	// (exclude-from-coverage.txt): defaultconfig is defaults.yaml, embedded
	// at compile time and checked into the repo as valid YAML.
	v.SetConfigType("yaml")
	if err := v.ReadConfig(bytes.NewBufferString(defaultconfig)); err != nil {
		return nil, fmt.Errorf("failed to load embedded default config: %w", err)
	}

	configFile, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, fmt.Errorf("failed to read config flag: %w", err)
	}

	if configFile != "" {
		v.SetConfigFile(configFile)
	} else {
		v.SetConfigName("ssoosshd")
		v.SetConfigType("yaml")

		v.AddConfigPath(".")
		home, err := os.UserHomeDir()
		if err == nil {
			v.AddConfigPath(filepath.Join(home, ".config"))
			v.AddConfigPath(filepath.Join(home, ".config", "ssoossh"))
		}
		v.AddConfigPath("/etc")
		v.AddConfigPath("/etc/ssoossh")
	}

	if err := v.MergeInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Fail at startup rather than at first login: a bad public_url does not
	// stop anything booting, it just produces a redirect URI the identity
	// provider refuses.
	if err := c.HTTP.Validate(); err != nil {
		return nil, err
	}

	// Same reasoning: a zero request_ttl boots fine and then misbehaves in
	// the sweep and the resolved-outcome cache, both far from the config.
	if err := c.CertOptions.Validate(); err != nil {
		return nil, err
	}

	// Validate message-broker configuration before first use.
	if err := c.PubSub.Validate(); err != nil {
		return nil, err
	}

	// Require an explicit cookie_key when multi-instance is enabled, so
	// sessions don't break between instances. The default per-process
	// random key would leave users logging out unexpectedly.
	if c.MultiInstance && c.HTTP.CookieKey == "" {
		return nil, fmt.Errorf("multi_instance is enabled but http.cookie_key is not set: set an explicit key or disable multi_instance")
	}

	return &c, nil
}
