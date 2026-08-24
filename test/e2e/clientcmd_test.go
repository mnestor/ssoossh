//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// This file covers the client commands that report rather than change
// anything: ca, version, ssh inspect and ssh config. None of them had ever
// been run as a process by any test — the harness could start only
// `ssh login` and `ssh logout` — so between them they represent four of the
// eleven commands that shipped without end-to-end coverage.

// runClient is the shorthand this file uses: the real binary, the fixture's
// server, and the fixture's agent.
func runClient(t *testing.T, f *fixture, args ...string) harness.ClientResult {
	t.Helper()

	return harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: args,
		Env:  map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})
}

func TestCA_ShouldPrintTheServersCAPublicKeyWhenAsked(t *testing.T) {
	f := newFixture(t)

	res := runClient(t, f, "ca", "--server", f.Server.BaseURL)

	if res.ExitCode != 0 {
		t.Fatalf("expected ssoossh ca to succeed, got exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	// Parsed rather than string-compared: the point of this command is that
	// its output can be pasted into TrustedUserCAKeys, so "is it a public
	// key" is the property, not "does the text match".
	got, err := harness.ParseAuthorizedKey(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatalf("ssoossh ca did not print a parseable public key: %v\nstdout:\n%s", err, res.Stdout)
	}
	want, err := harness.ParseAuthorizedKey(f.Server.CAPublicKey)
	if err != nil {
		t.Fatalf("harness: failed to parse the test CA public key: %v", err)
	}
	if !harness.SameSSHKey(got, want) {
		t.Error("ssoossh ca printed a different key than the server's configured CA")
	}
}

// version declares itself offline (client/cmd/version.go), which means
// root's PreRun must skip building an API client and fetching the CA. An
// unreachable server is how that becomes observable: before the offline
// seam existed, `ssoossh version` opened a connection on every run purely
// as a side effect of shared init, so this is the regression guard for it.
func TestVersion_ShouldSucceedWhenTheServerIsUnreachable(t *testing.T) {
	_, ssoosshBin := harness.Binaries(t)

	res := harness.RunClient(t, ssoosshBin, harness.ClientOptions{
		// Port 1 is reserved and nothing listens on it, so any attempt to
		// reach the server fails fast rather than hanging the test.
		Args: []string{"version", "--server", "http://127.0.0.1:1"},
	})

	if res.ExitCode != 0 {
		t.Fatalf("expected ssoossh version to succeed with no reachable server, got exit %d\nstderr:\n%s",
			res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "ssoossh") {
		t.Errorf("expected version output to name the program, got %q", res.Stdout)
	}
	// The offline client refuses with a message naming the call it blocked.
	// Seeing one here would mean version reached for the network after all.
	if strings.Contains(res.Stderr, "must not contact the server") {
		t.Errorf("version attempted a server call despite declaring itself offline\nstderr:\n%s", res.Stderr)
	}
}

func TestInspect_ShouldReportTheCertificateJustIssued(t *testing.T) {
	f := newFixture(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)
	requestID := requestIDFromApprovalURL(t, approvalURL)

	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
	}

	res := runClient(t, f, "ssh", "inspect", "--server", f.Server.BaseURL)
	if res.ExitCode != 0 {
		t.Fatalf("expected ssh inspect to succeed, got exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	// Type is the assertion that matters most here. runInspect casts each
	// listed identity to *ssh.Certificate and prints "(not a certificate)"
	// on failure -- a branch its own comment calls unreachable short of a
	// backend bug, and the backend bug was real (docs/testing-needs.md).
	// Seeing "user" proves List(true) returned certificates, not bare keys.
	for _, want := range []string{"Principals", "alice", "Type", "user", "Serial"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("expected ssh inspect output to contain %q, got:\n%s", want, res.Stdout)
		}
	}
	if strings.Contains(res.Stdout, "not a certificate") {
		t.Errorf("ssh inspect listed a non-certificate identity:\n%s", res.Stdout)
	}
}

func TestInspect_ShouldReportNothingLoadedWhenTheAgentIsEmpty(t *testing.T) {
	f := newFixture(t)

	// A second agent, never logged into. The fixture's own agent is left
	// alone so this test says nothing about ordering with its neighbours.
	empty := harness.StartAgent(t)

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "inspect", "--server", f.Server.BaseURL},
		Env:  map[string]string{"SSH_AUTH_SOCK": empty.Socket},
	})

	// Exit 0, not an error: nothing loaded is an answer, not a failure.
	if res.ExitCode != 0 {
		t.Fatalf("expected ssh inspect to succeed against an empty agent, got exit %d\nstderr:\n%s",
			res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "No certificates") {
		t.Errorf("expected ssh inspect to say nothing is loaded, got:\n%s", res.Stdout)
	}
}

// `ssh config` is the wiring harness: the ssh_config recipes and nothing
// else. Resolved settings live in the --debug report alone, so that there
// is one answer to "what is in effect" instead of two that can drift.
func TestConfig_ShouldPrintTheSshConfigRecipes(t *testing.T) {
	f := newFixture(t)

	res := runClient(t, f, "ssh", "config")
	if res.ExitCode != 0 {
		t.Fatalf("expected ssh config to succeed, got exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	for _, want := range []string{"Match host", "exec \"ssoossh ssh login\"", "ProxyCommand ssoossh ssh proxycommand"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("expected ssh config output to contain %q, got:\n%s", want, res.Stdout)
		}
	}
	for _, unwanted := range []string{"TLS verification", "CA public key", "Key type"} {
		if strings.Contains(res.Stdout, unwanted) {
			t.Errorf("ssh config reported the setting %q; --debug is the only place settings are reported. Got:\n%s",
				unwanted, res.Stdout)
		}
	}
}

// The recipes have to be readable when there is no server configured at
// all, which is when someone is most likely to be reading them. `ssh config`
// is declared offline so root's PreRun never attempts the CA fetch; without
// that this exits non-zero with a network error instead of answering.
func TestConfig_ShouldPrintTheRecipesWithNoServerConfigured(t *testing.T) {
	f := newFixture(t)

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "config"},
		// A server that is syntactically fine and answers nothing: if the
		// command reaches out at all, this fails.
		UserYAML: "server: https://unreachable.example.invalid\n",
	})

	if res.ExitCode != 0 {
		t.Fatalf("ssh config must not need a reachable server, got exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "Match host") {
		t.Errorf("expected the ssh_config recipes, got:\n%s", res.Stdout)
	}
}

