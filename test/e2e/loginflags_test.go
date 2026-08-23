//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// Across every suite, exactly two client flags ever reached a real binary:
// --server and --force. `ssh login`'s own flags -- the key algorithm and the
// five extension opt-outs -- had never been passed to the compiled client
// at all, so nothing proved that asking for ed25519 produced an ed25519 key
// or that --no-pty removed permit-pty from the certificate that came back.

// loginAndCollect runs an approved login with the given options and returns
// the certificate that ended up in the agent.
func loginAndCollect(t *testing.T, f *fixture, o harness.ClientOptions) *ssh.Certificate {
	t.Helper()

	o.Args = append([]string{"ssh", "login", "--server", f.Server.BaseURL}, o.Args...)
	if o.Env == nil {
		o.Env = map[string]string{}
	}
	o.Env["SSH_AUTH_SOCK"] = f.Agent.Socket

	login := harness.StartClient(t, f.SsoosshBin, o)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed: %v\nstderr:\n%s", err, login.Stderr())
	}

	certs := f.Agent.Certificates(t)
	if len(certs) != 1 {
		t.Fatalf("got %d certificates in the agent, want 1", len(certs))
	}
	return certs[0]
}

// The key type has to survive all the way to the issued certificate. A
// certificate's Key is the public key that was signed, so its type is what
// the client actually generated -- not what it said it would.
func TestLoginFlags_ShouldGenerateTheRequestedKeyType(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantType string
	}{
		{name: "ed25519", args: []string{"--key-type", "ed25519"}, wantType: ssh.KeyAlgoED25519},
		{name: "ecdsa 256", args: []string{"--key-type", "ecdsa", "--key-size", "256"}, wantType: ssh.KeyAlgoECDSA256},
		{name: "ecdsa 384", args: []string{"--key-type", "ecdsa", "--key-size", "384"}, wantType: ssh.KeyAlgoECDSA384},
		{name: "ecdsa 521", args: []string{"--key-type", "ecdsa", "--key-size", "521"}, wantType: ssh.KeyAlgoECDSA521},
		{name: "rsa 2048", args: []string{"--key-type", "rsa", "--key-size", "2048"}, wantType: ssh.KeyAlgoRSA},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A fixture each, because the agent accumulates certificates and
			// loginAndCollect expects exactly one.
			f := newFixture(t)

			cert := loginAndCollect(t, f, harness.ClientOptions{Args: tt.args})

			if got := cert.Key.Type(); got != tt.wantType {
				t.Errorf("got key type %q, want %q", got, tt.wantType)
			}
		})
	}
}

// The same setting from a config file rather than a flag, since that is the
// route a Match exec line uses -- there is no command line to add a flag to.
func TestLoginFlags_ShouldGenerateTheKeyTypeNamedInConfig(t *testing.T) {
	f := newFixture(t)

	cert := loginAndCollect(t, f, harness.ClientOptions{
		UserYAML: "sshkey:\n  type: ed25519\n",
	})

	if got := cert.Key.Type(); got != ssh.KeyAlgoED25519 {
		t.Errorf("got key type %q, want %q", got, ssh.KeyAlgoED25519)
	}
}

// A flag beats the config file for the key type too, which is the same
// precedence every other setting has but had never been checked for one
// that changes what gets generated rather than what gets printed.
func TestLoginFlags_ShouldLetTheKeyTypeFlagBeatConfig(t *testing.T) {
	f := newFixture(t)

	cert := loginAndCollect(t, f, harness.ClientOptions{
		Args:     []string{"--key-type", "ed25519"},
		UserYAML: "sshkey:\n  type: rsa\n  size: 2048\n",
	})

	if got := cert.Key.Type(); got != ssh.KeyAlgoED25519 {
		t.Errorf("got key type %q, want the flag's %q", got, ssh.KeyAlgoED25519)
	}
}

// Each opt-out flag has to remove exactly its own extension from the
// certificate that comes back, and leave the others alone. The server
// narrows the request against its own configuration before issuing, so this
// is also the first end-to-end proof that a client-side opt-out survives
// that narrowing rather than being reinstated by it.
func TestLoginFlags_ShouldRemoveOnlyTheOptedOutExtension(t *testing.T) {
	tests := []struct {
		name string
		flag string
		ext  string
	}{
		{name: "no-pty", flag: "--no-pty", ext: "permit-pty"},
		{name: "no-agent-forwarding", flag: "--no-agent-forwarding", ext: "permit-agent-forwarding"},
		{name: "no-port-forwarding", flag: "--no-port-forwarding", ext: "permit-port-forwarding"},
		{name: "no-x11-forwarding", flag: "--no-x11-forwarding", ext: "permit-X11-forwarding"},
		{name: "no-user-rc", flag: "--no-user-rc", ext: "permit-user-rc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)

			cert := loginAndCollect(t, f, harness.ClientOptions{Args: []string{tt.flag}})

			if _, present := cert.Extensions[tt.ext]; present {
				t.Errorf("%s did not remove %s from the certificate: %v", tt.flag, tt.ext, cert.Extensions)
			}
			if len(cert.Extensions) == 0 {
				t.Errorf("%s removed every extension, not just %s", tt.flag, tt.ext)
			}
		})
	}
}

