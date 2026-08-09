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
	"slices"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

//go:embed defaults.yaml
var defaultconfig string

type ctxKey string

const Context ctxKey = "CONFIG"

// NewConfig loads the ssoosshd configuration using Viper.
//
// If the command's --config/-c flag is set, that file is used. Otherwise, the
// following locations are merged in order:
// last file merged overrides
//   - /etc/ssoossh/ssoossh.yaml
//   - ~/.config/ssoossh.yaml
//   - ./ssoossh.yaml
//   - config flag setting
func NewConfig(cmd *cobra.Command) (*Config, error) {
	v := viper.New()

	// set defaults
	v.SetConfigType("yaml")
	v.ReadConfig(bytes.NewBufferString(defaultconfig))

	configFile, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, fmt.Errorf("failed to read config flag: %w", err)
	}

	configFiles := []string{
		"/etc/ssoossh/ssoossh.yaml",
		"./ssoossh.yaml",
	}
	home, err := os.UserHomeDir()
	if err == nil {
		configFiles = slices.Insert(configFiles, 1, filepath.Join(home, ".config"))
	}

	if configFile != "" {
		configFiles = append(configFiles, configFile)
		v.SetConfigFile(configFile)
	}

	for _, file := range configFiles {
		v.SetConfigFile(file)
		_ = v.MergeInConfig()
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	return &c, nil
}
