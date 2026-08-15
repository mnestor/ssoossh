//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// Tier 3 ("ssh"): tier 1 plus a real sshd trusting the test CA — the
// certificate actually authenticating a session. Modifies the host: creates
// harness.TestSSHUser, unlocks it, and runs sshd as root via sudo. See
// docs/e2e-testing-plan.md.

func TestSSH_SshdAcceptsTheIssuedCertificate(t *testing.T) {
	f := newFixture(t)
	sshd := harness.StartSSHD(t, f.Server.CAPublicKey)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, sshd.Principal, nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
	}

	output, err := harness.RunSSH(t, sshd, f.Agent.Socket, "echo ssoossh-e2e-ok")
	if err != nil {
		t.Fatalf("ssh session failed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "ssoossh-e2e-ok") {
		t.Errorf("got ssh output %q, want it to contain the echoed marker", output)
	}
}

func TestSSH_AfterLogoutTheSameSSHIsRefused(t *testing.T) {
	f := newFixture(t)
	sshd := harness.StartSSHD(t, f.Server.CAPublicKey)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, sshd.Principal, nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
	}

	if output, err := harness.RunSSH(t, sshd, f.Agent.Socket, "true"); err != nil {
		t.Fatalf("expected the session to succeed before logout: %v\noutput:\n%s", err, output)
	}

	if output, err := harness.RunLogout(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket); err != nil {
		t.Fatalf("ssh logout failed: %v\noutput:\n%s", err, output)
	}

	output, err := harness.RunSSH(t, sshd, f.Agent.Socket, "true")
	if err == nil {
		t.Fatalf("expected ssh to be refused after logout, got a clean exit\noutput:\n%s", output)
	}
}
