package agent

import (
	"crypto/rand"
	"errors"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// fakeXAgent is a minimal in-memory implementation of
// golang.org/x/crypto/ssh/agent.Agent, used to unit test SshAgent without a
// live ssh-agent process. Each *Err field, when set, is returned by the
// corresponding method instead of the normal result.
type fakeXAgent struct {
	keys []*agent.Key

	listErr   error
	signErr   error
	addErr    error
	removeErr error

	added   []agent.AddedKey
	removed []ssh.PublicKey
}

func (f *fakeXAgent) List() ([]*agent.Key, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.keys, nil
}

func (f *fakeXAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	if f.signErr != nil {
		return nil, f.signErr
	}
	return &ssh.Signature{Format: "fake", Blob: data}, nil
}

func (f *fakeXAgent) Add(key agent.AddedKey) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.added = append(f.added, key)
	return nil
}

func (f *fakeXAgent) Remove(key ssh.PublicKey) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	f.removed = append(f.removed, key)
	for i, k := range f.keys {
		if publicKeysEqual(mustParseAgentKey(k), key) {
			f.keys = append(f.keys[:i], f.keys[i+1:]...)
			break
		}
	}
	return nil
}

func (f *fakeXAgent) RemoveAll() error {
	f.keys = nil
	return nil
}

func (f *fakeXAgent) Lock(passphrase []byte) error   { return nil }
func (f *fakeXAgent) Unlock(passphrase []byte) error { return nil }

func (f *fakeXAgent) Signers() ([]ssh.Signer, error) {
	return nil, errors.New("not implemented by fakeXAgent")
}

// mustParseAgentKey re-parses an agent.Key's marshaled bytes back into an
// ssh.PublicKey, mirroring what SshAgent itself does when reading List().
func mustParseAgentKey(k *agent.Key) ssh.PublicKey {
	pub, err := ssh.ParsePublicKey(k.Marshal())
	if err != nil {
		panic(err)
	}
	return pub
}

// agentKeyFor wraps an ssh.PublicKey as the *agent.Key shape List() returns.
func agentKeyFor(pub ssh.PublicKey) *agent.Key {
	return &agent.Key{Format: pub.Type(), Blob: pub.Marshal()}
}

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

// should list identities from the underlying agent, optionally filtered to certificates signed by a trusted CA
func TestSshAgent_List(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	cert := newTestCert(t, leaf.Public(), caSigner)
	plainKey, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	t.Run("should error when filtering by CA but no CA is registered", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{}}
		if _, err := a.List(true); err == nil {
			t.Error("List(true) error = nil, want error")
		}
	})

	t.Run("should return all identities unfiltered", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{keys: []*agent.Key{agentKeyFor(plainKey.Public()), agentKeyFor(cert)}}}
		got, err := a.List(false)
		if err != nil {
			t.Fatalf("List(false) error = %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("List(false) returned %d identities, want 2", len(got))
		}
	})

	t.Run("should return only CA-signed certificates when filtering", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{keys: []*agent.Key{agentKeyFor(plainKey.Public()), agentKeyFor(cert)}}, cas: []ssh.PublicKey{ca.Public()}}
		got, err := a.List(true)
		if err != nil {
			t.Fatalf("List(true) error = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("List(true) returned %d identities, want 1", len(got))
		}
	})

	t.Run("should propagate an error from the underlying agent", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{listErr: errors.New("boom")}}
		if _, err := a.List(false); err == nil {
			t.Error("List(false) error = nil, want error")
		}
	})

	t.Run("should propagate an error when an identity's key bytes cannot be parsed", func(t *testing.T) {
		t.Parallel()
		bad := &agent.Key{Format: "ssh-ed25519", Blob: []byte("not a valid key blob")}
		a := &SshAgent{agent: &fakeXAgent{keys: []*agent.Key{bad}}}
		if _, err := a.List(false); err == nil {
			t.Error("List(false) error = nil, want error")
		}
	})
}

