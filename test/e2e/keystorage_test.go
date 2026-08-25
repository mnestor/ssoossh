//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// File-based key storage had no end-to-end coverage at all -- the open
// entry at the top of docs/dev/testing-needs.md. Every harness invocation put a
// live SSH_AUTH_SOCK in the environment and nothing could set
// use_agent: false, because nothing could write a config file.
//
// That is what let a real bug ship: a file-backed `ssh login` deleted the
// key files it had just written and reported success (fixed in 800d5e1).
// The agent path was fine, because a real ssh-agent's List returns
// certificates and pruneSuperseded matched the identity it had just
// installed. Only the file path, the one never driven, was broken.

// keyFilename is the default from client/config/defaults.yaml. Named here
// so the tests below can find what the client wrote.
const keyFilename = "id_ssoossh"

// loginWithFileStorage runs a full approved login with use_agent: false and
// returns the home directory the key files landed under.
func loginWithFileStorage(t *testing.T, f *fixture, principal string, extraYAML string) string {
	t.Helper()

	home := t.TempDir()
	login := harness.StartClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:     []string{"ssh", "login", "--server", f.Server.BaseURL},
		Home:     home,
		UserYAML: "use_agent: false\n" + extraYAML,
		// No agent at all. use_agent: false already means the agent is not
		// consulted, so removing the variable makes the test say what it
		// means rather than relying on the setting alone.
		Unset: []string{"SSH_AUTH_SOCK"},
	})

	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))
	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, principal, nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("file-backed ssh login failed: %v\nstderr:\n%s", err, login.Stderr())
	}
	return home
}

// The regression guard for 800d5e1: all three files have to exist after a
// successful login. The bug reported success while deleting them.
func TestKeyStorage_ShouldLeaveAllThreeFilesOnDiskAfterAFileBackedLogin(t *testing.T) {
	f := newFixture(t)

	home := loginWithFileStorage(t, f, "alice", "")

	privateKey, publicKey, certificate := harness.KeyFilePaths(home, keyFilename)
	for _, path := range []string{privateKey, publicKey, certificate} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist after a file-backed login: %v", path, err)
		}
	}

	// The certificate has to be a certificate, not a bare public key --
	// which is exactly the distinction the unit test that missed this bug
	// failed to make (it asserted only a length).
	data, err := os.ReadFile(certificate)
	if err != nil {
		t.Fatalf("failed to read the certificate: %v", err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		t.Fatalf("the certificate file does not parse: %v", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		t.Fatalf("the certificate file holds a %s, not a certificate", pub.Type())
	}
	if got := cert.ValidPrincipals; len(got) != 1 || got[0] != "alice" {
		t.Errorf("got principals %v, want [alice]", got)
	}
}

// The private key must not be world- or group-readable. ssh refuses to use
// a key with loose permissions, so getting this wrong makes file mode fail
// at the point of use rather than at the point of writing.
func TestKeyStorage_ShouldWriteThePrivateKeyOwnerReadableOnly(t *testing.T) {
	f := newFixture(t)

	home := loginWithFileStorage(t, f, "alice", "")

	privateKey, _, _ := harness.KeyFilePaths(home, keyFilename)
	info, err := os.Stat(privateKey)
	if err != nil {
		t.Fatalf("failed to stat the private key: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("private key mode is %#o, want no group or other access", perm)
	}
}

// use_agent: false means "do not touch my ssh-agent", not "prefer files
// when there is no agent". A running agent must be left completely alone,
// or the setting fails at the one thing it exists to guarantee.
func TestKeyStorage_ShouldNotTouchARunningAgentWhenUseAgentIsFalse(t *testing.T) {
	f := newFixture(t)

	home := t.TempDir()
	login := harness.StartClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:     []string{"ssh", "login", "--server", f.Server.BaseURL},
		Home:     home,
		UserYAML: "use_agent: false\n",
		// The agent is deliberately reachable here.
		Env: map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})

	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))
	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("file-backed ssh login failed: %v\nstderr:\n%s", err, login.Stderr())
	}

	if certs := f.Agent.Certificates(t); len(certs) != 0 {
		t.Errorf("use_agent: false still loaded %d certificate(s) into the agent", len(certs))
	}
	_, _, certificate := harness.KeyFilePaths(home, keyFilename)
	if _, err := os.Stat(certificate); err != nil {
		t.Errorf("expected the certificate on disk instead: %v", err)
	}
}

