package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// newConfigCommand builds a cobra command carrying the same flags the real
// root declares, since NewConfig reads both of them off it.
func newConfigCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{Use: "ssoossh", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().StringP("config", "c", "", "path to the ssoossh config file")
	cmd.PersistentFlags().String("server", "", "server address including scheme")
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return cmd
}

// writeConfig writes a config file in a temp dir and returns its path.
func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ssoossh.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// TestNewConfig_ShouldLetTheServerFlagOverrideTheConfigFile is the
// regression test for a flag that was declared, documented, and silently
// ignored: it was never bound, so passing it changed nothing.
func TestNewConfig_ShouldLetTheServerFlagOverrideTheConfigFile(t *testing.T) {
	path := writeConfig(t, "server: https://from-file.example.com\n")
	cmd := newConfigCommand(t, "--config", path, "--server", "https://from-flag.example.com")

	cfg, err := NewConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server != "https://from-flag.example.com" {
		t.Errorf("got server %q, want the flag to win over the file", cfg.Server)
	}
}

// newLoginConfigCommand builds a cobra command carrying --config/--server
// plus the local --key-type/--key-size flags ssh login registers, since
// NewConfig binds those the same way it binds --server.
func newLoginConfigCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{Use: "ssoossh", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.PersistentFlags().StringP("config", "c", "", "path to the ssoossh config file")
	cmd.PersistentFlags().String("server", "", "server address including scheme")
	cmd.Flags().String("key-type", "", "ssh key algorithm")
	cmd.Flags().Int("key-size", 0, "ssh key size")
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return cmd
}

// TestNewConfig_ShouldLetTheKeyTypeFlagOverrideTheConfigFile and its size
// counterpart are the --server tests' pattern, generalized to the two
// flags item 3 adds.
func TestNewConfig_ShouldLetTheKeyTypeFlagOverrideTheConfigFile(t *testing.T) {
	path := writeConfig(t, "sshkey:\n  type: rsa\n")
	cmd := newLoginConfigCommand(t, "--config", path, "--key-type", "ecdsa")

	cfg, err := NewConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SSHKey.Type != SSHKeyTypeECDSA {
		t.Errorf("got sshkey.type %q, want the flag to win over the file", cfg.SSHKey.Type)
	}
}

func TestNewConfig_ShouldKeepTheConfiguredKeyTypeWhenTheFlagIsAbsent(t *testing.T) {
	path := writeConfig(t, "sshkey:\n  type: rsa\n  size: 3072\n")
	cmd := newLoginConfigCommand(t, "--config", path)

	cfg, err := NewConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SSHKey.Type != SSHKeyTypeRSA {
		t.Errorf("got sshkey.type %q, want the configured value preserved", cfg.SSHKey.Type)
	}
	if cfg.SSHKey.Size != 3072 {
		t.Errorf("got sshkey.size %d, want the configured value preserved", cfg.SSHKey.Size)
	}
}

func TestNewConfig_ShouldLetTheKeySizeFlagOverrideTheConfigFile(t *testing.T) {
	path := writeConfig(t, "sshkey:\n  type: ecdsa\n  size: 256\n")
	cmd := newLoginConfigCommand(t, "--config", path, "--key-size", "384")

	cfg, err := NewConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SSHKey.Size != 384 {
		t.Errorf("got sshkey.size %d, want the flag to win over the file", cfg.SSHKey.Size)
	}
}

// TestNewConfig_ShouldKeepTheConfiguredServerWhenTheFlagIsAbsent is the other
// half, and the reason the flag is bound rather than read directly: an unset
// flag must not overwrite a configured value with its empty default.
func TestNewConfig_ShouldKeepTheConfiguredServerWhenTheFlagIsAbsent(t *testing.T) {
	path := writeConfig(t, "server: https://from-file.example.com\n")
	cmd := newConfigCommand(t, "--config", path)

	cfg, err := NewConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server != "https://from-file.example.com" {
		t.Errorf("got server %q, want the configured value preserved", cfg.Server)
	}
}

