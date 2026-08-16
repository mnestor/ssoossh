package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
)

// newProxyExec builds the command tree around a specific API client and
// agent, so a test can choose what obtaining a certificate does.
//
// Note what is *not* tested here: the success path. `proxycommand` ends in
// syscall.Exec, which would replace this test binary with whatever it was
// asked to run. Every case below therefore fails before the hand-off, which
// is also the behavior worth guarding — handing a connection to ssh without
// a certificate is the failure that matters.
func newProxyExec(t *testing.T, client api.Client, ag agent.Agent) *simplecobra.Exec {
	t.Helper()

	root := &RootCommand{
		newConfig:    func(cmd *cobra.Command) (*config.Config, error) { return &config.Config{UseAgent: true}, nil },
		newAPIClient: func(cfg *config.Config) (api.Client, error) { return client, nil },
		newSSHAgent:  func() (agent.Agent, error) { return ag, nil },
		newFileAgent: func(path string) (agent.Agent, error) { return ag, nil },
	}
	root.commands = []simplecobra.Commander{newSSHCommand()}

	x, err := simplecobra.New(root)
	if err != nil {
		t.Fatalf("failed to build command tree: %v", err)
	}
	return x
}

// TestProxyCommand_ShouldNotExecWhenNoCertificateCanBeObtained is the reason
// this command wraps the relay at all. If it handed off regardless, it would
// be indistinguishable from configuring the relay directly in ssh_config.
func TestProxyCommand_ShouldNotExecWhenNoCertificateCanBeObtained(t *testing.T) {
	client := &fakeAPIClient{result: &api.CertificateResult{Status: api.StatusDenied}}
	x := newProxyExec(t, client, &stubAgent{})

	// /bin/true would exec successfully if the guard were missing, ending
	// this test process rather than failing the assertion.
	_, err := x.Execute(context.Background(), []string{"ssh", "proxycommand", "/bin/true"})
	if err == nil {
		t.Fatal("expected a denied request to stop the hand-off")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("got %q, want the denial surfaced", err)
	}
}

func TestProxyCommand_ShouldRequireACommandToExec(t *testing.T) {
	x := newProxyExec(t, &fakeAPIClient{}, &stubAgent{})

	_, err := x.Execute(context.Background(), []string{"ssh", "proxycommand"})
	if err == nil || err.Error() != errProxyCommandRequiresArgs.Error() {
		t.Fatalf("got %v, want %v", err, errProxyCommandRequiresArgs)
	}
}

// TestProxyCommand_ShouldRefuseFileBasedKeys covers a real limitation rather
// than a policy choice: ssh reads key files once at startup, so a
// certificate written afterwards is never seen.
func TestProxyCommand_ShouldRefuseFileBasedKeys(t *testing.T) {
	x := newProxyExec(t, &fakeAPIClient{}, &stubAgent{agentType: agent.AgentTypeFile})

	_, err := x.Execute(context.Background(), []string{"ssh", "proxycommand", "/bin/true"})
	if err == nil {
		t.Fatal("expected file-based keys to be refused")
	}
	if !strings.Contains(err.Error(), "file based ssh keys") {
		t.Errorf("got %q, want the file-agent limitation explained", err)
	}
}

// TestProxyCommand_ShouldNotClaimFlagsMeantForTheRelayedCommand guards the
// hand-off contract: everything after "proxycommand" belongs to the command
// being run, so cobra must not try to parse any of it.
func TestProxyCommand_ShouldNotClaimFlagsMeantForTheRelayedCommand(t *testing.T) {
	client := &fakeAPIClient{result: &api.CertificateResult{Status: api.StatusDenied}}
	x := newProxyExec(t, client, &stubAgent{})

	// -X is nc's flag. If cobra parsed it, this would fail as an unknown
	// shorthand instead of reaching the certificate check.
	_, err := x.Execute(context.Background(), []string{
		"ssh", "proxycommand", "/usr/bin/nc", "-X", "connect", "-x", "192.0.2.0:8080", "host", "22",
	})
	if err == nil {
		t.Fatal("expected the denied request to stop the hand-off")
	}
	if strings.Contains(err.Error(), "unknown shorthand") || strings.Contains(err.Error(), "unknown flag") {
		t.Errorf("cobra parsed a flag meant for the relayed command: %v", err)
	}
}

// TestProxyCommand_ShouldHandOverTheCompleteArgumentVector is the regression
// test for a bug that made this command unusable: argv was passed as
// args[1:], so the relay read its first real argument as its own program
// name and saw one fewer argument than it was given. With
// `socat - TCP:host:port` that surfaced as socat reporting "exactly 2
// addresses required (there are 1)" — a message pointing nowhere near the
// cause.
func TestProxyCommand_ShouldHandOverTheCompleteArgumentVector(t *testing.T) {
	ours := newTestCA(t)
	valid := newTestCert(t, ours, "alice", time.Hour)
	ag := &stubAgent{identities: []xssh.PublicKey{valid}, cas: []xssh.PublicKey{ours.public}}

	var gotPath string
	var gotArgv []string
	original := execCommand
	execCommand = func(path string, argv []string, _ []string) error {
		gotPath, gotArgv = path, argv
		return nil
	}
	t.Cleanup(func() { execCommand = original })

	x := newProxyExec(t, &fakeAPIClient{}, ag)
	if _, err := x.Execute(context.Background(), []string{
		"ssh", "proxycommand", "/usr/bin/socat", "-", "TCP:db01.internal:22",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != "/usr/bin/socat" {
		t.Errorf("got path %q, want the relay binary", gotPath)
	}
	want := []string{"/usr/bin/socat", "-", "TCP:db01.internal:22"}
	if len(gotArgv) != len(want) {
		t.Fatalf("got argv %q, want %q — argv must start with the program name", gotArgv, want)
	}
	for i := range want {
		if gotArgv[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, gotArgv[i], want[i])
		}
	}
}

// TestProxyCommand_ShouldReportAFailedHandOff covers the other half of the
// same confusion: ssh renders this as a bare "Connection closed", so the
// error has to name the command that could not be run.
func TestProxyCommand_ShouldReportAFailedHandOff(t *testing.T) {
	ours := newTestCA(t)
	valid := newTestCert(t, ours, "alice", time.Hour)
	ag := &stubAgent{identities: []xssh.PublicKey{valid}, cas: []xssh.PublicKey{ours.public}}

	original := execCommand
	execCommand = func(string, []string, []string) error { return errors.New("no such file or directory") }
	t.Cleanup(func() { execCommand = original })

	x := newProxyExec(t, &fakeAPIClient{}, ag)
	_, err := x.Execute(context.Background(), []string{"ssh", "proxycommand", "/usr/bin/nc", "host", "22"})
	if err == nil {
		t.Fatal("expected a failed hand-off to be reported")
	}
	if !strings.Contains(err.Error(), "/usr/bin/nc") {
		t.Errorf("got %q, want the command that failed to be named", err)
	}
}
