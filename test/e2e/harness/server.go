//go:build e2e || resilience || load

package harness

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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
	// Args are inserted before --config on the ssoosshd command line, so a
	// test can select a startup mode ("serve", "api") - see split_test.go.
	Args []string
	// ExtraConfigYAML is appended verbatim to the rendered config. Used to
	// add top-level sections the template does not know about (pubsub for
	// the split-mode test). Not usable for cert_options.pam: that lives
	// under the cert_options key the template already renders, and a
	// duplicate top-level key is a YAML error — hence PAMRequireGroup below.
	ExtraConfigYAML string
	// PAMRequireGroup sets cert_options.pam.require_group, which fails
	// closed: unset (the default) means the server issues no PAM
	// certificates at all (see CertOptionsPAM.RequireGroup).
	PAMRequireGroup string
	// DSN points this server at a specific postgres database, so a test can
	// deliberately share one database across servers (multi-signer HA).
	// Empty means automatic: a private database per server when
	// SSOOSSH_E2E_POSTGRES_DSN is set, in-memory sqlite otherwise.
	DSN string
	// UserKeyIDTemplate sets cert_options.user.key_id_template. Empty keeps
	// the product default ({{.Username}}).
	UserKeyIDTemplate string
	// ExtraClaimFields sets authentication.fields.extra (template field
	// name -> claim name), for key ID template tests.
	ExtraClaimFields map[string]string

	// ServiceAccountsField sets authentication.fields.service_accounts, the
	// claim naming which service accounts an identity may approve service
	// certificates for. Empty leaves the key unset, which is the product
	// default and means no identity can approve a service enrollment --
	// so a service test has to set it.
	//
	// Rendered inside the fields block rather than appended through
	// ExtraConfigYAML: appending would need a second top-level
	// "authentication:" key, and the later one wins, silently discarding
	// client_id and provider_url.
	ServiceAccountsField string
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

	// ConfigPath is the rendered config file, shared with the signer
	// process in split-mode tests - both halves must agree on the broker
	// and the CA key.
	ConfigPath string

	cmd            *exec.Cmd
	stdout, stderr *lockedBuffer
	name           string
}

// The OAuth credentials every harness-rendered config carries; exposed on
// Server for tests that drive the IdP directly.
const (
	harnessClientID     = "e2e-test-client"
	harnessClientSecret = "e2e-test-secret"
)

// NewSignerConfig renders exactly the config StartServer would — fresh CA
// key, fresh (unused) port, same defaults and DSN resolution — and writes it
// without starting a process. For tests that run `ssoosshd sign` as a
// standalone signer with its own CA key (multi-signer split mode). Returns
// the config path and the CA public key in authorized_keys format.
func NewSignerConfig(t *testing.T, idp *IdentityProvider, opts ServerOptions) (configPath, caPublicKey string) {
	t.Helper()

	configPath, _, caPublicKey = newServerConfig(t, idp, opts)
	return configPath, caPublicKey
}

// newServerConfig applies ServerOptions defaults, resolves the database,
// generates a CA key, renders the YAML, and writes it to a temp file. Both
// StartServer and NewSignerConfig build on it.
func newServerConfig(t *testing.T, idp *IdentityProvider, opts ServerOptions) (configPath, baseURL, caPublicKey string) {
	t.Helper()

	if opts.ValidDuration == "" {
		opts.ValidDuration = "8h"
	}
	if len(opts.Extensions) == 0 {
		opts.Extensions = []string{"permit-pty", "permit-agent-forwarding"}
	}

	// Automatic database resolution: a private postgres database per server
	// when the environment advertises an instance, in-memory sqlite
	// otherwise. opts.DSN overrides both, for tests that deliberately share
	// one database across servers.
	dsn := opts.DSN
	if dsn == "" && os.Getenv("SSOOSSH_E2E_POSTGRES_DSN") != "" {
		dsn = NewPostgresDatabase(t)
	}

	port := freePort(t)
	baseURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	sshKeyPEM, caPublicKey := generateCAKey(t)

	configYAML := renderServerConfig(serverConfigData{
		PublicURL:            baseURL,
		ServerName:           "127.0.0.1",
		Address:              "127.0.0.1",
		Port:                 port,
		ClientID:             harnessClientID,
		ClientSecret:         harnessClientSecret,
		ProviderURL:          idp.URL(),
		SSHKeyPEM:            sshKeyPEM,
		ValidDuration:        opts.ValidDuration,
		Extensions:           opts.Extensions,
		PAMRequireGroup:      opts.PAMRequireGroup,
		DSN:                  dsn,
		UserKeyIDTemplate:    opts.UserKeyIDTemplate,
		ExtraClaimFields:     opts.ExtraClaimFields,
		ServiceAccountsField: opts.ServiceAccountsField,
	}) + opts.ExtraConfigYAML

	configPath = filepath.Join(t.TempDir(), "ssoosshd.yaml")
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("harness: failed to write server config: %v", err)
	}

	return configPath, baseURL, caPublicKey
}