// The report is the resolved configuration, and this is the settings block
// someone is sent to when a login misbehaves.
func TestDebug_ShouldReportTheResolvedConfiguration(t *testing.T) {
	f := newFixture(t)

	res := runClient(t, f, "ca", "--server", f.Server.BaseURL, "--debug")
	if res.ExitCode != 0 {
		t.Fatalf("expected ca --debug to succeed, got exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	tests := []struct {
		name string
		want string
	}{
		// The server as resolved, which here came from the flag -- the
		// precedence tests in config_precedence_test.go vary where it comes
		// from and assert this same line.
		{name: "server", want: f.Server.BaseURL},
		{name: "tls verification", want: "TLS verification enabled"},
		// storageDescription reports where keys actually end up, which is a
		// runtime answer: with SSH_AUTH_SOCK pointing at a live agent it has
		// to say agent, whatever use_agent asked for.
		{name: "storage", want: "agent"},
		{name: "ca public key", want: "CA public key"},
	}

	// Whitespace is collapsed because the report aligns its columns with
	// %-22s, and asserting on the padding would make this a formatting test.
	got := strings.Join(strings.Fields(res.Stderr), " ")
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(got, tt.want) {
				t.Errorf("expected the debug report to contain %q, got:\n%s", tt.want, res.Stderr)
			}
		})
	}
}

// The CA summary is truncated for readability, and the truncation is the
// part worth pinning: it exists so two deployments can be compared at a
// glance, which stops working if it ever prints the whole blob or too
// little of it to tell two CAs apart.
func TestDebug_ShouldSummariseTheCAKeyRatherThanPrintingItWhole(t *testing.T) {
	f := newFixture(t)

	res := runClient(t, f, "ssh", "inspect", "--server", f.Server.BaseURL, "--debug")
	if res.ExitCode != 0 {
		t.Fatalf("expected ssh inspect --debug to succeed, got exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	fields := strings.Fields(f.Server.CAPublicKey)
	if len(fields) < 2 {
		t.Fatalf("harness: CA public key has no key material: %q", f.Server.CAPublicKey)
	}
	keyType, material := fields[0], fields[1]

	report := resolvedValue(t, res.Stderr, "CA public key")
	if !strings.Contains(report, keyType) {
		t.Errorf("expected the CA summary to name the key type %q, got %q", keyType, report)
	}
	if strings.Contains(report, material) {
		t.Error("the debug report printed the full CA key material; it is meant to be truncated")
	}
}
