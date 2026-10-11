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
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/go-viper/mapstructure/v2"
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

	enforced, enforceSource, err := mergeEnforceFile(v, paths, enforce)
	if err != nil {
		return nil, err
	}
	enforceKeys := enforced.keys

	// Merged after `enforce`, so a platform-native policy source (Windows
	// GPO registry, macOS managed preferences) wins over even the enforce
	// file. See policy_windows.go / policy_darwin.go / policy_other.go —
	// each key it sets is treated exactly like an enforce-file key.
	// Forbidden certificate extensions are handled separately because they're
	// not a per-key override but an unconditional floor — see extractForbiddenExtensions.
	policy, err := mergePlatformPolicy(v, loadPolicy)
	if err != nil {
		return nil, err
	}
	policySource := ConfigSource{Label: "platform policy", Status: SourceNotGiven, AdminLock: true}
	if policy.merged {
		policySource.Status = SourceMerged
	}

	// Flags override the user's own files but never a lock. viper reads a
	// bound flag above every merged layer, so a flag for a setting that
	// `enforce` or platform policy sets is not bound at all: bound, it
	// would let the command line beat the administrator. The rest are bound
	// rather than read directly so viper's own precedence applies: a flag
	// that wasn't passed contributes its empty default at the lowest
	// priority and does not clobber a configured value. --key-type/
	// --key-size and certificate extension flags are local to `ssh login`
	// (service enroll also generates a keypair, but from sshkey.* alone),
	// so Lookup returns nil and binding them no-ops for every other
	// command's cmd.
	locked := make(map[string]bool, len(enforceKeys)+len(policy.keys))
	for key := range enforceKeys {
		locked[key] = true
	}
	for key := range policy.keys {
		locked[key] = true
	}
	changed, overridden, setByFlag, err := bindFlags(v, cmd, map[string]string{
		"server":              "server",
		"key-type":            "sshkey.type",
		"key-size":            "sshkey.size",
		"no-pty":              "certificate_extensions.no_pty",
		"no-agent-forwarding": "certificate_extensions.no_agent_forwarding",
		"no-port-forwarding":  "certificate_extensions.no_port_forwarding",
		"no-x11-forwarding":   "certificate_extensions.no_x11_forwarding",
		"no-user-rc":          "certificate_extensions.no_user_rc",
	}, locked)
	if err != nil {
		return nil, err
	}
	// Said at load rather than left to --debug: the user typed the flag
	// and is owed an answer for why it did nothing.
	for _, flag := range overridden {
		slog.Warn("ignoring a command-line flag for a setting an administrator has locked", "flag", flag)
	}

	// Recorded in precedence order, which is not the order the work above
	// happened in: flags sit above the user's files and below both locks.
	sources = append(sources, flagsSource(changed, overridden), enforceSource, policySource)

	// The hook chain is spelled out rather than left to viper's default
	// because capubkey accepts a scalar as well as a sequence, and the
	// default StringToSliceHookFunc(",") would split a single key on a
	// comma in its comment. Ours runs first and takes the conversion; the
	// two defaults follow so nothing else changes.
	var c Config
	if err := v.Unmarshal(&c, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		stringToKeyListHook(),
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	))); err != nil {
		return nil, describeDecodeError(err)
	}
	c.FIPSEnforced = (enforceKeys["fips"] || policy.fipsSet) && c.FIPS != nil && *c.FIPS
	// Both locked sources contribute, so an enforce file is enough on a
	// platform (Linux) that has no native policy store.
	c.ForbiddenCertificateExtensions = unionStrings(enforced.forbiddenExtensions, policy.forbiddenExtensions)
	c.SetByFlag = setByFlag

	// Resolve the key settings now so a bad combination is reported at
	// startup rather than at the first attempt to obtain a certificate.
	// The resolved values are recomputed where they're needed; this call is
	// for validation and for emitting the warnings exactly once.
	_, _, warnings, err := c.ResolveSSHKey()
	if err != nil {
		// Unwrapped: ResolveSSHKey's messages name the flag and the
		// setting already.
		return nil, err
	}
	for _, w := range warnings {
		slog.Warn(w)
	}

	if c.RequestTimeout <= 0 {
		return nil, fmt.Errorf("request_timeout must be a positive duration such as 2m, got %s", c.RequestTimeout)
	}

	if err := c.ResolvePaths(); err != nil {
		return nil, fmt.Errorf("failed to resolve config paths: %w", err)
	}

	c.Sources = sources

	return &c, nil
}

// ExplicitConfigError reports a file named with --config that does not
// exist or will not parse. Callers never downgrade it to a warning, not even
// for a command that reads no setting: the person asked for that file, and a
// run that carries on without it would succeed on settings they did not pick.
type ExplicitConfigError struct {
	Path   string
	Reason string
}

