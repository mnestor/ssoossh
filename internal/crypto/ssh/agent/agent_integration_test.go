//go:build integration

package agent

// Test methodology: a real OpenSSH ssh-agent process, not a fake. These
// assertions are about what an agent actually does, which is exactly what a
// fake cannot tell us — and `ssh logout` is built on the answer.

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	xagent "golang.org/x/crypto/ssh/agent"
)

// startAgent runs a real ssh-agent and returns its socket path, killing it
// when the test ends.
func startAgent(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("ssh-agent"); err != nil {
		t.Skipf("ssh-agent not available: %v", err)
	}

	out, err := exec.Command("ssh-agent", "-c").Output()
	if err != nil {
		t.Fatalf("start ssh-agent: %v", err)
	}

	var sock, pid string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		switch fields[1] {
		case "SSH_AUTH_SOCK":
			sock = strings.TrimSuffix(fields[2], ";")
		case "SSH_AGENT_PID":
			pid = strings.TrimSuffix(fields[2], ";")
		}
	}
	if sock == "" {
		t.Fatalf("could not parse the agent socket out of %q", out)
	}

	t.Cleanup(func() {
		if pid != "" {
			_ = exec.Command("kill", pid).Run() //nolint:errcheck // best-effort teardown
		}
		_ = os.Remove(sock) //nolint:errcheck // best-effort teardown
	})
	return sock
}

// TestSshAgent_ShouldStoreACertifiedKeypairAsOneIdentity pins the behavior
// `ssh logout` depends on: AddKeypair sends a private key and its
// certificate together, and a real agent holds that as a single identity. If
// an agent ever split them in two, removing the certificate would leave the
// bare private key behind and logout would silently stop being a logout.
func TestSshAgent_ShouldStoreACertifiedKeypairAsOneIdentity(t *testing.T) {
	sock := startAgent(t)

	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial the agent: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() }) //nolint:errcheck // best-effort teardown

	client := xagent.NewClient(conn)

	caPublic, cert, keyPriv := certifiedKeypair(t)

	// An unrelated personal key: the thing logout must never touch.
	_, personal, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate a personal key: %v", err)
	}
	if err := client.Add(xagent.AddedKey{PrivateKey: &personal, Comment: "personal"}); err != nil {
		t.Fatalf("add the personal key: %v", err)
	}

	if err := client.Add(xagent.AddedKey{PrivateKey: &keyPriv, Certificate: cert, Comment: "ssoossh"}); err != nil {
		t.Fatalf("add the certified keypair: %v", err)
	}

	before, err := client.List()
	if err != nil {
		t.Fatalf("list identities: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("got %d identities, want 2 (the personal key and one certified keypair)", len(before))
	}

	sshAgent := &SshAgent{agent: client, backend: BackendOpenSSHAgent}
	if err := sshAgent.SetCA(string(ssh.MarshalAuthorizedKey(caPublic))); err != nil {
		t.Fatalf("set the CA: %v", err)
	}

	ours, err := sshAgent.List(true)
	if err != nil {
		t.Fatalf("list CA-signed identities: %v", err)
	}
	if len(ours) != 1 {
		t.Fatalf("got %d CA-signed identities, want 1", len(ours))
	}

	if err := sshAgent.Remove(*ours[0]); err != nil {
		t.Fatalf("remove the certificate identity: %v", err)
	}

	after, err := client.List()
	if err != nil {
		t.Fatalf("list identities after removal: %v", err)
	}
	if len(after) != 1 {
		t.Fatalf("got %d identities after removing ours, want 1 — a bare key was left behind", len(after))
	}
	if after[0].Comment != "personal" {
		t.Errorf("the surviving identity is %q, want the user's personal key", after[0].Comment)
	}
}

// certifiedKeypair returns a CA public key, a certificate over a fresh user
// key, and that user key's private half.
func certifiedKeypair(t *testing.T) (ssh.PublicKey, *ssh.Certificate, ed25519.PrivateKey) {
	t.Helper()

	_, caPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate a CA key: %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(caPriv)
	if err != nil {
		t.Fatalf("build a CA signer: %v", err)
	}

	userPub, userPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate a user key: %v", err)
	}
	sshUserPub, err := ssh.NewPublicKey(userPub)
	if err != nil {
		t.Fatalf("convert the user key: %v", err)
	}

	cert := &ssh.Certificate{
		Key:             sshUserPub,
		Serial:          1,
		CertType:        ssh.UserCert,
		KeyId:           "integration",
		ValidPrincipals: []string{"integration"},
		ValidAfter:      uint64(time.Now().Add(-time.Minute).Unix()),
		ValidBefore:     uint64(time.Now().Add(time.Hour).Unix()),
	}
	if err := cert.SignCert(rand.Reader, caSigner); err != nil {
		t.Fatalf("sign the certificate: %v", err)
	}

	return caSigner.PublicKey(), cert, userPriv
}
