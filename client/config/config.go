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
//
// Finally, a platform-native policy source is merged on top of even that:
// a Windows registry key managed via Group Policy, or macOS managed
// preferences pushed by MDM. See policy_windows.go / policy_darwin.go /
// policy_other.go and docs/client-settings-enforcement.md.
func NewConfig(cmd *cobra.Command) (*Config, error) {
	return newConfig(cmd, defaultSearchPaths(), loadPlatformPolicy)
}

// newConfig is NewConfig with the search locations and the platform policy
// loader as parameters, so the `enforce` mechanism and platform-native
// policy precedence can both be tested without writing to a system
// directory or the real registry/managed-preferences locations, and so
// tests do not pick up the developer's own configuration or the CI
// machine's GOOS. Production callers go through NewConfig.
func newConfig(cmd *cobra.Command, paths searchPaths, loadPolicy func() (map[string]any, error)) (*Config, error) {
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

	// Flags override whatever the files said. Bound rather than read
	// directly so viper's own precedence applies: a flag that wasn't passed
	// contributes its empty default at the lowest priority and does not
	// clobber a configured value. --key-type/--key-size and certificate
	// extension flags are local to `ssh login` (the only command that
	// generates a keypair and requests extensions), so Lookup returns nil
	// and binding them no-ops for every other command's cmd.
	if err := bindFlags(v, cmd, map[string]string{
		"server":              "server",
		"key-type":            "sshkey.type",
		"key-size":            "sshkey.size",
		"no-pty":              "certificate_extensions.no_pty",
		"no-agent-forwarding": "certificate_extensions.no_agent_forwarding",
		"no-port-forwarding":  "certificate_extensions.no_port_forwarding",
		"no-x11-forwarding":   "certificate_extensions.no_x11_forwarding",
		"no-user-rc":          "certificate_extensions.no_user_rc",
	}); err != nil {
		return nil, err
	}

	// Merged last, so it wins over every user-writable location — that is the
	// whole point of `enforce`. A relative target resolves inside the system
	// directory, so naming one cannot reach a file the user controls.
	enforceSetsFIPS := false
	if enforce != "" {
		if !filepath.IsAbs(enforce) {
			enforce = filepath.Join(paths.systemDir, enforce)
		}
		enforceSetsFIPS = enforceFileSets(enforce, "fips")
		mergeConfig(v, enforce)
	}

	// Merged after `enforce`, so a platform-native policy source (Windows
	// GPO registry, macOS managed preferences) wins over even the enforce
	// file. See policy_windows.go / policy_darwin.go / policy_other.go —
	// each key it sets is treated exactly like an enforce-file key.
	// Forbidden certificate extensions are handled separately because they're
	// not a per-key override but an unconditional floor — see extractForbiddenExtensions.
	policySetsFIPS, policyForbiddenExtensions, err := mergePlatformPolicy(v, loadPolicy)
	if err != nil {
		return nil, err
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	c.FIPSEnforced = (enforceSetsFIPS || policySetsFIPS) && c.FIPS != nil && *c.FIPS
	c.ForbiddenCertificateExtensions = policyForbiddenExtensions

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

// mergePlatformPolicy loads and merges the platform-native policy source
// (see loadPlatformPolicy and its per-GOOS implementations) into v, and
// reports whether it set fips: true — the platform-policy counterpart of
// enforceFileSets, used the same way by newConfig's FIPSEnforced
// computation. It also extracts and returns the forbidden_certificate_extensions
// list, which is handled separately because it's not a per-key override but
// an unconditional floor on which extensions are allowed.
func mergePlatformPolicy(v *viper.Viper, loadPolicy func() (map[string]any, error)) (fipsSet bool, forbiddenExtensions []string, err error) {
	policy, err := loadPolicy()
	if err != nil {
		return false, nil, fmt.Errorf("failed to load platform policy: %w", err)
	}
	if len(policy) == 0 {
		return false, nil, nil
	}

	// Extract forbidden extensions before merging, since it shouldn't go through
	// viper's normal merge (it's a list that acts as a floor, not an override).
	if forbidden, ok := policy["forbidden_certificate_extensions"]; ok {
		delete(policy, "forbidden_certificate_extensions")
		// Convert []any to []string if present
		if forbiddenSlice, ok := forbidden.([]any); ok {
			for _, ext := range forbiddenSlice {
				if s, ok := ext.(string); ok {
					forbiddenExtensions = append(forbiddenExtensions, s)
				}
			}
		}
	}

	if fips, ok := policy["fips"].(bool); ok {
		fipsSet = fips
	}
	if err := v.MergeConfigMap(policy); err != nil {
		return false, nil, fmt.Errorf("failed to merge platform policy: %w", err)
	}
	return fipsSet, forbiddenExtensions, nil
}

func mergeConfig(v *viper.Viper, f string) {
	v.SetConfigFile(f)
	_ = v.MergeInConfig() //nolint:errcheck // config override is optional
}

// bindFlags binds each cmd flag named in flagToKey to the viper key it maps
// to, skipping any flag cmd does not have registered (a leaf-local flag on
// a command other than the one invoked).
func bindFlags(v *viper.Viper, cmd *cobra.Command, flagToKey map[string]string) error {
	for flag, key := range flagToKey {
		f := cmd.Flags().Lookup(flag)
		if f == nil {
			continue
		}
		if err := v.BindPFlag(key, f); err != nil {
			return fmt.Errorf("failed to bind the --%s flag: %w", flag, err)
		}
	}
	return nil
}

// enforceFileSets reports whether the enforce file at path explicitly sets
// key, read in isolation from the main viper instance so the origin of a
// merged value isn't lost by the time it's unmarshaled. A read failure
// (missing/malformed file) is not reported here: mergeConfig re-reads the
// same file right after this call and is where a genuine problem surfaces.
func enforceFileSets(path, key string) bool {
	v := viper.New()
	v.SetConfigFile(path)
	if err := v.MergeInConfig(); err != nil {
		return false
	}
	return v.IsSet(key)
}