// should delegate signing to the underlying agent
func TestSshAgent_Sign(t *testing.T) {
	t.Parallel()

	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	t.Run("should return the signature from the underlying agent", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{}}
		sig, err := a.Sign(leaf.Public(), []byte("data"))
		if err != nil {
			t.Fatalf("Sign() error = %v", err)
		}
		if sig == nil {
			t.Fatal("Sign() returned nil signature")
		}
	})

	t.Run("should propagate an error from the underlying agent", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{signErr: errors.New("boom")}}
		if _, err := a.Sign(leaf.Public(), []byte("data")); err == nil {
			t.Error("Sign() error = nil, want error")
		}
	})
}

// should accept only agent.AddedKey values, delegating everything else as an error
func TestSshAgent_Add(t *testing.T) {
	t.Parallel()

	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	t.Run("should add an agent.AddedKey via the underlying agent", func(t *testing.T) {
		t.Parallel()
		fake := &fakeXAgent{}
		a := &SshAgent{agent: fake}
		key := agent.AddedKey{PrivateKey: leaf.Private()}
		if err := a.Add(key); err != nil {
			t.Fatalf("Add() error = %v", err)
		}
		if len(fake.added) != 1 {
			t.Fatalf("underlying agent recorded %d Add calls, want 1", len(fake.added))
		}
	})

	t.Run("should reject a key of the wrong type", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{}}
		if err := a.Add("not an AddedKey"); err == nil {
			t.Error("Add() error = nil, want error for unsupported key type")
		}
	})

	t.Run("should propagate an error from the underlying agent", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{addErr: errors.New("boom")}}
		if err := a.Add(agent.AddedKey{PrivateKey: leaf.Private()}); err == nil {
			t.Error("Add() error = nil, want error")
		}
	})
}

// should close the underlying connection when present, and be a no-op otherwise
func TestSshAgent_Close(t *testing.T) {
	t.Parallel()

	t.Run("should be a no-op when there is no connection (e.g. Pageant)", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{}
		if err := a.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})

	t.Run("should close the underlying connection when present", func(t *testing.T) {
		t.Parallel()
		serverConn, clientConn := net.Pipe()
		t.Cleanup(func() { _ = serverConn.Close() }) //nolint:errcheck // test cleanup
		a := &SshAgent{conn: clientConn}
		if err := a.Close(); err != nil {
			t.Errorf("Close() error = %v, want nil", err)
		}
	})
}

// should delegate identity removal to the underlying agent
func TestSshAgent_Remove(t *testing.T) {
	t.Parallel()

	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	t.Run("should remove via the underlying agent", func(t *testing.T) {
		t.Parallel()
		fake := &fakeXAgent{}
		a := &SshAgent{agent: fake}
		if err := a.Remove(leaf.Public()); err != nil {
			t.Fatalf("Remove() error = %v", err)
		}
		if len(fake.removed) != 1 {
			t.Fatalf("underlying agent recorded %d Remove calls, want 1", len(fake.removed))
		}
	})

	t.Run("should propagate an error from the underlying agent", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{removeErr: errors.New("boom")}}
		if err := a.Remove(leaf.Public()); err == nil {
			t.Error("Remove() error = nil, want error")
		}
	})
}

// should remove every identity in the agent, one at a time, and count the removals
func TestSshAgent_RemoveAll(t *testing.T) {
	t.Parallel()

	leaf1, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	leaf2, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	t.Run("should remove all identities and report the count", func(t *testing.T) {
		t.Parallel()
		fake := &fakeXAgent{keys: []*agent.Key{agentKeyFor(leaf1.Public()), agentKeyFor(leaf2.Public())}}
		a := &SshAgent{agent: fake}
		n, err := a.RemoveAll()
		if err != nil {
			t.Fatalf("RemoveAll() error = %v", err)
		}
		if n != 2 {
			t.Errorf("RemoveAll() = %d, want 2", n)
		}
	})

	t.Run("should propagate an error from List", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{listErr: errors.New("boom")}}
		if _, err := a.RemoveAll(); err == nil {
			t.Error("RemoveAll() error = nil, want error")
		}
	})

	t.Run("should propagate an error from Remove", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{keys: []*agent.Key{agentKeyFor(leaf1.Public())}, removeErr: errors.New("boom")}}
		if _, err := a.RemoveAll(); err == nil {
			t.Error("RemoveAll() error = nil, want error")
		}
	})
}

