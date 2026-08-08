package cmd

// Test methodology: Tests verify CLI command creation and flag configuration.
// Tests do not run in parallel (tests may modify environment). Uses table-
// driven tests where appropriate. Helper functions generate test data
// (config files, SSH keys).

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestNewCommand_ShouldSetUseToSsoosshd(t *testing.T) {
	t.Parallel()

	c := NewCommand()
	if c.Use != "ssoosshd" {
		t.Errorf("got Use %q, want %q", c.Use, "ssoosshd")
	}
}

func TestNewCommand_ShouldRegisterConfigFlag(t *testing.T) {
	t.Parallel()

	c := NewCommand()
	flag := c.PersistentFlags().Lookup("config")
	if flag == nil {
		t.Fatal("expected a --config persistent flag to be registered")
	}
	if flag.Shorthand != "c" {
		t.Errorf("got shorthand %q, want %q", flag.Shorthand, "c")
	}
	if flag.DefValue != "" {
		t.Errorf("got default value %q, want empty string", flag.DefValue)
	}
}

func TestNewCommand_ShouldReturnDistinctCommandInstances(t *testing.T) {
	t.Parallel()

	c1 := NewCommand()
	c2 := NewCommand()

	if c1 == c2 {
		t.Fatal("expected NewCommand to return a fresh instance each call")
	}
	// Setting a flag on one instance must not leak into the other, i.e.
	// each command owns its own independent flag set.
	if err := c1.PersistentFlags().Set("config", "/tmp/one.yaml"); err != nil {
		t.Fatalf("failed to set flag on c1: %v", err)
	}
	if got, err := c2.PersistentFlags().GetString("config"); err != nil || got != "" {
		t.Errorf("expected c2's config flag to be unaffected, got %q (err %v)", got, err)
	}
}

// writeServerTestConfig writes a minimal, valid ssoosshd config (port 0,
// in-memory SQLite, freshly generated CA key) and returns its path.
func writeServerTestConfig(t *testing.T) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test-key")
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	keyPEM := strings.TrimRight(string(pem.EncodeToMemory(block)), "\n")

	content := `http:
  address: 127.0.0.1
  port: 0
db:
  provider: sqlite
  connection_string: ":memory:"
ssh_key: |
  ` + strings.ReplaceAll(keyPEM, "\n", "\n  ") + "\n"

	path := filepath.Join(t.TempDir(), "ssoosshd.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}
	return path
}

// saveSlogDefault snapshots slog's default logger and restores it when the
// test finishes; running the server rewires it via logging.Setup and the
// OTel bootstrap. Tests using it must not run in parallel.
func saveSlogDefault(t *testing.T) {
	t.Helper()

	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
}

// TestNewCommand_RunShouldBootstrapServerUntilContextCanceled boots the
// real server through the command's Run function; the pre-canceled context
// makes it start up fully and immediately shut down cleanly. If Bootstrap
// fails, Run exits the whole test process with status 1 — that is the
// command's intended failure behavior, so a failure here is loud rather
// than graceful.
func TestNewCommand_RunShouldBootstrapServerUntilContextCanceled(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	c := NewCommand()
	c.SetArgs([]string{"-c", writeServerTestConfig(t)})

	if err := c.ExecuteContext(ctx); err != nil {
		t.Fatalf("expected the command to run and shut down cleanly, got %v", err)
	}
}

// TestExecute_ShouldShutDownOnInterruptSignal exercises Command.Execute,
// which installs the process signal handler. The handler can only be
// installed once per process, so this must be the only test in the package
// that calls Execute.
func TestExecute_ShouldShutDownOnInterruptSignal(t *testing.T) {
	saveSlogDefault(t)
	t.Setenv("OTEL_LOGS_EXPORTER", "none")

	c := NewCommand()
	c.SetArgs([]string{"-c", writeServerTestConfig(t)})

	go func() {
		// Give Execute time to install the signal handler and start the
		// server; the signal shuts it down gracefully whenever it lands.
		time.Sleep(500 * time.Millisecond)
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.Execute()
	}()

	select {
	case <-done:
		// Execute returned without exiting the process: success.
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for Execute to shut down after SIGTERM")
	}
}
