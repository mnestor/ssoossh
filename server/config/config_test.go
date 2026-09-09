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
	if c.Signer.SSHKey != "test-key-material" {
		t.Errorf("got SSHKey %q, want %q", c.Signer.SSHKey, "test-key-material")
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
ssh_key: "test-key-material"
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
	// ssh_key is the one setting NewConfig refuses to default (it is the CA
	// key); everything else in these assertions still comes from defaults.
	writeFile(t, path, `ssh_key: "test-key-material"`)

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
	// ssh_key is the one setting NewConfig refuses to default (it is the CA
	// key); everything else in these assertions still comes from defaults.
	writeFile(t, path, `ssh_key: "test-key-material"`)

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
	// ssh_key is the one setting NewConfig refuses to default (it is the CA
	// key); everything else in these assertions still comes from defaults.
	writeFile(t, path, `ssh_key: "test-key-material"`)

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

func TestNewConfig_ShouldAllowEmptySSHKeyForAPIMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "api-mode.yaml")
	// ssh_key is intentionally left empty for API-only mode
	writeFile(t, path, "")

	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error for API mode without ssh_key, got %v", err)
	}
	if c.Signer.SSHKey != "" {
		t.Errorf("got SSHKey %q, want empty string for API mode", c.Signer.SSHKey)
	}
}

// TestNewConfig_ShouldDecodeRotationKeysDirectlyUnderTheirLoggingBlock
// pins the flat key shape every doc and the shipped defaults.yaml describe:
// the timberjack rotation options belong to the logging block itself, not to
// a level named for the embedded Go type. They only do so because each embed
// carries `mapstructure:",squash"` -- viper's decoder does not squash an
// untagged embedded struct, and without the tag a filename written where the
// docs say to put it is discarded in silence, leaving the destination
// unrouted and its effective-config row empty.
func TestNewConfig_ShouldDecodeRotationKeysDirectlyUnderTheirLoggingBlock(t *testing.T) {
	// Changes the process's working directory via t.Chdir, so it must not
	// run in parallel with other tests that also rely on cwd.
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, filepath.Join(dir, "ssoosshd.yaml"), `
ssh_key: "test-key-material"
logging:
  filename: "/logs/app.log"
  maxsize: 25
db:
  logging:
    filename: "/logs/db.log"
ldap:
  logging:
    filename: "/logs/ldap.log"
audit:
  logging:
    filename: "/logs/audit.log"
http:
  access_logging:
    filename: "/logs/access.log"
mail:
  logging:
    filename: "/logs/mail.log"
`)

	c, err := NewConfig(newTestCommand())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"main log", c.Logging.Filename, "/logs/app.log"},
		{"database log", c.DB.Logging.Filename, "/logs/db.log"},
		{"ldap log", c.LDAP.Logging.Filename, "/logs/ldap.log"},
		{"audit log", c.Audit.Logging.Filename, "/logs/audit.log"},
		{"access log", c.HTTP.AccessLogging.Filename, "/logs/access.log"},
		{"mail log", c.Mail.Logging.Filename, "/logs/mail.log"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got filename %q, want %q", tc.got, tc.want)
			}
		})
	}

	// A second rotation key, to show the whole embedded group is reachable
	// and not just the one field.
	if c.Logging.MaxSize != 25 {
		t.Errorf("got Logging.MaxSize %d, want 25", c.Logging.MaxSize)
	}
}

// TestConfig_EffectiveShouldReportRotationKeysUnderTheirLoggingBlock keeps
// the admin screen's key paths honest against the decoder. The two are
// derived separately -- one by mapstructure, one by config.Effective's own
// walk -- so a divergence shows up as a row an operator can copy into their
// config file and have ignored.
func TestConfig_EffectiveShouldReportRotationKeysUnderTheirLoggingBlock(t *testing.T) {
	// Changes the process's working directory via t.Chdir, so it must not
	// run in parallel with other tests that also rely on cwd.
	dir := t.TempDir()
	t.Chdir(dir)

	writeFile(t, filepath.Join(dir, "ssoosshd.yaml"), `
ssh_key: "test-key-material"
ldap:
  logging:
    filename: "/logs/ldap.log"
`)

	c, err := NewConfig(newTestCommand())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	var found bool
	for _, s := range c.Effective() {
		if s.Key == "ldap.logging.filename" {
			found = true
			if s.Value != "/logs/ldap.log" {
				t.Errorf("got ldap.logging.filename %q, want %q", s.Value, "/logs/ldap.log")
			}
		}
	}
	if !found {
		t.Error("expected ldap.logging.filename in the effective configuration, got no such key")
	}
}

// PKCE is on unless a config file says otherwise, and the switch is a plain
// bool key: the zero value has to be the secure one, since every code path
// that builds a config.Config without reading a file gets it.
func TestNewConfig_ShouldKeepPKCEEnabledByDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	writeFile(t, path, `ssh_key: "test-key-material"`)

	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if c.AuthConfig.DisablePKCE {
		t.Error("got AuthConfig.DisablePKCE true, want false (default): PKCE must stay on unless it is explicitly turned off")
	}
}

// And an operator whose provider refuses a code challenge can turn it off.
func TestNewConfig_ShouldReadDisablePKCEFromTheConfigFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "no-pkce.yaml")
	writeFile(t, path, `
ssh_key: "test-key-material"
authentication:
  disable_pkce: true
`)

	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !c.AuthConfig.DisablePKCE {
		t.Error("got AuthConfig.DisablePKCE false, want true from the config file")
	}
}

// The support contact binds from the branding block, and the label is
// carried alongside the address rather than in a block of its own.
func TestNewConfig_ShouldReadTheBrandingSupportContact(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "support.yaml")
	writeFile(t, path, `
ssh_key: "test-key-material"
branding:
  support_email: "support@example.com"
  support_label: "the IT service desk"
`)

	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"support email", c.Branding.SupportEmail, "support@example.com"},
		{"support label", c.Branding.SupportLabel, "the IT service desk"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// An unconfigured deployment says nothing about support, and the web UI
// keeps its own defaults.
func TestNewConfig_ShouldLeaveTheSupportContactEmptyByDefault(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	writeFile(t, path, `ssh_key: "test-key-material"`)

	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	c, err := NewConfig(cc)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if c.Branding.SupportEmail != "" {
		t.Errorf("got Branding.SupportEmail %q, want empty (default)", c.Branding.SupportEmail)
	}
}

// The address becomes an href on the login page — the one page a locked-out
// user can reach — so a typo fails the server rather than producing a dead
// link nobody who could fix it will ever click.
func TestNewConfig_ShouldRejectAnUnparseableSupportEmail(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "bad-support.yaml")
	writeFile(t, path, `
ssh_key: "test-key-material"
branding:
  support_email: "not an address"
`)

	cc := newTestCommand()
	if err := cc.Flags().Set("config", path); err != nil {
		t.Fatalf("failed to set --config flag: %v", err)
	}

	if _, err := NewConfig(cc); err == nil {
		t.Error("NewConfig() error = nil, want an error for an unparseable branding.support_email")
	}
}