// fallback_file_agent decides what happens when an agent was wanted and is
// unreachable: key files, or fail closed. Both directions matter, and
// neither had ever been exercised end to end.
func TestKeyStorage_ShouldHonourFallbackFileAgentWhenNoAgentIsReachable(t *testing.T) {
	f := newFixture(t)

	t.Run("falls back to files when allowed", func(t *testing.T) {
		home := t.TempDir()
		login := harness.StartClient(t, f.SsoosshBin, harness.ClientOptions{
			Args:     []string{"ssh", "login", "--server", f.Server.BaseURL},
			Home:     home,
			UserYAML: "use_agent: true\nfallback_file_agent: true\n",
			Unset:    []string{"SSH_AUTH_SOCK"},
		})

		requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))
		client := newBrowserClient(t)
		approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

		if err := login.Wait(t, waitFor); err != nil {
			t.Fatalf("expected the fallback to succeed: %v\nstderr:\n%s", err, login.Stderr())
		}
		_, _, certificate := harness.KeyFilePaths(home, keyFilename)
		if _, err := os.Stat(certificate); err != nil {
			t.Errorf("expected a certificate written to disk by the fallback: %v", err)
		}
		// The fallback is a downgrade from what was asked for, so it warns.
		if !strings.Contains(login.Stderr(), "falling back to file-based storage") {
			t.Errorf("expected a warning about the fallback, got:\n%s", login.Stderr())
		}
	})

	t.Run("fails closed when not allowed", func(t *testing.T) {
		res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
			Args:     []string{"ssh", "login", "--server", f.Server.BaseURL},
			UserYAML: "use_agent: true\nfallback_file_agent: false\n",
			Unset:    []string{"SSH_AUTH_SOCK"},
		})

		if res.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit with no agent and no fallback, got 0\nstdout:\n%s", res.Stdout)
		}
		if !strings.Contains(res.Stderr, "connect to ssh-agent") {
			t.Errorf("expected the error to name the agent connection, got:\n%s", res.Stderr)
		}
	})
}

// The other half of 800d5e1: logout has to remove the files it wrote and
// leave everything else alone. FileAgent.Remove honouring the key it is
// given was itself a recent fix (d3e3b6e).
func TestKeyStorage_ShouldRemoveOnlyItsOwnFilesOnLogout(t *testing.T) {
	f := newFixture(t)

	home := loginWithFileStorage(t, f, "alice", "")
	privateKey, publicKey, certificate := harness.KeyFilePaths(home, keyFilename)

	// An unrelated key sitting beside ours, as a real ~/.ssh would have.
	unrelated := strings.TrimSuffix(privateKey, keyFilename) + "id_unrelated"
	if err := os.WriteFile(unrelated, []byte("unrelated private key\n"), 0600); err != nil {
		t.Fatalf("failed to write the unrelated key: %v", err)
	}

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:     []string{"ssh", "logout", "--server", f.Server.BaseURL},
		Home:     home,
		UserYAML: "use_agent: false\n",
		Unset:    []string{"SSH_AUTH_SOCK"},
	})
	if res.ExitCode != 0 {
		t.Fatalf("file-backed ssh logout failed with exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	for _, path := range []string{privateKey, publicKey, certificate} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("expected logout to remove %s", path)
		}
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Errorf("logout removed an unrelated key file: %v", err)
	}
}

// An absolute key_filename bypasses the $HOME/.ssh rule entirely, which is
// what a service account or a system daemon would use.
func TestKeyStorage_ShouldHonourAnAbsoluteKeyFilename(t *testing.T) {
	f := newFixture(t)

	dir := t.TempDir()
	absolute := dir + "/service-key"

	login := harness.StartClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:     []string{"ssh", "login", "--server", f.Server.BaseURL},
		UserYAML: "use_agent: false\nkey_filename: " + absolute + "\n",
		Unset:    []string{"SSH_AUTH_SOCK"},
	})

	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))
	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("file-backed ssh login failed: %v\nstderr:\n%s", err, login.Stderr())
	}

	for _, path := range []string{absolute, absolute + ".pub", absolute + "-cert.pub"} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}
}

// Tier 3, and the point of the whole file: an sshd accepts a certificate
// obtained in file mode. This is the documented Match exec story for hosts
// without an agent, and nothing proved it worked.
func TestSSH_ShouldAcceptACertificateObtainedInFileMode(t *testing.T) {
	f := newFixture(t)
	sshd := harness.StartSSHD(t, f.Server.CAPublicKey)

	home := loginWithFileStorage(t, f, sshd.Principal, "")
	privateKey, _, _ := harness.KeyFilePaths(home, keyFilename)

	// No agent: SSH_AUTH_SOCK empty, IdentitiesOnly on, and ssh derives the
	// certificate's name from IdentityFile.
	output, err := harness.RunSSHWith(t, sshd, "", []string{
		"IdentityFile " + privateKey,
		"IdentitiesOnly yes",
	}, "echo ssoossh-filemode-ok")
	if err != nil {
		t.Fatalf("ssh with a file-mode certificate failed: %v\noutput:\n%s", err, output)
	}
	if !strings.Contains(output, "ssoossh-filemode-ok") {
		t.Errorf("got ssh output %q, want it to contain the echoed marker", output)
	}
}
