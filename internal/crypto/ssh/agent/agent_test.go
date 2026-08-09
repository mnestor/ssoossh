package agent

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// should report ssh-agent as the coarse type when backed by any live agent connection
func TestSshAgent_TypeAndBackend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		backend     string
		wantBackend string
	}{
		{name: "should default to openssh-agent when backend is unset", backend: "", wantBackend: BackendOpenSSHAgent},
		{name: "should report openssh-agent when explicitly set", backend: BackendOpenSSHAgent, wantBackend: BackendOpenSSHAgent},
		{name: "should report pageant when explicitly set", backend: BackendPageant, wantBackend: BackendPageant},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := &SshAgent{backend: tt.backend}

			if got := a.Type(); got != AgentTypeSsh {
				t.Errorf("Type() = %q, want %q", got, AgentTypeSsh)
			}
			if got := a.Backend(); got != tt.wantBackend {
				t.Errorf("Backend() = %q, want %q", got, tt.wantBackend)
			}
		})
	}
}

// should satisfy the generic Agent interface without requiring callers to
// know whether they hold a live agent or a file-backed implementation
func TestAgent_InterfaceSatisfiedByAllBackends(t *testing.T) {
	t.Parallel()

	var (
		_ Agent = (*SshAgent)(nil)
		_ Agent = (*FileAgent)(nil)
	)
}

// should expose the underlying golang.org/x/crypto/ssh/agent.Agent when present
func TestSshAgent_Agent(t *testing.T) {
	t.Parallel()

	var underlying agent.Agent
	a := &SshAgent{agent: underlying}

	if got := a.Agent(); got != underlying {
		t.Errorf("Agent() = %v, want %v", got, underlying)
	}
}

// newTestCert signs pub as an ssh.Certificate using caSigner.
func newTestCert(t *testing.T, pub ssh.PublicKey, caSigner ssh.Signer) *ssh.Certificate {
	t.Helper()
	cert := &ssh.Certificate{
		Key:         pub,
		CertType:    ssh.UserCert,
		ValidAfter:  uint64(time.Now().Add(-time.Hour).Unix()),
		ValidBefore: uint64(time.Now().Add(time.Hour).Unix()),
	}
	if err := cert.SignCert(rand.Reader, caSigner); err != nil {
		t.Fatalf("SignCert() error = %v", err)
	}
	return cert
}

// should accept multiple CAs registered across separate SetCA calls, matching a certificate signed by any of them
func TestSetCA_AccumulatesMultipleCAs(t *testing.T) {
	t.Parallel()

	ca1, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	ca2, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	ca2Signer, err := ssh.NewSignerFromKey(ca2.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	cert := newTestCert(t, leaf.Public(), ca2Signer)

	a := &SshAgent{}
	ca1Str, err := ca1.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("MarshalAuthorizedKey() error = %v", err)
	}
	ca2Str, err := ca2.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("MarshalAuthorizedKey() error = %v", err)
	}

	if err := a.SetCA(ca1Str); err != nil {
		t.Fatalf("SetCA(ca1) error = %v", err)
	}
	if err := a.SetCA(ca2Str); err != nil {
		t.Fatalf("SetCA(ca2) error = %v", err)
	}

	if len(a.cas) != 2 {
		t.Fatalf("expected 2 registered CAs, got %d", len(a.cas))
	}
	if !CertificateValid(cert, a.cas) {
		t.Error("expected certificate signed by ca2 to be valid against the combined CA set")
	}
}

// should reject a certificate not signed by any registered CA
func TestCertificateValid_RejectsUntrustedSigner(t *testing.T) {
	t.Parallel()

	untrustedCA, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	trustedCA, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	untrustedSigner, err := ssh.NewSignerFromKey(untrustedCA.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	cert := newTestCert(t, leaf.Public(), untrustedSigner)

	if CertificateValid(cert, []ssh.PublicKey{trustedCA.Public()}) {
		t.Error("expected certificate signed by an untrusted CA to be invalid")
	}
}
