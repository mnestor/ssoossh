//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// `ssh proxycommand` is the mode the README leads with, and nothing had ever
// run it. Its unit tests stub syscall.Exec, which is unavoidable there --
// the real call would replace the test binary -- so the handoff itself, the
// part that either relays a working connection or does not, had never
// happened in a test at all.
//
// These are tier 3: only a real ssh driving a real sshd exercises
// ProxyCommand the way production does.

// proxyOption builds the ssh -o ProxyCommand line a user would put in their
// ssh_config, with %h and %p left for ssh to substitute.
func proxyOption(ssoosshBin, serverURL string) string {
	return fmt.Sprintf("ProxyCommand %s ssh proxycommand --server %s /usr/bin/nc %%h %%p",
		ssoosshBin, serverURL)
}

func TestSSHProxy_ShouldRelayAConnectionSshdAccepts(t *testing.T) {
	f := newFixture(t)
	sshd := harness.StartSSHD(t, f.Server.CAPublicKey)

	// Log in first so a valid certificate is already in the agent. The
	// proxycommand's own runLogin then reuses it rather than opening a
	// second approval, which is both what happens in production on every
	// connection after the first and what makes this test deterministic --
	// otherwise it would have to approve concurrently with ssh connecting.
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, sshd.Principal, nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
	}

	output, err := harness.RunSSHWith(t, sshd, f.Agent.Socket,
		[]string{proxyOption(f.SsoosshBin, f.Server.BaseURL)},
		"echo ssoossh-proxy-ok")
	if err != nil {
		t.Fatalf("ssh through the proxycommand failed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "ssoossh-proxy-ok") {
		t.Errorf("got ssh output %q, want it to contain the echoed marker", output)
	}
}

// stdout belongs to the SSH stream from the moment the relay starts, so
// anything the client prints there corrupts the connection. runLogin is
// given ErrOrStderr for exactly this reason. A successful session above
// already proves the stream was not corrupted; this pins the reason, so a
// stray fmt.Println in the login path fails with a message that says what
// happened rather than an opaque protocol error.
func TestSSHProxy_ShouldKeepClientChatterOffTheRelayedStream(t *testing.T) {
	f := newFixture(t)
	sshd := harness.StartSSHD(t, f.Server.CAPublicKey)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, sshd.Principal, nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
	}

	// A marker the remote shell echoes, with nothing else expected on the
	// stream. If the client wrote progress to stdout it would arrive
	// interleaved and ssh would fail to negotiate at all.
	output, err := harness.RunSSHWith(t, sshd, f.Agent.Socket,
		[]string{proxyOption(f.SsoosshBin, f.Server.BaseURL)},
		"echo marker-only")
	if err != nil {
		t.Fatalf("ssh through the proxycommand failed: %v\noutput:\n%s", err, output)
	}

	// The login path's own progress wording must not appear in what the
	// session returned.
	for _, chatter := range []string{"Approve this request", "Waiting"} {
		if strings.Contains(output, chatter) {
			t.Errorf("client progress text reached the relayed stream: %q\noutput:\n%s", chatter, output)
		}
	}
}

func TestSSHProxy_ShouldFailWhenGivenNoCommandToExec(t *testing.T) {
	f := newFixture(t)

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "proxycommand", "--server", f.Server.BaseURL},
		Env:  map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit with nothing to exec, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "requires a command to exec") {
		t.Errorf("expected the error to say a command is required, got:\n%s", res.Stderr)
	}
}

// The refusal exists because ssh reads key files once at startup and never
// sees a certificate written after that, so file-backed proxycommand would
// appear to work and then fail authentication. It is reachable only through
// configuration -- there is no flag for it -- which is why nothing had
// tested it: until the harness could write a config file, this branch could
// not be reached from a test at all.
func TestSSHProxy_ShouldRefuseFileBasedKeyStorage(t *testing.T) {
	f := newFixture(t)

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:     []string{"ssh", "proxycommand", "--server", f.Server.BaseURL, "/usr/bin/nc", "127.0.0.1", "22"},
		UserYAML: "use_agent: false\n",
		// A live agent is deliberately still reachable. use_agent: false
		// means "do not touch my ssh-agent", not "prefer files when there
		// is no agent", so the refusal has to happen even here.
		Env: map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit with file-based keys, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "file based ssh keys are not supported for proxycommand") {
		t.Errorf("expected the file-storage refusal, got:\n%s", res.Stderr)
	}
}