// should delegate signer retrieval to the underlying agent
func TestSshAgent_Signers(t *testing.T) {
	t.Parallel()

	a := &SshAgent{agent: &fakeXAgent{}}
	if _, err := a.Signers(); err == nil {
		t.Error("Signers() error = nil, want error (fakeXAgent does not implement Signers)")
	}
}

// should remove certificate identities that are expired or untrusted, leaving valid ones and non-certificate keys alone
func TestSshAgent_CleanupAgent(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	untrustedCA, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	untrustedSigner, err := ssh.NewSignerFromKey(untrustedCA.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	plainKey, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	validCert := newTestCert(t, leaf.Public(), caSigner)
	untrustedCert := newTestCert(t, leaf.Public(), untrustedSigner)

	t.Run("should remove a certificate not signed by a trusted CA", func(t *testing.T) {
		t.Parallel()
		fake := &fakeXAgent{keys: []*agent.Key{agentKeyFor(untrustedCert)}}
		a := &SshAgent{agent: fake, cas: []ssh.PublicKey{ca.Public()}}
		if err := a.CleanupAgent(); err != nil {
			t.Fatalf("CleanupAgent() error = %v", err)
		}
		if len(fake.keys) != 0 {
			t.Errorf("CleanupAgent() left %d keys, want 0", len(fake.keys))
		}
	})

	t.Run("should leave a valid, trusted certificate in place", func(t *testing.T) {
		t.Parallel()
		fake := &fakeXAgent{keys: []*agent.Key{agentKeyFor(validCert)}}
		a := &SshAgent{agent: fake, cas: []ssh.PublicKey{ca.Public()}}
		if err := a.CleanupAgent(); err != nil {
			t.Fatalf("CleanupAgent() error = %v", err)
		}
		if len(fake.keys) != 1 {
			t.Errorf("CleanupAgent() left %d keys, want 1", len(fake.keys))
		}
	})

	t.Run("should leave a non-certificate key untouched", func(t *testing.T) {
		t.Parallel()
		fake := &fakeXAgent{keys: []*agent.Key{agentKeyFor(plainKey.Public())}}
		a := &SshAgent{agent: fake, cas: []ssh.PublicKey{ca.Public()}}
		if err := a.CleanupAgent(); err != nil {
			t.Fatalf("CleanupAgent() error = %v", err)
		}
		if len(fake.keys) != 1 {
			t.Errorf("CleanupAgent() left %d keys, want 1", len(fake.keys))
		}
	})

	t.Run("should propagate an error from List", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{listErr: errors.New("boom")}, cas: []ssh.PublicKey{ca.Public()}}
		if err := a.CleanupAgent(); err == nil {
			t.Error("CleanupAgent() error = nil, want error")
		}
	})

	t.Run("should refuse to run when no CAs are registered", func(t *testing.T) {
		t.Parallel()
		fake := &fakeXAgent{keys: []*agent.Key{agentKeyFor(validCert)}}
		a := &SshAgent{agent: fake}
		if err := a.CleanupAgent(); err == nil {
			t.Error("CleanupAgent() error = nil, want error with no CAs registered")
		}
		if len(fake.keys) != 1 {
			t.Errorf("CleanupAgent() removed keys with no CAs registered, left %d, want 1", len(fake.keys))
		}
	})

	t.Run("should skip an identity whose key bytes cannot be parsed", func(t *testing.T) {
		t.Parallel()
		bad := &agent.Key{Format: "ssh-ed25519", Blob: []byte("not a valid key blob")}
		fake := &fakeXAgent{keys: []*agent.Key{bad, agentKeyFor(validCert)}}
		a := &SshAgent{agent: fake, cas: []ssh.PublicKey{ca.Public()}}
		if err := a.CleanupAgent(); err != nil {
			t.Fatalf("CleanupAgent() error = %v", err)
		}
		if len(fake.keys) != 2 {
			t.Errorf("CleanupAgent() left %d keys, want 2 (unparseable key untouched, valid cert kept)", len(fake.keys))
		}
	})
}

