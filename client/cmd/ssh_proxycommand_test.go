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
	if !errors.Is(err, errProxyCommandNeedsAgent) {
		t.Errorf("got %v, want %v", err, errProxyCommandNeedsAgent)
	}
}

// A missing relay command is the more basic mistake, so it is reported
// first even when no agent is reachable either.
func TestProxyCommand_ShouldReportAMissingCommandBeforeAMissingAgent(t *testing.T) {
	x := newProxyExec(t, &fakeAPIClient{}, &stubAgent{agentType: agent.AgentTypeFile})

	_, err := x.Execute(context.Background(), []string{"ssh", "proxycommand"})
	if !errors.Is(err, errProxyCommandRequiresArgs) {
		t.Errorf("got %v, want %v", err, errProxyCommandRequiresArgs)
	}
}

// TestWarnMisplacedClientFlags covers the trap that sent test traffic to the
// wrong server: an ssoossh flag after the relay command goes to the relay.
func TestWarnMisplacedClientFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "should warn about --server after the relay command",
			args: []string{"/usr/bin/nc", "%h", "%p", "--server", "http://127.0.0.1:9"},
			want: "Warning: --server comes after the relay command, so /usr/bin/nc receives it, not ssoossh; put ssoossh flags before \"/usr/bin/nc\"\n",
		},
		{
			name: "should warn about --config=value after the relay command",
			args: []string{"/usr/bin/nc", "--config=/tmp/c.yaml"},
			want: "Warning: --config comes after the relay command, so /usr/bin/nc receives it, not ssoossh; put ssoossh flags before \"/usr/bin/nc\"\n",
		},
		{
			name: "should not warn about relay flags that ssoossh also has",
			args: []string{"/usr/bin/nc", "-v", "-c", "host", "22"},
			want: "",
		},
		{
			name: "should not warn about the relay command itself",
			args: []string{"--server"},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			warnMisplacedClientFlags(&out, tt.args)
			if out.String() != tt.want {
				t.Errorf("got %q, want %q", out.String(), tt.want)
			}
		})
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

// The recipe in the help must be one a user can paste: the subcommand
// spelled right and the binary named ssoossh, not whatever built the docs.
func TestProxyCommandHelp_ShouldShowAWorkingRecipe(t *testing.T) {
	root, err := CobraCommandForManpage()
	if err != nil {
		t.Fatalf("build tree: %v", err)
	}
	cmd := findCommand(root, "ssh", "proxycommand")

	want := "After:  ProxyCommand ssoossh ssh proxycommand /usr/bin/nc -X connect -x 192.0.2.0:8080 %h %p"
	if !strings.Contains(cmd.Long, want) {
		t.Errorf("Long does not contain %q:\n%s", want, cmd.Long)
	}
}
