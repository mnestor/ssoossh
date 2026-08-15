package config

import (
	"os"
	"path/filepath"
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

	cfg, err := newConfig(cmd, testPaths(sysDir))
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

	cfg, err := newConfig(cmd, testPaths(sysDir))
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

	cfg, err := newConfig(cmd, testPaths(sysDir))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SkipVerifySSL {
		t.Error("expected a bare enforce filename to resolve inside the system directory")
	}
}