// TestNewConfig_ShouldDefaultToUsingTheAgent pins the default that decides
// where every key ends up. It is only meaningful because `use_agent` is now
// actually read: a false zero value would quietly move every user to key
// files, which `ssh proxycommand` does not support.
func TestNewConfig_ShouldDefaultToUsingTheAgent(t *testing.T) {
	cmd := newConfigCommand(t, "--config", writeConfig(t, "server: https://ssh.example.com\n"))

	cfg, err := NewConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.UseAgent {
		t.Error("expected use_agent to default to true")
	}
}

func TestNewConfig_ShouldLetTheFileTurnTheAgentOff(t *testing.T) {
	path := writeConfig(t, "server: https://ssh.example.com\nuse_agent: false\n")
	cmd := newConfigCommand(t, "--config", path)

	cfg, err := NewConfig(cmd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.UseAgent {
		t.Error("expected use_agent: false to be honored")
	}
}

func TestNewConfig_ShouldRejectAnUnusableKeyConfiguration(t *testing.T) {
	path := writeConfig(t, "sshkey:\n  type: rsa\n  size: 512\n")
	cmd := newConfigCommand(t, "--config", path)

	if _, err := NewConfig(cmd); err == nil {
		t.Fatal("expected an RSA size below the minimum to fail at load time")
	}
}

// noPolicy stands in for loadPlatformPolicy in tests that aren't exercising
// platform-native policy, so they run identically regardless of the CI
// machine's GOOS.
func noPolicy() (map[string]any, error) { return nil, nil }

// errPlatformPolicyTest is a sentinel for
// TestNewConfig_ShouldSurfaceAPlatformPolicyLoadError.
var errPlatformPolicyTest = errors.New("test: platform policy load failure")

// writeSystemConfig lays out a system configuration directory the way
// /etc/ssoossh would look, and returns its path.
func testPaths(sysDir string) searchPaths {
	// No user or local file: a test must not be influenced by whatever the
	// developer has in their home or working directory.
	return searchPaths{systemDir: sysDir}
}

func writeSystemConfig(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// TestNewConfig_ShouldLetAnEnforcedFileOverrideTheUsersOwnConfig is the
// mechanism an administrator relies on: a setting pinned in the enforced file
// must beat the user's own, whatever they put in it. Without this the lock is
// decoration.
func TestNewConfig_ShouldLetAnEnforcedFileOverrideTheUsersOwnConfig(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "enforce: locked.yaml\n",
		"locked.yaml":  "insecure_skip_verify: false\n",
	})
	userConfig := writeConfig(t, "server: https://ssh.example.com\ninsecure_skip_verify: true\n")
	cmd := newConfigCommand(t, "--config", userConfig)

	cfg, err := newConfig(cmd, testPaths(sysDir), noPolicy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SkipVerifySSL {
		t.Error("the user's insecure_skip_verify: true overrode the enforced value")
	}
	// The enforced file pins one setting; everything else still comes from
	// the user's config.
	if cfg.Server != "https://ssh.example.com" {
		t.Errorf("got server %q, want the user's value preserved", cfg.Server)
	}
}

// TestNewConfig_ShouldFailClosedWhenTheEnforceFileIsMalformed is the
// fail-closed guarantee for the lock: a malformed enforce file must abort
// startup, not be silently skipped, because skipping it drops every setting
// the administrator pinned and lets the user's own config win — exactly the
// bypass the mechanism exists to prevent.
func TestNewConfig_ShouldFailClosedWhenTheEnforceFileIsMalformed(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "enforce: locked.yaml\n",
		"locked.yaml":  "insecure_skip_verify: : not valid yaml {{{\n",
	})
	userConfig := writeConfig(t, "insecure_skip_verify: true\n")
	cmd := newConfigCommand(t, "--config", userConfig)

	if _, err := newConfig(cmd, testPaths(sysDir), noPolicy); err == nil {
		t.Error("newConfig() error = nil, want an error for a malformed enforce file")
	}
}