// Error implements error.
func (e *ExplicitConfigError) Error() string {
	return fmt.Sprintf("the config file %q given with --config %s", e.Path, e.Reason)
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
			return "", nil, &ExplicitConfigError{Path: file.path, Reason: source.describeFailure()}
		}
		// A search-path file that is there but will not parse is not the
		// normal absence above: its settings silently not applying is the
		// same surprise, so it is reported even though the run continues.
		if source.Status == SourceError {
			msg := fmt.Sprintf("the %s %q could not be read, so none of its settings apply: %s", file.label, file.path, source.Err)
			if i == 0 {
				msg += "; that includes any enforce file it names, so no administrator lock from it is in effect"
			}
			slog.Warn(msg)
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

// mergeEnforceFile merges the `enforce` file the system file named, if
// any, into v, and returns the settings it sets along with its entry in the
// source chain. Merged last of the files, so it wins over every
// user-writable location — that is the whole point of `enforce`. A relative
// target resolves inside the system directory, so naming one cannot reach a
// file the user controls.
func mergeEnforceFile(v *viper.Viper, paths searchPaths, enforce string) (enforceFile, ConfigSource, error) {
	if enforce == "" {
		return enforceFile{}, ConfigSource{Label: "enforce", Status: SourceNotGiven, AdminLock: true}, nil
	}
	if !filepath.IsAbs(enforce) {
		enforce = filepath.Join(paths.systemDir, enforce)
	}

	// Fail closed, unlike the optional user/local config files merged
	// before it: `enforce` is the admin's mechanism for locking settings a
	// user cannot override, so a missing or malformed enforce file must be a
	// hard startup error rather than silently dropping every locked setting
	// (which is exactly what the control exists to prevent).
	v.SetConfigFile(enforce)
	if err := v.MergeInConfig(); err != nil {
		return enforceFile{}, ConfigSource{}, enforceLoadError(enforce, err)
	}
	found, err := readEnforceFile(enforce)
	if err != nil {
		return enforceFile{}, ConfigSource{}, enforceLoadError(enforce, err)
	}
	warnUnknownSettings(enforce, slices.Sorted(maps.Keys(found.keys)), true)
	return found, ConfigSource{Label: "enforce", Path: enforce, Status: SourceMerged, AdminLock: true}, nil
}

// enforceLoadError reports an enforce file that would not load. It says why
// the command stops rather than carrying on: the locks in it cannot be
// applied, and running without them is what the fail-closed rule forbids.
func enforceLoadError(path string, err error) error {
	return fmt.Errorf("the enforce file %q could not be loaded, so the settings an administrator "+
		"locked cannot be applied: %s", path, parseErrorText(err))
}

// enforceFile is what readEnforceFile found in the enforce file.
type enforceFile struct {
	// keys are the settings it sets, flattened the way viper names them
	// ("sshkey.type"). No command-line flag may override one.
	keys map[string]bool
	// forbiddenExtensions is its forbidden_certificate_extensions, the
	// counterpart of platformPolicy.forbiddenExtensions. Config never
	// decodes that key from the merged chain, so a user file cannot set it.
	forbiddenExtensions []string
}

// unionStrings returns every string in a then b, in that order, once each.
func unionStrings(a, b []string) []string {
	var out []string
	seen := make(map[string]bool, len(a)+len(b))
	for _, s := range slices.Concat(a, b) {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// flagsSource is the command-line flags' entry in the source chain: which
// flags took effect, and which a lock overrode.
func flagsSource(changed, overridden []string) ConfigSource {
	source := ConfigSource{Label: "command-line flags", Status: SourceNotGiven}
	switch {
	case len(changed) > 0:
		source.Status = SourceMerged
		source.Detail = strings.Join(changed, ", ")
		if len(overridden) > 0 {
			source.Detail += "; overridden by a lock: " + strings.Join(overridden, ", ")
		}
	case len(overridden) > 0:
		source.Status = SourceOverridden
		source.Detail = strings.Join(overridden, ", ")
	}
	return source
}

// platformPolicy is what mergePlatformPolicy found in the platform-native
// policy source.
type platformPolicy struct {
	// merged says the source set anything at all, for the debug report's
	// chain.
	merged bool
	// keys are the settings it set, flattened the way viper names them
	// ("sshkey.type"). No command-line flag may override one.
	keys map[string]bool
	// fipsSet says it set fips: true, the counterpart of the enforce file's
	// "fips" key in newConfig's FIPSEnforced computation.
	fipsSet bool
	// forbiddenExtensions is forbidden_certificate_extensions, handled
	// separately because it is not a per-key override but an
	// unconditional floor on which extensions are allowed.
	forbiddenExtensions []string
}

// mergePlatformPolicy loads and merges the platform-native policy source
// (see loadPlatformPolicy and its per-GOOS implementations) into v, and
// reports what it found.
func mergePlatformPolicy(v *viper.Viper, loadPolicy func() (map[string]any, error)) (platformPolicy, error) {
	policy, err := loadPolicy()
	if err != nil {
		return platformPolicy{}, fmt.Errorf("failed to load platform policy: %w", err)
	}
	if len(policy) == 0 {
		return platformPolicy{}, nil
	}
	found := platformPolicy{merged: true}

	// Extract forbidden extensions before merging, since it shouldn't go through
	// viper's normal merge (it's a list that acts as a floor, not an override).
	if forbidden, ok := policy["forbidden_certificate_extensions"]; ok {
		delete(policy, "forbidden_certificate_extensions")
		// Convert []any to []string if present
		if forbiddenSlice, ok := forbidden.([]any); ok {
			for _, ext := range forbiddenSlice {
				if s, ok := ext.(string); ok {
					found.forbiddenExtensions = append(found.forbiddenExtensions, s)
				}
			}
		}
	}

	if fips, ok := policy["fips"].(bool); ok {
		found.fipsSet = fips
	}
	if err := v.MergeConfigMap(policy); err != nil {
		return platformPolicy{}, fmt.Errorf("failed to merge platform policy: %w", err)
	}

	// Read back through a viper of its own, which flattens the nested map
	// into the dotted keys the flags are bound to.
	isolated := viper.New()
	if err := isolated.MergeConfigMap(policy); err != nil {
		return platformPolicy{}, fmt.Errorf("failed to merge platform policy: %w", err)
	}
	found.keys = keySet(isolated.AllKeys())
	return found, nil
}

// keySet turns a list of setting keys into a set.
func keySet(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		set[key] = true
	}
	return set
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
		warnUnknownSettings(f, fileSettings(f), false)
		return ConfigSource{Label: label, Path: f, Status: SourceMerged}
	case isNotExist(err):
		return ConfigSource{Label: label, Path: f, Status: SourceAbsent}
	default:
		return ConfigSource{Label: label, Path: f, Status: SourceError, Err: parseErrorText(err)}
	}
}

// parseErrorText is err's message without viper's "While parsing config: "
// prefix, which arrives capitalised in the middle of a sentence and adds
// nothing the YAML parser's own "yaml: line 3: ..." does not say.
func parseErrorText(err error) string {
	var parseErr viper.ConfigParseError
	if errors.As(err, &parseErr) && errors.Unwrap(parseErr) != nil {
		return errors.Unwrap(parseErr).Error()
	}
	return err.Error()
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
// a command other than the one invoked) and any whose key is locked, which
// an administrator lock sets and no flag may override.
//
// Returns the names of the flags the caller actually passed, sorted, split
// into those that took effect and those a lock overrode, so the merge chain
// can report both rather than only that flags were considered. setByFlag is
// the viper keys the effective flags set, keyed the way Config.SetByFlag
// is — binding loses which layer a value came from, and that is the only
// thing that can tell a user whether to look at their command line or their
// config file.
func bindFlags(v *viper.Viper, cmd *cobra.Command, flagToKey map[string]string, locked map[string]bool) (changed, overridden []string, setByFlag map[string]bool, err error) {
	setByFlag = make(map[string]bool)
	for flag, key := range flagToKey {
		f := cmd.Flags().Lookup(flag)
		if f == nil {
			continue
		}
		if locked[key] {
			if f.Changed {
				overridden = append(overridden, "--"+flag)
			}
			continue
		}
		if err := v.BindPFlag(key, f); err != nil {
			return nil, nil, nil, fmt.Errorf("failed to bind the --%s flag: %w", flag, err)
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
	sort.Strings(overridden)
	return changed, overridden, setByFlag, nil
}

// readEnforceFile returns the settings the enforce file at path sets,
// flattened the way viper names them ("sshkey.type"), and the extensions it
// forbids. Read in isolation from the main viper instance, where the origin
// of a merged value is lost. These are the settings no command-line flag may
// override, so a read that fails is an error rather than an empty set: the
// caller has just merged the same file, and an empty set here would quietly
// unlock all of them.
//
// Parsed as YAML whatever its extension, the same way the main instance
// (SetConfigType("yaml") in newConfig) merged it a moment ago: an enforce
// file named without a recognised extension would otherwise merge fine and
// then fail here with viper's "Unsupported Config Type".
func readEnforceFile(path string) (enforceFile, error) {
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigFile(path)
	if err := v.MergeInConfig(); err != nil {
		return enforceFile{}, err
	}
	return enforceFile{
		keys:                keySet(v.AllKeys()),
		forbiddenExtensions: v.GetStringSlice("forbidden_certificate_extensions"),
	}, nil
}