// StartServer renders a config pointing at idp, starts ssoosshd against it,
// waits for /healthz, and registers teardown via t.Cleanup.
func StartServer(t *testing.T, idp *IdentityProvider, opts ServerOptions) *Server {
	t.Helper()

	configPath, baseURL, caPublicKey := newServerConfig(t, idp, opts)

	ssoosshdPath, _ := Binaries(t)

	args := append(append([]string{}, opts.Args...), "--config", configPath)
	cmd := exec.Command(ssoosshdPath, args...)
	var stdout, stderr lockedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("harness: failed to start ssoosshd: %v", err)
	}

	srv := &Server{
		ConfigPath:  configPath,
		BaseURL:     baseURL,
		CAPublicKey: caPublicKey,
		ClientID:    harnessClientID,
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
		// Keyed by port: a multi-instance test runs several servers under
		// one test name, and fixed filenames meant the last one to shut
		// down silently overwrote every other instance's diagnostics --
		// which is precisely the log you need when the instances disagree.
		port := s.BaseURL[strings.LastIndex(s.BaseURL, ":")+1:]
		writeArtifact(t, "ssoosshd-"+port+"-stdout.log", s.stdout.Bytes())
		writeArtifact(t, "ssoosshd-"+port+"-stderr.log", s.stderr.Bytes())
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
// reuse race, deliberately accepted (docs/dev/e2e-testing-plan.md, "Ports").
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
	PublicURL            string
	ServerName           string
	Address              string
	Port                 int
	ClientID             string
	ClientSecret         string
	ProviderURL          string
	SSHKeyPEM            string
	ValidDuration        string
	Extensions           []string
	PAMRequireGroup      string
	DSN                  string
	UserKeyIDTemplate    string
	ExtraClaimFields     map[string]string
	ServiceAccountsField string
}

// renderServerConfig builds the ssoosshd config YAML from d directly (not
// from a text/template over hand-typed prose) — the exact obstacle
// docs/dev/e2e-testing-plan.md calls out: "authentication:" must be top-level,
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
	if d.ServiceAccountsField != "" {
		fmt.Fprintf(&b, "    service_accounts: %q\n", d.ServiceAccountsField)
	}
	if len(d.ExtraClaimFields) > 0 {
		fmt.Fprintf(&b, "    extra:\n")
		// Sorted so the rendered config is deterministic across runs.
		names := make([]string, 0, len(d.ExtraClaimFields))
		for name := range d.ExtraClaimFields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(&b, "      %s: %q\n", name, d.ExtraClaimFields[name])
		}
	}

	// d.DSN empty means throwaway in-memory sqlite. Non-empty it is a
	// postgres database this server owns outright: newServerConfig
	// provisions a private one per server via NewPostgresDatabase (so runs
	// with SSOOSSH_E2E_POSTGRES_DSN exercise the same flows on real dialect
	// semantics with sqlite-equivalent isolation), or the test passed
	// ServerOptions.DSN to deliberately share one database across servers.
	fmt.Fprintf(&b, "db:\n")
	if d.DSN != "" {
		fmt.Fprintf(&b, "  provider: postgres\n")
		fmt.Fprintf(&b, "  connection_string: %q\n", d.DSN)
	} else {
		fmt.Fprintf(&b, "  provider: sqlite\n")
		fmt.Fprintf(&b, "  connection_string: \":memory:\"\n")
	}

	fmt.Fprintf(&b, "production: false\n")
	fmt.Fprintf(&b, "logging:\n")
	fmt.Fprintf(&b, "  level: DEBUG\n")
	// Stdout too, or process logs land in a timberjack temp file where
	// neither artifact capture nor the signer-readiness poll can see them.
	fmt.Fprintf(&b, "  enable_stdout: true\n")

	fmt.Fprintf(&b, "ssh_key: |\n")
	for _, line := range strings.Split(strings.TrimRight(d.SSHKeyPEM, "\n"), "\n") {
		fmt.Fprintf(&b, "  %s\n", line)
	}

	fmt.Fprintf(&b, "cert_options:\n")
	fmt.Fprintf(&b, "  user:\n")
	fmt.Fprintf(&b, "    valid_duration: %s\n", d.ValidDuration)
	if d.UserKeyIDTemplate != "" {
		fmt.Fprintf(&b, "    key_id_template: %q\n", d.UserKeyIDTemplate)
	}
	fmt.Fprintf(&b, "    extensions:\n")
	for _, ext := range d.Extensions {
		fmt.Fprintf(&b, "      - %s\n", ext)
	}
	// The pam block's valid_duration and extensions keep the product
	// defaults (30s, none) — appropriate for a certificate validated once
	// in-process and discarded.
	if d.PAMRequireGroup != "" {
		fmt.Fprintf(&b, "  pam:\n")
		fmt.Fprintf(&b, "    require_group: %q\n", d.PAMRequireGroup)
	}

	return b.String()
}
