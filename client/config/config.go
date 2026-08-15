// Package config defines application configuration and sets up Viper.
//
// This package centralizes configuration loading and access for the client.
package config

import (
	"bytes"
	_ "embed"
	"fmt"
	"log/slog"
	"path/filepath"

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
//
// Then, if the system file names an `enforce` file, that one is merged last
// of all and wins over everything above it. Nothing else may set `enforce`.
func NewConfig(cmd *cobra.Command) (*Config, error) {
	return newConfig(cmd, defaultSearchPaths())
}

// newConfig is NewConfig with the search locations as a parameter, so the
// `enforce` mechanism can be tested without writing to a system directory
// and so tests do not pick up the developer's own configuration. Production
// callers go through NewConfig.
func newConfig(cmd *cobra.Command, paths searchPaths) (*Config, error) {
	v := viper.New()

	// set defaults
	v.SetConfigType("yaml")
	_ = v.ReadConfig(bytes.NewBufferString(defaultconfig)) //nolint:errcheck // using embeded file, will never fail

	configFile, err := cmd.Flags().GetString("config")
	if err != nil {
		return nil, fmt.Errorf("failed to read config flag: %w", err)
	}

	// The system file first, so it is the one `enforce` is read from, then
	// each more specific location. A missing file at any of them is normal.
	configFiles := []string{filepath.Join(paths.systemDir, configFileName)}
	if paths.userFile != "" {
		configFiles = append(configFiles, paths.userFile)
	}
	if paths.localFile != "" {
		configFiles = append(configFiles, paths.localFile)
	}

	if configFile != "" {
		configFiles = append(configFiles, configFile)
		v.SetConfigFile(configFile)
	}

	enforce := ""
	for i, file := range configFiles {
		mergeConfig(v, file)
		if i == 0 {
			enforce = v.GetString("enforce")
		}
	}

	// --server overrides whatever the files said. Bound rather than read
	// directly so viper's own precedence applies: a flag that wasn't passed
	// contributes its empty default at the lowest priority and does not
	// clobber a configured value.
	if serverFlag := cmd.Flags().Lookup("server"); serverFlag != nil {
		if err := v.BindPFlag("server", serverFlag); err != nil {
			return nil, fmt.Errorf("failed to bind the --server flag: %w", err)
		}
	}

	// Merged last, so it wins over every user-writable location — that is the
	// whole point of `enforce`. A relative target resolves inside the system
	// directory, so naming one cannot reach a file the user controls.
	if enforce != "" {
		if !filepath.IsAbs(enforce) {
			enforce = filepath.Join(paths.systemDir, enforce)
		}
		mergeConfig(v, enforce)
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Resolve the key settings now so a bad combination is reported at
	// startup rather than at the first attempt to obtain a certificate.
	// The resolved values are recomputed where they're needed; this call is
	// for validation and for emitting the warnings exactly once.
	_, _, warnings, err := c.ResolveSSHKey()
	if err != nil {
		return nil, fmt.Errorf("invalid ssh key configuration: %w", err)
	}
	for _, w := range warnings {
		slog.Warn(w)
	}

	return &c, nil
}

func mergeConfig(v *viper.Viper, f string) {
	v.SetConfigFile(f)
	_ = v.MergeInConfig() //nolint:errcheck // config override is optional
}
