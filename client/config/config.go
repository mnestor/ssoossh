// Package config defines application configuration and sets up Viper.
//
// This package centralizes configuration loading and access for the client.
package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

//go:embed defaults.yaml
var defaultconfig string

type ctxKey string

const Context ctxKey = "CONFIG"

// configFlagLabel names the --config source in the merge chain. A constant
// because mergeConfigFiles both labels that source and singles it out for
// stricter handling than the search-path locations, and the two must agree.
const configFlagLabel = "--config"

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
// policy_other.go and https://mnestor.github.io/ssoossh/hosts/client-enforcement/.
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

	enforce, sources, err := mergeConfigFiles(v, paths, configFile)
	if err != nil {
		return nil, err
	}

	// Flags override whatever the files said. Bound rather than read
	// directly so viper's own precedence applies: a flag that wasn't passed
	// contributes its empty default at the lowest priority and does not
	// clobber a configured value. --key-type/--key-size and certificate
	// extension flags are local to `ssh login` (the only command that
	// generates a keypair and requests extensions), so Lookup returns nil
	// and binding them no-ops for every other command's cmd.
	changed, setByFlag, err := bindFlags(v, cmd, map[string]string{
		"server":              "server",
		"key-type":            "sshkey.type",
		"key-size":            "sshkey.size",
		"no-pty":              "certificate_extensions.no_pty",
		"no-agent-forwarding": "certificate_extensions.no_agent_forwarding",
		"no-port-forwarding":  "certificate_extensions.no_port_forwarding",
		"no-x11-forwarding":   "certificate_extensions.no_x11_forwarding",
		"no-user-rc":          "certificate_extensions.no_user_rc",
	})
	if err != nil {
		return nil, err
	}
	flagSource := ConfigSource{Label: "command-line flags", Status: SourceNotGiven}
	if len(changed) > 0 {
		flagSource.Status = SourceMerged
		flagSource.Detail = strings.Join(changed, ", ")
	}
	sources = append(sources, flagSource)

	// Merged last, so it wins over every user-writable location — that is the
	// whole point of `enforce`. A relative target resolves inside the system
	// directory, so naming one cannot reach a file the user controls.
	enforceSetsFIPS := false
	if enforce == "" {
		sources = append(sources, ConfigSource{Label: "enforce", Status: SourceNotGiven, AdminLock: true})
	}
	if enforce != "" {
		if !filepath.IsAbs(enforce) {
			enforce = filepath.Join(paths.systemDir, enforce)
		}
		enforceSetsFIPS = enforceFileSets(enforce, "fips")
		// Fail closed, unlike the optional user/local config files merged
		// above: `enforce` is the admin's mechanism for locking settings a
		// user cannot override, so a missing or malformed enforce file must
		// be a hard startup error rather than silently dropping every locked
		// setting (which is exactly what the control exists to prevent).
		v.SetConfigFile(enforce)
		if err := v.MergeInConfig(); err != nil {
			return nil, fmt.Errorf("failed to load enforce file %q: %w", enforce, err)
		}
		sources = append(sources, ConfigSource{Label: "enforce", Path: enforce, Status: SourceMerged, AdminLock: true})
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
	policyStatus := SourceNotGiven
	if policySetsFIPS || len(policyForbiddenExtensions) > 0 {
		policyStatus = SourceMerged
	}
	sources = append(sources, ConfigSource{Label: "platform policy", Status: policyStatus, AdminLock: true})

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}
	c.FIPSEnforced = (enforceSetsFIPS || policySetsFIPS) && c.FIPS != nil && *c.FIPS
	c.ForbiddenCertificateExtensions = policyForbiddenExtensions
	c.SetByFlag = setByFlag

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

	if err := c.ResolvePaths(); err != nil {
		return nil, fmt.Errorf("failed to resolve config paths: %w", err)
	}

	c.Sources = sources

	return &c, nil
}

