package config

// Test methodology: Tests verify config loading from defaults, user files,
// and CLI flags. Some tests mutate cwd (t.Chdir) or environment and cannot
// run in parallel. Uses helper functions to build test cobra.Command objects.
// Each test verifies one specific config loading behavior.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// newTestCommand builds a bare cobra.Command with the same --config/-c flag
// NewConfig expects (mirroring server/cmd.NewCommand, without pulling in
// the bootstrap package).
func newTestCommand() *cobra.Command {
	cc := &cobra.Command{}
	cc.Flags().StringP("config", "c", "", "path to the ssoosshd config file")
	return cc
}

func TestNewConfig_ShouldErrorWhenConfigFlagNotRegistered(t *testing.T) {
	t.Parallel()

	cc := &cobra.Command{} // no --config flag registered

	_, err := NewConfig(cc)
	if err == nil {
		t.Fatal("expected an error when the command has no --config flag, got nil")
	}
}

func TestNewConfig_ShouldErrorWhenNoConfigFileFoundAnywhere(t *testing.T) {
	// Changes the process's working directory via t.Chdir, so it must not
	// run in parallel with other tests that also rely on cwd.
	t.Chdir(t.TempDir())

	cc := newTestCommand()

	_, err := NewConfig(cc)
	if err == nil {
		t.Fatal("expected an error when no ssoosshd.yaml exists in any search path, got nil")
	}

	var notFound viper.ConfigFileNotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("expected error to wrap viper.ConfigFileNotFoundError, got: %v", err)
	}
}

func TestNewConfig_ShouldMergeCwdConfigFileOverDefaults(t *testing.T) {
	// Changes the process's working directory via t.Chdir, so it must not
	// run in parallel with other tests that also rely on cwd.
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, filepath.Join(dir, "ssoosshd.yaml"), `
http:
  port: 9443
ssh_key: "test-key-material"
`)

	cc := newTestCommand()

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if c.HTTP.Port != 9443 {
		t.Errorf("got HTTP.Port %d, want 9443 (from config file)", c.HTTP.Port)
	}
	if c.HTTP.Address != "127.0.0.1" {
		t.Errorf("got HTTP.Address %q, want %q (default, not overridden)", c.HTTP.Address, "127.0.0.1")
	}
	if c.Logging.Level != "WARN" {
		t.Errorf("got Logging.Level %q, want %q (default, not overridden)", c.Logging.Level, "WARN")
	}
	if c.SSHKey != "test-key-material" {
		t.Errorf("got SSHKey %q, want %q", c.SSHKey, "test-key-material")
	}
}

func TestNewConfig_ShouldUseConfigFlagPathWhenSet(t *testing.T) {
	// Changes the process's working directory via t.Chdir, so it must not
	// run in parallel with other tests that also rely on cwd.
	//
	// Put an empty cwd (no ssoosshd.yaml) to prove the flag path is used
	// instead of the search locations.
	t.Chdir(t.TempDir())

	explicitDir := t.TempDir()
	explicitPath := filepath.Join(explicitDir, "custom-name.yaml")
	writeFile(t, explicitPath, `
http:
  port: 8080
`)

	cc := newTestCommand()
	if err := cc.Flags().Set("config", explicitPath); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if c.HTTP.Port != 8080 {
		t.Errorf("got HTTP.Port %d, want 8080 (from --config file)", c.HTTP.Port)
	}
}

func TestNewConfig_ShouldErrorWhenConfigFlagFileDoesNotExist(t *testing.T) {
	t.Parallel()

	cc := newTestCommand()
	if err := cc.Flags().Set("config", "/nonexistent/path/that/should/never/exist.yaml"); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	_, err := NewConfig(cc)
	if err == nil {
		t.Fatal("expected an error when --config points to a nonexistent file, got nil")
	}
}

func TestNewConfig_ShouldErrorWhenConfigFileIsMalformedYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	writeFile(t, path, "not: valid: yaml: [unclosed")

	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	_, err := NewConfig(cc)
	if err == nil {
		t.Fatal("expected an error when the config file has malformed YAML, got nil")
	}
}

func TestNewConfig_ShouldLeaveDefaultsUntouchedWhenConfigFileEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	writeFile(t, path, "")

	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error for an empty (but valid) config file, got %v", err)
	}
	if c.HTTP.Port != 8080 {
		t.Errorf("got HTTP.Port %d, want 8080 (default)", c.HTTP.Port)
	}
	if c.Production != true {
		t.Errorf("got Production %v, want true (default)", c.Production)
	}
	if c.Traces != false {
		t.Errorf("got Traces %v, want false (default)", c.Traces)
	}
	if c.Metrics != false {
		t.Errorf("got Metrics %v, want false (default)", c.Metrics)
	}
}

func TestNewConfig_ShouldDefaultToSecureTLSSettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	writeFile(t, path, "")

	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if c.HTTP.TLS.TLSMinVersion != "TLS1.3" {
		t.Errorf("got TLS min version %q, want %q (default)", c.HTTP.TLS.TLSMinVersion, "TLS1.3")
	}
	// Empty lists defer cipher suite and curve selection to Go's defaults.
	if len(c.HTTP.TLS.CipherSuites) != 0 {
		t.Errorf("got default cipher suites %v, want none", c.HTTP.TLS.CipherSuites)
	}
	if len(c.HTTP.TLS.CurveNames) != 0 {
		t.Errorf("got default curves %v, want none", c.HTTP.TLS.CurveNames)
	}
}

func TestNewConfig_ShouldDefaultHstsToOneYearWithSubdomains(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	writeFile(t, path, "")

	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if c.HTTP.Hsts != "max-age=31536000; includeSubDomains" {
		t.Errorf("got HTTP.Hsts %q, want %q (default)", c.HTTP.Hsts, "max-age=31536000; includeSubDomains")
	}
}

// writeFile writes contents to path, failing the test on any error.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("failed to write test file %s: %v", path, err)
	}
}

func TestNewConfig_ShouldErrorWhenConfigValueHasWrongType(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "wrong-type.yaml")
	// Valid YAML, but http.port cannot be unmarshaled into an int.
	writeFile(t, path, "http:\n  port: not-a-number\n")

	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	_, err := NewConfig(cc)
	if err == nil {
		t.Fatal("expected an error when a config value has the wrong type, got nil")
	}
}