// TestNewConfig_ShouldFailClosedWhenTheEnforceFileIsMissing covers the other
// way the lock can silently vanish: naming an enforce file that does not
// exist. Like a malformed file, a missing one must be a hard error rather
// than a no-op.
func TestNewConfig_ShouldFailClosedWhenTheEnforceFileIsMissing(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "enforce: nonexistent.yaml\n",
	})
	cmd := newConfigCommand(t, "--config", writeConfig(t, ""))

	if _, err := newConfig(cmd, testPaths(sysDir), noPolicy); err == nil {
		t.Error("newConfig() error = nil, want an error for a missing enforce file")
	}
}

// TestNewConfig_ShouldIgnoreEnforceOutsideTheSystemFile keeps the lock from
// being self-service: a user who can write their own config must not be able
// to point `enforce` at a file of their choosing.
func TestNewConfig_ShouldIgnoreEnforceOutsideTheSystemFile(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "insecure_skip_verify: false\n",
	})
	// The user asks for an enforced file that would turn reuse back on.
	userDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(userDir, "mine.yaml"), []byte("insecure_skip_verify: true\n"), 0o600); err != nil {
		t.Fatalf("write user enforce target: %v", err)
	}
	userConfig := writeConfig(t, "enforce: "+filepath.Join(userDir, "mine.yaml")+"\n")
	cmd := newConfigCommand(t, "--config", userConfig)

	cfg, err := newConfig(cmd, testPaths(sysDir), noPolicy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SkipVerifySSL {
		t.Error("a user-supplied enforce target was honored, defeating the system setting")
	}
}

// TestNewConfig_ShouldResolveARelativeEnforceInsideTheSystemDirectory is the
// other half of that: a bare filename must not be read relative to the
// working directory, which any user controls.
func TestNewConfig_ShouldResolveARelativeEnforceInsideTheSystemDirectory(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "enforce: locked.yaml\n",
		"locked.yaml":  "insecure_skip_verify: false\n",
	})
	cmd := newConfigCommand(t, "--config", writeConfig(t, "insecure_skip_verify: true\n"))

	cfg, err := newConfig(cmd, testPaths(sysDir), noPolicy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SkipVerifySSL {
		t.Error("expected a bare enforce filename to resolve inside the system directory")
	}
}

// TestNewConfig_ShouldRejectAnUnapprovedKeyTypeWhenFIPSIsEnforced is the
// point of splitting FIPS in two: an administrator who locks fips: true via
// the enforce file expects it actually enforced, not merely advised.
func TestNewConfig_ShouldRejectAnUnapprovedKeyTypeWhenFIPSIsEnforced(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "enforce: locked.yaml\n",
		"locked.yaml":  "fips: true\n",
	})
	userConfig := writeConfig(t, "sshkey:\n  type: ed25519\n")
	cmd := newConfigCommand(t, "--config", userConfig)

	if _, err := newConfig(cmd, testPaths(sysDir), noPolicy); err == nil {
		t.Fatal("expected an enforced fips: true to reject a non-FIPS-approved key type")
	}
}

// TestNewConfig_ShouldAcceptAnApprovedKeyTypeWhenFIPSIsEnforced pins that
// FIPSEnforced only rejects the unapproved case, not FIPS mode itself.
func TestNewConfig_ShouldAcceptAnApprovedKeyTypeWhenFIPSIsEnforced(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "enforce: locked.yaml\n",
		"locked.yaml":  "fips: true\n",
	})
	userConfig := writeConfig(t, "sshkey:\n  type: ecdsa\n  size: 256\n")
	cmd := newConfigCommand(t, "--config", userConfig)

	cfg, err := newConfig(cmd, testPaths(sysDir), noPolicy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.FIPSEnforced {
		t.Error("expected FIPSEnforced to be true when the enforce file sets fips: true")
	}
}