// should return only certificates signed by a trusted CA, erroring when none are configured or none qualify
func TestSshAgent_Certificates(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	plainKey, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	validCert := newTestCert(t, leaf.Public(), caSigner)

	t.Run("should error when no CA is registered", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{}}
		if _, err := a.Certificates(); err == nil {
			t.Error("Certificates() error = nil, want error")
		}
	})

	t.Run("should return certificates signed by a trusted CA, skipping non-certificate keys", func(t *testing.T) {
		t.Parallel()
		fake := &fakeXAgent{keys: []*agent.Key{agentKeyFor(plainKey.Public()), agentKeyFor(validCert)}}
		a := &SshAgent{agent: fake, cas: []ssh.PublicKey{ca.Public()}}
		got, err := a.Certificates()
		if err != nil {
			t.Fatalf("Certificates() error = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("Certificates() returned %d certs, want 1", len(got))
		}
	})

	t.Run("should error when no identity qualifies", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{keys: []*agent.Key{agentKeyFor(plainKey.Public())}}, cas: []ssh.PublicKey{ca.Public()}}
		if _, err := a.Certificates(); err == nil {
			t.Error("Certificates() error = nil, want error")
		}
	})

	t.Run("should propagate an error from the underlying agent", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{listErr: errors.New("boom")}, cas: []ssh.PublicKey{ca.Public()}}
		if _, err := a.Certificates(); err == nil {
			t.Error("Certificates() error = nil, want error")
		}
	})

	t.Run("should skip an identity whose key bytes cannot be parsed", func(t *testing.T) {
		t.Parallel()
		bad := &agent.Key{Format: "ssh-ed25519", Blob: []byte("not a valid key blob")}
		fake := &fakeXAgent{keys: []*agent.Key{bad, agentKeyFor(validCert)}}
		a := &SshAgent{agent: fake, cas: []ssh.PublicKey{ca.Public()}}
		got, err := a.Certificates()
		if err != nil {
			t.Fatalf("Certificates() error = %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("Certificates() returned %d certs, want 1", len(got))
		}
	})
}

// should send the keypair's private key, certificate, and a fixed comment to the underlying agent
func TestSshAgent_AddKeypair(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := ssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	cert := newTestCert(t, leaf.Public(), caSigner)
	leaf.SetCertificate(cert)

	t.Run("should add the keypair's private key and certificate", func(t *testing.T) {
		t.Parallel()
		fake := &fakeXAgent{}
		a := &SshAgent{agent: fake}
		if err := a.AddKeypair(leaf); err != nil {
			t.Fatalf("AddKeypair() error = %v", err)
		}
		if len(fake.added) != 1 {
			t.Fatalf("underlying agent recorded %d Add calls, want 1", len(fake.added))
		}
		got := fake.added[0]
		if got.Comment != "ssoossh" {
			t.Errorf("AddKeypair() comment = %q, want %q", got.Comment, "ssoossh")
		}
		if !reflect.DeepEqual(got.PrivateKey, leaf.Private()) {
			t.Errorf("AddKeypair() private key = %v, want %v", got.PrivateKey, leaf.Private())
		}
		if got.Certificate != cert {
			t.Errorf("AddKeypair() certificate = %v, want %v", got.Certificate, cert)
		}
	})

	t.Run("should propagate an error from the underlying agent", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{agent: &fakeXAgent{addErr: errors.New("boom")}}
		if err := a.AddKeypair(leaf); err == nil {
			t.Error("AddKeypair() error = nil, want error")
		}
	})
}

// should require at least one CA and reject an unparseable CA string
func TestSshAgent_SetCA_ErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("should reject a call with no CAs", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{}
		if err := a.SetCA(); err == nil {
			t.Error("SetCA() error = nil, want error")
		}
	})

	t.Run("should reject an unparseable CA string", func(t *testing.T) {
		t.Parallel()
		a := &SshAgent{}
		if err := a.SetCA("not a key"); err == nil {
			t.Error("SetCA() error = nil, want error")
		}
	})
}
