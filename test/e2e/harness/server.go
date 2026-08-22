//go:build e2e

package harness

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// ServerOptions configures the ssoosshd instance StartServer launches. Zero
// values pick harness-appropriate defaults, not the product's own defaults —
// notably a much longer certificate lifetime than the shipped 30s, which
// would make the reuse assertion flaky and the ssh assertion a race.
type ServerOptions struct {
	// ValidDuration overrides the user certificate lifetime. Defaults to 8h.
	ValidDuration string
	// Extensions overrides the user certificate's permitted extensions.
	// Defaults to permit-pty and permit-agent-forwarding.
	Extensions []string
}

// Server is a running ssoosshd subprocess.
type Server struct {
	// BaseURL is the origin ssoosshd is reachable at, also its
	// http.public_url.
	BaseURL string
	// CAPublicKey is the CA's public key in authorized_keys format, for
	// sshd's TrustedUserCAKeys (tier 3).
	CAPublicKey string
	// ClientID/ClientSecret are the OAuth credentials this instance is
	// configured with, for tests that need to drive the IdP directly.
	ClientID string

	cmd            *exec.Cmd
	stdout, stderr *bytes.Buffer
	name           string
}

// StartServer renders a config pointing at idp, starts ssoosshd against it,
// waits for /healthz, and registers teardown via t.Cleanup.
func StartServer(t *testing.T, idp *IdentityProvider, opts ServerOptions) *Server {
	t.Helper()

	if opts.ValidDuration == "" {
		opts.ValidDuration = "8h"
	}
	if len(opts.Extensions) == 0 {
		opts.Extensions = []string{"permit-pty", "permit-agent-forwarding"}
	}

	port := freePort(t)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	sshKeyPEM, caPublicKey := generateCAKey(t)

	const clientID = "e2e-test-client"
	const clientSecret = "e2e-test-secret"

	configYAML := renderServerConfig(serverConfigData{
		PublicURL:     baseURL,
		ServerName:    "127.0.0.1",
		Address:       "127.0.0.1",
		Port:          port,
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		ProviderURL:   idp.URL(),
		SSHKeyPEM:     sshKeyPEM,
		ValidDuration: opts.ValidDuration,
		Extensions:    opts.Extensions,
	})

	dir := t.TempDir()
	configPath := filepath.Join(dir, "ssoosshd.yaml")
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("harness: failed to write server config: %v", err)
	}

	ssoosshdPath, _ := Binaries(t)

	cmd := exec.Command(ssoosshdPath, "--config", configPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("harness: failed to start ssoosshd: %v", err)
	}

	srv := &Server{
		BaseURL:     baseURL,
		CAPublicKey: caPublicKey,
		ClientID:    clientID,
		cmd:         cmd,
		stdout:      &stdout,
		stderr:      &stderr,
		name:        t.Name(),
	}

	t.Cleanup(func() { srv.shutdown(t) })

	srv.waitHealthy(t)

	return srv
}

// waitHealthy polls /healthz until it answers or a deadline passes. Startup
// runs migrations, OIDC discovery against the harness IdP, and pub/sub
// bootstrap, so how long this takes isn't knowable in advance — hence
// polling rather than a fixed sleep.
func (s *Server) waitHealthy(t *testing.T) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		if s.cmd.ProcessState != nil {
			t.Fatalf("harness: ssoosshd exited before becoming healthy (%v)\nstderr:\n%s", s.cmd.ProcessState, s.stderr.String())
		}
		resp, err := http.Get(s.BaseURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			lastErr = fmt.Errorf("unexpected status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("harness: ssoosshd did not become healthy before deadline: %v\nstderr:\n%s", lastErr, s.stderr.String())
}

// shutdown sends SIGTERM (matching production shutdown, which cancels the
// bootstrap context on that signal), falling back to Kill if it doesn't
// exit in time. On test failure it also writes captured logs to the
// artifacts directory.
func (s *Server) shutdown(t *testing.T) {
	t.Helper()

	if t.Failed() {
		writeArtifact(t, "ssoosshd-stdout.log", s.stdout.Bytes())
		writeArtifact(t, "ssoosshd-stderr.log", s.stderr.Bytes())
	}

	if s.cmd.Process == nil {
		return
	}

	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()

	_ = s.cmd.Process.Signal(syscall.SIGTERM) //nolint:errcheck // best-effort graceful shutdown; the Kill fallback below covers failure

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = s.cmd.Process.Kill() //nolint:errcheck // best-effort teardown
		<-done
	}
}