// TestNewConfig_ShouldRejectAnUnapprovedKeyTypeWhenFIPSComesFromUserConfig
// confirms FIPS enforcement no longer distinguishes where `fips: true` came
// from: a non-approved key type is a hard error whether it's the system
// enforce file or the user's own config that turned FIPS on. Only an
// explicit `fips: false` is an escape hatch — see
// TestNewConfig_ShouldAcceptANonFIPSKeyTypeWhenFIPSIsExplicitlyDisabled.
func TestNewConfig_ShouldRejectAnUnapprovedKeyTypeWhenFIPSComesFromUserConfig(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "server: https://ssh.example.com\n",
	})
	userConfig := writeConfig(t, "fips: true\nsshkey:\n  type: ed25519\n")
	cmd := newConfigCommand(t, "--config", userConfig)

	if _, err := newConfig(cmd, testPaths(sysDir), noPolicy); err == nil {
		t.Fatal("expected user-set fips: true to reject a non-FIPS-approved key type")
	}
}

// TestNewConfig_ShouldAcceptANonFIPSKeyTypeWhenFIPSIsExplicitlyDisabled pins
// the one remaining escape hatch: `fips: false` in a user-writable config
// still allows a non-approved key type.
func TestNewConfig_ShouldAcceptANonFIPSKeyTypeWhenFIPSIsExplicitlyDisabled(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "server: https://ssh.example.com\n",
	})
	userConfig := writeConfig(t, "fips: false\nsshkey:\n  type: ed25519\n")
	cmd := newConfigCommand(t, "--config", userConfig)

	cfg, err := newConfig(cmd, testPaths(sysDir), noPolicy)
	if err != nil {
		t.Fatalf("expected fips: false to allow a non-FIPS-approved key type, got: %v", err)
	}
	if cfg.FIPSEnforced {
		t.Error("expected FIPSEnforced to be false when fips came from the user's own config")
	}
}

// TestNewConfig_ShouldLetPlatformPolicyOverrideTheEnforcedFile is the
// mechanism an administrator managing the fleet via GPO/MDM relies on: a
// platform-native policy value must beat even the enforce file, exactly as
// the enforce file beats the user's own config.
func TestNewConfig_ShouldLetPlatformPolicyOverrideTheEnforcedFile(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "enforce: locked.yaml\n",
		"locked.yaml":  "server: https://from-enforce-file.example.com\n",
	})
	cmd := newConfigCommand(t, "--config", writeConfig(t, "server: https://from-user.example.com\n"))
	policy := func() (map[string]any, error) {
		return map[string]any{"server": "https://from-platform-policy.example.com"}, nil
	}

	cfg, err := newConfig(cmd, testPaths(sysDir), policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server != "https://from-platform-policy.example.com" {
		t.Errorf("got server %q, want the platform policy value to win over the enforce file", cfg.Server)
	}
}

// TestNewConfig_ShouldLeaveUnsetPolicyKeysToTheEnforcedFile confirms a
// platform policy that only sets some keys doesn't clobber the rest —
// matching how the enforce file already behaves for a partial override.
func TestNewConfig_ShouldLeaveUnsetPolicyKeysToTheEnforcedFile(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "enforce: locked.yaml\n",
		"locked.yaml":  "server: https://from-enforce-file.example.com\ninsecure_skip_verify: true\n",
	})
	cmd := newConfigCommand(t, "--config", writeConfig(t, ""))
	policy := func() (map[string]any, error) {
		return map[string]any{"insecure_skip_verify": false}, nil
	}

	cfg, err := newConfig(cmd, testPaths(sysDir), policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SkipVerifySSL {
		t.Error("expected the platform policy's insecure_skip_verify: false to win")
	}
	if cfg.Server != "https://from-enforce-file.example.com" {
		t.Errorf("got server %q, want the enforce file's value preserved", cfg.Server)
	}
}