// mergeConfigFiles merges the file half of the configuration chain into v
// and returns the `enforce` target the system file named, along with the
// outcome of every location consulted.
//
// Split out of newConfig to keep that function readable; the ordering rules
// it encodes are the interesting part and were getting lost among newConfig's
// other concerns.
func mergeConfigFiles(v *viper.Viper, paths searchPaths, configFile string) (enforce string, sources []ConfigSource, err error) {
	sources = []ConfigSource{{Label: "embedded defaults", Status: SourceMerged}}

	// The system file first, so it is the one `enforce` is read from, then
	// each more specific location. A missing file at any of them is normal.
	type candidate struct{ label, path string }
	configFiles := []candidate{{"system file", filepath.Join(paths.systemDir, configFileName)}}
	if paths.userFile != "" {
		configFiles = append(configFiles, candidate{"user file", paths.userFile})
	}
	if paths.localFile != "" {
		configFiles = append(configFiles, candidate{"local file", paths.localFile})
	}
	if configFile != "" {
		configFiles = append(configFiles, candidate{configFlagLabel, configFile})
		v.SetConfigFile(configFile)
	}

	for i, file := range configFiles {
		source := mergeConfig(v, file.label, file.path)
		sources = append(sources, source)
		if i == 0 {
			enforce = v.GetString("enforce")
		}

		// A file the user named on the command line is not the same as a
		// search-path location that happens to be empty. Absence is normal
		// at the search paths and stays silent there. Naming one that is
		// not there, or cannot be parsed, is a typo — and continuing means
		// every setting in it silently does not apply, which surfaces much
		// later as a confusing failure or, worse, as a security setting
		// quietly not in effect.
		//
		// Same reasoning `enforce` below already fails closed on.
		if file.label == configFlagLabel && source.Status != SourceMerged {
			return "", nil, fmt.Errorf("failed to load the config file %q given with --config: %s", file.path, source.describeFailure())
		}
	}
	// Recorded after the loop so the chain reads in precedence order: an
	// explicit --config is merged last of the files above, so its "not
	// given" marker belongs here rather than ahead of them.
	if configFile == "" {
		sources = append(sources, ConfigSource{Label: configFlagLabel, Status: SourceNotGiven})
	}
	return enforce, sources, nil
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

// mergeConfig merges f into v and reports what happened. A missing file is
// normal at every search location, so it is not an error — but it is also
// not the same as a file that exists and failed to parse, and the caller
// records the difference (see ConfigSource) because only one of the two is
// a configuration the user believes is in effect.
func mergeConfig(v *viper.Viper, label, f string) ConfigSource {
	v.SetConfigFile(f)
	err := v.MergeInConfig()
	switch {
	case err == nil:
		return ConfigSource{Label: label, Path: f, Status: SourceMerged}
	case isNotExist(err):
		return ConfigSource{Label: label, Path: f, Status: SourceAbsent}
	default:
		return ConfigSource{Label: label, Path: f, Status: SourceError, Err: err.Error()}
	}
}

// isNotExist reports whether err is viper's or the OS's "no such file". A
// file that is simply not there is the common case at most search
// locations, and must not be reported as a problem.
func isNotExist(err error) bool {
	var notFound viper.ConfigFileNotFoundError
	if errors.As(err, &notFound) {
		return true
	}
	return errors.Is(err, os.ErrNotExist)
}

// bindFlags binds each cmd flag named in flagToKey to the viper key it maps
// to, skipping any flag cmd does not have registered (a leaf-local flag on
// a command other than the one invoked).
// Returns the names of the flags the caller actually passed, sorted, so the
// merge chain can report which flags overrode the files rather than only
// that flags were considered.
// It returns both the display list of changed flags (for the debug report)
// and the set of viper keys those flags set, keyed the way Config.SetByFlag
// is — binding loses which layer a value came from, and that is the only
// thing that can tell a user whether to look at their command line or their
// config file.
func bindFlags(v *viper.Viper, cmd *cobra.Command, flagToKey map[string]string) (changed []string, setByFlag map[string]bool, err error) {
	setByFlag = make(map[string]bool)
	for flag, key := range flagToKey {
		f := cmd.Flags().Lookup(flag)
		if f == nil {
			continue
		}
		if err := v.BindPFlag(key, f); err != nil {
			return nil, nil, fmt.Errorf("failed to bind the --%s flag: %w", flag, err)
		}
		if f.Changed {
			changed = append(changed, "--"+flag)
			setByFlag[key] = true
		}
	}
	// Sorted because flagToKey is a map: without this the reported order
	// varies run to run, and a diagnostic report that differs between two
	// identical invocations wastes the reader's time.
	sort.Strings(changed)
	return changed, setByFlag, nil
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
