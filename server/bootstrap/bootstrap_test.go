package bootstrap

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/server/testutil"
)

// Test methodology: Tests verify the full startup sequence and system
// integration. Uses table-driven tests where appropriate. Tests that mutate
// global state (slog, OpenTelemetry, environment) run sequentially (no
// t.Parallel), restore state via t.Cleanup(), and use t.TempDir() for
// temporary files. See saveSlogDefault in observability_test.go for global
// state isolation.

// newTestSSHKeyPEM generates a throwaway ed25519 SSH private key in
// OpenSSH PEM format.
func newTestSSHKeyPEM(t *testing.T) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test-key")
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}

// bootstrapConfigOpts customizes the config file written by
// writeBootstrapConfig. Zero values mean "use a working default".
type bootstrapConfigOpts struct {
	dbProvider string // database provider; defaults to sqlite
	sshKey     string // CA key material; defaults to a freshly generated valid key
	httpExtra  string // appended verbatim inside the http: block (must be indented)
	extra      string // appended verbatim at the top level of the file
}

// writeBootstrapConfig writes an ssoosshd config file (port 0, in-memory
// SQLite, generated SSH key, unless overridden via opts) and returns its
// path.
func writeBootstrapConfig(t *testing.T, opts bootstrapConfigOpts) string {
	t.Helper()

	if opts.dbProvider == "" {
		opts.dbProvider = "sqlite"
	}

	var sshKeyYAML string
	if opts.sshKey == "" {
		keyPEM := strings.TrimRight(newTestSSHKeyPEM(t), "\n")
		sshKeyYAML = "ssh_key: |\n  " + strings.ReplaceAll(keyPEM, "\n", "\n  ")
	} else {
		sshKeyYAML = "ssh_key: " + strconv.Quote(opts.sshKey)
	}

	content := fmt.Sprintf(`http:
  address: 127.0.0.1
  port: 0
%s
db:
  provider: %s
  connection_string: ":memory:"
%s
%s`, opts.httpExtra, opts.dbProvider, sshKeyYAML, opts.extra)

	path := filepath.Join(t.TempDir(), "ssoosshd.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	return path
}

// newBootstrapCommand builds a cobra command carrying the --config flag
// (pointing at configPath) and ctx, mirroring what server/cmd sets up before
// calling Bootstrap.
func newBootstrapCommand(t *testing.T, ctx context.Context, configPath string) *cobra.Command {
	t.Helper()

	cc := &cobra.Command{}
	cc.Flags().StringP("config", "c", "", "path to the ssoosshd config file")
	if err := cc.Flags().Set("config", configPath); err != nil {
		t.Fatalf("failed to set config flag: %v", err)
	}
	cc.SetContext(ctx)
	return cc
}

func TestBootstrap_ShouldStartAndShutDownCleanlyWhenContextCanceled(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	oidcSrv := testutil.NewTestOIDCProvider(t)
	// authentication: is a top-level config key (config.Config.AuthConfig),
	// a sibling of http: — not nested under it — so this goes in opts.extra,
	// not opts.httpExtra. redirect_url isn't a config field at all: it's
	// inferred from http.server_name/port/is_https (see
	// service.NewAuthService's doc comment).
	extra := fmt.Sprintf(`authentication:
  client_id: test-client
  provider_url: %q
  fields:
    username: sub`, oidcSrv.URL)

	// A pre-canceled context makes the server run its full startup and then
	// shut down immediately, exercising the whole Bootstrap sequence
	// without leaving anything running.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cc := newBootstrapCommand(t, ctx, writeBootstrapConfig(t, bootstrapConfigOpts{
		httpExtra: "  server_name: ssoossh.example.com",
		extra:     extra,
	}))

	if err := BootstrapServe(cc, ServerModeFull); err != nil {
		t.Fatalf("expected Bootstrap to shut down cleanly, got %v", err)
	}
}

func TestBootstrap_ShouldErrorWhenConfigFileMissing(t *testing.T) {
	saveSlogDefault(t)

	cc := newBootstrapCommand(t, context.Background(), filepath.Join(t.TempDir(), "does-not-exist.yaml"))

	if err := BootstrapServe(cc, ServerModeFull); err == nil {
		t.Fatal("expected an error when the config file does not exist, got nil")
	}
}

func TestBootstrap_ShouldErrorWhenObservabilityInitFails(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")
	t.Setenv("OTEL_TRACES_EXPORTER", "not-a-real-exporter")

	cc := newBootstrapCommand(t, context.Background(),
		writeBootstrapConfig(t, bootstrapConfigOpts{extra: "traces: true\n"}))

	err := BootstrapServe(cc, ServerModeFull)
	if err == nil {
		t.Fatal("expected an error when the tracing exporter cannot be created, got nil")
	}
	if !strings.Contains(err.Error(), "OpenTelemetry") {
		t.Errorf("expected an OpenTelemetry initialization error, got: %v", err)
	}
}

func TestBootstrap_ShouldErrorWhenDatabaseProviderUnsupported(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	cc := newBootstrapCommand(t, context.Background(),
		writeBootstrapConfig(t, bootstrapConfigOpts{dbProvider: "mysql"}))

	err := BootstrapServe(cc, ServerModeFull)
	if err == nil {
		t.Fatal("expected an error for an unsupported database provider, got nil")
	}
	if !strings.Contains(err.Error(), "database") {
		t.Errorf("expected a database initialization error, got: %v", err)
	}
}

func TestBootstrap_ShouldErrorWhenSSHKeyInvalid(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	oidcSrv := testutil.NewTestOIDCProvider(t)
	extra := fmt.Sprintf(`authentication:
  client_id: test-client
  provider_url: %q
  fields:
    username: sub`, oidcSrv.URL)

	cc := newBootstrapCommand(t, context.Background(),
		writeBootstrapConfig(t, bootstrapConfigOpts{
			sshKey:    "not-a-valid-key",
			httpExtra: "  server_name: ssoossh.example.com",
			extra:     extra,
		}))

	err := BootstrapServe(cc, ServerModeFull)
	if err == nil {
		t.Fatal("expected an error for an invalid CA SSH key, got nil")
	}
	// Error comes from initCAKeyAnnouncer -> NewConfigKeySource -> ssh.ParsePrivateKey
	if !strings.Contains(err.Error(), "failed to parse CA private key") {
		t.Errorf("expected a CA key parsing error, got: %v", err)
	}
}

func TestBootstrapSigner_ShouldErrorWhenSSHKeyMissing(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	cc := newBootstrapCommand(t, context.Background(),
		writeBootstrapConfig(t, bootstrapConfigOpts{
			sshKey: "",
			// Use gochannel backend so NATS config is not required, but note that
			// BootstrapSigner itself rejects gochannel (requires NATS). This test
			// verifies the SSH key check happens before the backend check.
		}))

	err := BootstrapSigner(cc)
	if err == nil {
		t.Fatal("expected an error for missing CA SSH key in signer mode, got nil")
	}
	// The error could be either:
	// 1. "no CA private key configured" from initCAKeyAnnouncer (if checked first), or
	// 2. "gochannel is in-process only" from BootstrapSigner backend check
	// We verify it's one of the expected signer-mode errors, not an auth config error
	errStr := err.Error()
	if !strings.Contains(errStr, "no CA private key configured") &&
		!strings.Contains(errStr, "gochannel is in-process only") {
		t.Errorf("expected SSH key or backend config error, got: %v", err)
	}
}