// TestNewConfig_ShouldSetFIPSEnforcedWhenPlatformPolicySetsFIPS mirrors
// TestNewConfig_ShouldAcceptAnApprovedKeyTypeWhenFIPSIsEnforced for the
// platform-policy source: an org locking fips via GPO/MDM expects the same
// hard enforcement as the enforce file.
func TestNewConfig_ShouldSetFIPSEnforcedWhenPlatformPolicySetsFIPS(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "server: https://ssh.example.com\n",
	})
	userConfig := writeConfig(t, "sshkey:\n  type: ecdsa\n  size: 256\n")
	cmd := newConfigCommand(t, "--config", userConfig)
	policy := func() (map[string]any, error) {
		return map[string]any{"fips": true}, nil
	}

	cfg, err := newConfig(cmd, testPaths(sysDir), policy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !cfg.FIPSEnforced {
		t.Error("expected FIPSEnforced to be true when platform policy sets fips: true")
	}
}

// TestNewConfig_ShouldRejectAnUnapprovedKeyTypeWhenPlatformPolicySetsFIPS is
// the platform-policy counterpart of the enforce-file FIPS hard-error test.
func TestNewConfig_ShouldRejectAnUnapprovedKeyTypeWhenPlatformPolicySetsFIPS(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "server: https://ssh.example.com\n",
	})
	userConfig := writeConfig(t, "sshkey:\n  type: ed25519\n")
	cmd := newConfigCommand(t, "--config", userConfig)
	policy := func() (map[string]any, error) {
		return map[string]any{"fips": true}, nil
	}

	if _, err := newConfig(cmd, testPaths(sysDir), policy); err == nil {
		t.Fatal("expected a platform-policy fips: true to reject a non-FIPS-approved key type")
	}
}

// TestNewConfig_ShouldSurfaceAPlatformPolicyLoadError confirms a failure
// reading the platform policy source (e.g. a malformed registry value or
// plist) fails config loading rather than silently proceeding unlocked.
func TestNewConfig_ShouldSurfaceAPlatformPolicyLoadError(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "server: https://ssh.example.com\n",
	})
	cmd := newConfigCommand(t, "--config", writeConfig(t, ""))
	policy := func() (map[string]any, error) {
		return nil, errPlatformPolicyTest
	}

	if _, err := newConfig(cmd, testPaths(sysDir), policy); err == nil {
		t.Fatal("expected a platform policy load error to fail config loading")
	}
}

// sourceFor returns the recorded outcome for the named source, so a test can
// assert one entry without pinning the whole chain's length.
func sourceFor(t *testing.T, sources []ConfigSource, label string) ConfigSource {
	t.Helper()
	for _, s := range sources {
		if s.Label == label {
			return s
		}
	}
	t.Fatalf("no %q entry in the recorded sources: %v", label, sources)
	return ConfigSource{}
}

// The gap this closes: mergeConfig ignores errors, because a missing file at
// any search location is normal. That also swallows a config file that IS
// there and failed to parse — the user believes their settings are in
// effect, and nothing says otherwise. --debug reads these outcomes.
//
// Driven through the system file rather than --config: a search-path
// location is where "optional" genuinely applies, and it is the case this
// recording exists for. A malformed file named explicitly with --config is
// a hard error instead — see
// TestNewConfig_ShouldFailWhenTheExplicitConfigFileIsMalformed.
func TestNewConfig_ShouldRecordAMalformedConfigFileAsAnError(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{"ssoossh.yaml": "server: https://sys.example.com\nbroken: [unclosed\n"})
	cmd := newConfigCommand(t)

	cfg, err := newConfig(cmd, testPaths(sysDir), noPolicy)
	if err != nil {
		t.Fatalf("a malformed optional config must not fail the load: %v", err)
	}

	got := sourceFor(t, cfg.Sources, "system file")
	if got.Status != SourceError {
		t.Fatalf("status = %q, want %q (sources: %v)", got.Status, SourceError, cfg.Sources)
	}
	if got.Err == "" {
		t.Error("expected the parse failure to be recorded, got an empty message")
	}
}