// And the same opt-out through configuration.
func TestLoginFlags_ShouldRemoveTheExtensionOptedOutInConfig(t *testing.T) {
	f := newFixture(t)

	cert := loginAndCollect(t, f, harness.ClientOptions{
		UserYAML: "certificate_extensions:\n  no_pty: true\n",
	})

	if _, present := cert.Extensions["permit-pty"]; present {
		t.Errorf("config no_pty did not remove permit-pty: %v", cert.Extensions)
	}
}

// Opting everything out cannot produce a usable certificate, so the client
// refuses before asking. The wording has to name the layer the reader can
// change: someone who typed five flags should not be sent to read a config
// file.
//
// The flag wording was unreachable before this branch -- removed_flag was
// declared, switched on twice, and never assigned, because flags reach
// effectiveExtensions through viper as ordinary config values.
func TestLoginFlags_ShouldRefuseWhenEveryExtensionIsOptedOut(t *testing.T) {
	f := newFixture(t)

	allFlags := []string{"--no-pty", "--no-agent-forwarding", "--no-port-forwarding", "--no-x11-forwarding", "--no-user-rc"}
	allConfig := "certificate_extensions:\n" +
		"  no_pty: true\n" +
		"  no_agent_forwarding: true\n" +
		"  no_port_forwarding: true\n" +
		"  no_x11_forwarding: true\n" +
		"  no_user_rc: true\n"

	tests := []struct {
		name    string
		opts    harness.ClientOptions
		wantMsg string
	}{
		{
			name:    "via flags",
			opts:    harness.ClientOptions{Args: allFlags},
			wantMsg: "command-line flags",
		},
		{
			name:    "via config",
			opts:    harness.ClientOptions{UserYAML: allConfig},
			wantMsg: "via configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.opts
			opts.Args = append([]string{"ssh", "login", "--server", f.Server.BaseURL}, opts.Args...)
			opts.Env = map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket}

			res := harness.RunClient(t, f.SsoosshBin, opts)

			if res.ExitCode == 0 {
				t.Fatalf("expected a non-zero exit, got 0\nstdout:\n%s", res.Stdout)
			}
			if !strings.Contains(res.Stderr, tt.wantMsg) {
				t.Errorf("expected the refusal to say %q, got:\n%s", tt.wantMsg, res.Stderr)
			}
		})
	}
}

// The summary line names which layer removed each extension, and has to name
// the flag when a flag did it. Reporting "config" for something typed on the
// command line sends the reader to the wrong file.
func TestLoginFlags_ShouldAttributeARemovalToTheFlagThatCausedIt(t *testing.T) {
	f := newFixture(t)

	login := harness.StartClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "login", "--server", f.Server.BaseURL, "--no-pty"},
		Env:  map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)
	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed: %v\nstderr:\n%s", err, login.Stderr())
	}

	if !strings.Contains(login.Stderr(), "permit-pty(flag)") {
		t.Errorf("expected the removal to be attributed to the flag, got:\n%s", login.Stderr())
	}
}

// -v steps up what the client explains about itself, and it all belongs on
// stderr: stdout carries certificates, relayed connection data, and the
// principal list sshd parses, none of which tolerate commentary.
func TestLoginFlags_ShouldTraceToStderrWhenVerbose(t *testing.T) {
	f := newFixture(t)

	quiet := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "config", "--server", f.Server.BaseURL},
		Env:  map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})
	verbose := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "config", "--server", f.Server.BaseURL, "-vv"},
		Env:  map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})

	if quiet.ExitCode != 0 || verbose.ExitCode != 0 {
		t.Fatalf("expected both runs to succeed, got %d and %d", quiet.ExitCode, verbose.ExitCode)
	}
	if len(verbose.Stderr) <= len(quiet.Stderr) {
		t.Errorf("expected -vv to produce more stderr than the default\nquiet:\n%s\nverbose:\n%s",
			quiet.Stderr, verbose.Stderr)
	}
	// The report itself must be unchanged: verbosity is commentary, not data.
	if verbose.Stdout != quiet.Stdout {
		t.Errorf("-vv changed stdout\nquiet:\n%s\nverbose:\n%s", quiet.Stdout, verbose.Stdout)
	}
}

// $SSOOSSH_VERBOSE is the route for invocations whose command line is not
// yours to edit, which is most of the ones that matter: an ssh_config Match
// exec line, a cron entry, a systemd unit.
func TestLoginFlags_ShouldTraceWhenTheVerboseEnvironmentVariableIsSet(t *testing.T) {
	f := newFixture(t)

	quiet := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "config", "--server", f.Server.BaseURL},
		Env:  map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})
	verbose := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "config", "--server", f.Server.BaseURL},
		Env: map[string]string{
			"SSH_AUTH_SOCK":   f.Agent.Socket,
			"SSOOSSH_VERBOSE": "2",
		},
	})

	if verbose.ExitCode != 0 {
		t.Fatalf("expected the verbose run to succeed, got exit %d\nstderr:\n%s", verbose.ExitCode, verbose.Stderr)
	}
	if len(verbose.Stderr) <= len(quiet.Stderr) {
		t.Errorf("expected SSOOSSH_VERBOSE=2 to produce more stderr\nquiet:\n%s\nverbose:\n%s",
			quiet.Stderr, verbose.Stderr)
	}
}