// freePort binds an ephemeral TCP port, reads it, and closes the listener
// immediately so ssoosshd can bind it instead. This has a theoretical
// reuse race, deliberately accepted (docs/e2e-testing-plan.md, "Ports").
func freePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("harness: failed to allocate a port: %v", err)
	}
	defer ln.Close()

	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("harness: expected a *net.TCPAddr from a tcp listener, got %T", ln.Addr())
	}
	return addr.Port
}

// generateCAKey returns a fresh ed25519 CA keypair: the OpenSSH-PEM private
// key for ssh_key, and the public key in authorized_keys format for
// TrustedUserCAKeys.
func generateCAKey(t *testing.T) (privatePEM, publicAuthorizedKey string) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("harness: failed to generate CA key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "e2e-test-ca")
	if err != nil {
		t.Fatalf("harness: failed to marshal CA private key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("harness: failed to derive CA public key: %v", err)
	}

	return string(pem.EncodeToMemory(block)), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
}

type serverConfigData struct {
	PublicURL     string
	ServerName    string
	Address       string
	Port          int
	ClientID      string
	ClientSecret  string
	ProviderURL   string
	SSHKeyPEM     string
	ValidDuration string
	Extensions    []string
}

// renderServerConfig builds the ssoosshd config YAML from d directly (not
// from a text/template over hand-typed prose) — the exact obstacle
// docs/e2e-testing-plan.md calls out: "authentication:" must be top-level,
// not nested under "http:", which the project's own sample once got wrong.
func renderServerConfig(d serverConfigData) string {
	var b strings.Builder

	fmt.Fprintf(&b, "http:\n")
	fmt.Fprintf(&b, "  public_url: %q\n", d.PublicURL)
	fmt.Fprintf(&b, "  server_name: %q\n", d.ServerName)
	fmt.Fprintf(&b, "  address: %q\n", d.Address)
	fmt.Fprintf(&b, "  port: %d\n", d.Port)
	fmt.Fprintf(&b, "  cookie_secure: false\n")

	fmt.Fprintf(&b, "authentication:\n")
	fmt.Fprintf(&b, "  client_id: %q\n", d.ClientID)
	fmt.Fprintf(&b, "  client_secret: %q\n", d.ClientSecret)
	fmt.Fprintf(&b, "  provider_url: %q\n", d.ProviderURL)
	fmt.Fprintf(&b, "  scopes: \"profile email\"\n")
	fmt.Fprintf(&b, "  fields:\n")
	fmt.Fprintf(&b, "    username: \"preferred_username\"\n")
	fmt.Fprintf(&b, "    groups: \"groups\"\n")
	fmt.Fprintf(&b, "    email: \"email\"\n")

	// Default backend is throwaway in-memory sqlite. With
	// SSOOSSH_E2E_POSTGRES_DSN set, the whole suite runs the server against
	// that live Postgres instead — the same flows on real dialect
	// semantics. The instance must be disposable: each server start
	// migrates into whatever schema the DSN points at, and tests assume
	// they own it.
	fmt.Fprintf(&b, "db:\n")
	if dsn := os.Getenv("SSOOSSH_E2E_POSTGRES_DSN"); dsn != "" {
		fmt.Fprintf(&b, "  provider: postgres\n")
		fmt.Fprintf(&b, "  connection_string: %q\n", dsn)
	} else {
		fmt.Fprintf(&b, "  provider: sqlite\n")
		fmt.Fprintf(&b, "  connection_string: \":memory:\"\n")
	}

	fmt.Fprintf(&b, "production: false\n")
	fmt.Fprintf(&b, "logging:\n")
	fmt.Fprintf(&b, "  level: DEBUG\n")

	fmt.Fprintf(&b, "ssh_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(d.SSHKeyPEM, "\n"), "\n") {
		fmt.Fprintf(&b, "  %s\n", line)
	}

	fmt.Fprintf(&b, "cert_options:\n")
	fmt.Fprintf(&b, "  user:\n")
	fmt.Fprintf(&b, "    valid_duration: %s\n", d.ValidDuration)
	fmt.Fprintf(&b, "    extensions:\n")
	for _, ext := range d.Extensions {
		fmt.Fprintf(&b, "      - %s\n", ext)
	}

	return b.String()
}