func TestNewConfig_ShouldRecordAMergedConfigFile(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{"ssoossh.yaml": "server: https://sys.example.com\n"})
	cmd := newConfigCommand(t)

	cfg, err := newConfig(cmd, testPaths(sysDir), noPolicy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := sourceFor(t, cfg.Sources, "system file").Status; got != SourceMerged {
		t.Errorf("system file status = %q, want %q", got, SourceMerged)
	}
}

func TestNewConfig_ShouldRecordAnAbsentConfigFileAsAbsent(t *testing.T) {
	cmd := newConfigCommand(t)

	cfg, err := newConfig(cmd, testPaths(t.TempDir()), noPolicy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := sourceFor(t, cfg.Sources, "system file").Status; got != SourceAbsent {
		t.Errorf("system file status = %q, want %q", got, SourceAbsent)
	}
}

func TestNewConfig_ShouldRecordAnUnusedConfigFlagAsNotGiven(t *testing.T) {
	cmd := newConfigCommand(t)

	cfg, err := newConfig(cmd, testPaths(t.TempDir()), noPolicy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := sourceFor(t, cfg.Sources, "--config").Status; got != SourceNotGiven {
		t.Errorf("--config status = %q, want %q", got, SourceNotGiven)
	}
}

// The chain is only meaningful in precedence order: a reader uses it to work
// out which source won, and that answer is the last merged entry.
func TestNewConfig_ShouldRecordSourcesInPrecedenceOrder(t *testing.T) {
	sysDir := writeSystemConfig(t, map[string]string{"ssoossh.yaml": "server: https://sys.example.com\n"})
	cmd := newConfigCommand(t, "--config", writeConfig(t, "server: https://ssh.example.com\n"))

	cfg, err := newConfig(cmd, testPaths(sysDir), noPolicy)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var labels []string
	for _, s := range cfg.Sources {
		labels = append(labels, s.Label)
	}
	want := []string{"embedded defaults", "system file", "--config", "command-line flags", "enforce", "platform policy"}
	if len(labels) != len(want) {
		t.Fatalf("sources = %v, want %v", labels, want)
	}
	for i := range want {
		if labels[i] != want[i] {
			t.Fatalf("sources = %v, want %v", labels, want)
		}
	}
}

// A --config the user named explicitly is not the same as a search-path
// location that happens to be empty. Absence is normal at the search paths
// and is deliberately not an error there; naming a file that is not there
// is a typo, and continuing means the user's settings silently do not
// apply. If their file set `server:` they get a confusing failure much
// later; if it set `use_agent: false` they quietly keep using the agent the
// setting exists to avoid.
//
// Same reasoning the `enforce` file already fails closed on, for the same
// reason: silently dropping settings someone asked for is the failure mode
// worth refusing.
func TestNewConfig_ShouldFailWhenTheExplicitConfigFileIsMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-here.yaml")
	cmd := newConfigCommand(t, "--config", missing)

	_, err := newConfig(cmd, testPaths(t.TempDir()), noPolicy)
	if err == nil {
		t.Fatal("expected an error for a --config file that does not exist")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("expected the error to name the file, got %v", err)
	}
}

// Malformed is the other half: viper skips a file it cannot parse, so
// without this the user's settings vanish just as quietly.
func TestNewConfig_ShouldFailWhenTheExplicitConfigFileIsMalformed(t *testing.T) {
	path := writeConfig(t, "server: [unterminated\n")
	cmd := newConfigCommand(t, "--config", path)

	_, err := newConfig(cmd, testPaths(t.TempDir()), noPolicy)
	if err == nil {
		t.Fatal("expected an error for a --config file that cannot be parsed")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("expected the error to name the file, got %v", err)
	}
}

// The search-path locations keep their existing behaviour: absent is the
// common case and must stay silent, or every machine without a system file
// fails to start.
func TestNewConfig_ShouldStillIgnoreAbsentSearchPathFiles(t *testing.T) {
	cmd := newConfigCommand(t)

	if _, err := newConfig(cmd, testPaths(t.TempDir()), noPolicy); err != nil {
		t.Errorf("expected absent search-path files to be tolerated, got %v", err)
	}
}
