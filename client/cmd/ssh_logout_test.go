package cmd

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"
	xagent "golang.org/x/crypto/ssh/agent"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// stubAgent is a stateful agent.Agent holding a set of identities, enough to
// tell "removed the right thing" from "removed everything". Unlike fakeAgent
// it remembers what happened to it.
type stubAgent struct {
	identities []xssh.PublicKey
	cas        []xssh.PublicKey

	agentType string
	// removeAllCalled records the hazard this package must never trip: an
	// agent-wide wipe takes the user's own keys with it.
	removeAllCalled bool
	added           *keypair.SSHKeypair
	removeErr       error
}

func (s *stubAgent) Type() string {
	if s.agentType == "" {
		return agent.AgentTypeSsh
	}
	return s.agentType
}
func (s *stubAgent) Backend() string { return "stub" }

// List mirrors SshAgent.List: filterByCA keeps only certificates signed by a
// trusted CA, with no time-validity check — an expired ssoossh certificate is
// still ssoossh's to remove.
func (s *stubAgent) List(filterByCA bool) ([]*xssh.PublicKey, error) {
	var out []*xssh.PublicKey
	for i := range s.identities {
		key := s.identities[i]
		if !filterByCA {
			out = append(out, &key)
			continue
		}
		cert, ok := key.(*xssh.Certificate)
		if !ok || cert.SignatureKey == nil {
			continue
		}
		for _, ca := range s.cas {
			if bytes.Equal(cert.SignatureKey.Marshal(), ca.Marshal()) {
				out = append(out, &key)
				break
			}
		}
	}
	return out, nil
}

func (s *stubAgent) Add(key any) error { return nil }

func (s *stubAgent) Remove(key xssh.PublicKey) error {
	if s.removeErr != nil {
		return s.removeErr
	}
	for i, held := range s.identities {
		if bytes.Equal(held.Marshal(), key.Marshal()) {
			s.identities = append(s.identities[:i], s.identities[i+1:]...)
			return nil
		}
	}
	return errors.New("no such identity")
}

func (s *stubAgent) RemoveAll() (int, error) {
	s.removeAllCalled = true
	n := len(s.identities)
	s.identities = nil
	return n, nil
}

func (s *stubAgent) Signers() ([]xssh.Signer, error) { return nil, nil }
func (s *stubAgent) Close() error                    { return nil }
func (s *stubAgent) Agent() xagent.Agent             { return nil }
func (s *stubAgent) SetCA(cas ...string) error       { return nil }

// Certificates mirrors the real implementations: CA-signed *and* time-valid.
func (s *stubAgent) Certificates() ([]*xssh.Certificate, error) {
	var certs []*xssh.Certificate
	for _, key := range s.identities {
		cert, ok := key.(*xssh.Certificate)
		if !ok {
			continue
		}
		if !agent.CertificateValid(cert, s.cas) {
			continue
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, errors.New("no valid certificates found signed by the CA")
	}
	return certs, nil
}

func (s *stubAgent) AddKeypair(kp *keypair.SSHKeypair) error {
	s.added = kp
	if cert := kp.Certificate(); cert != nil {
		s.identities = append(s.identities, cert)
	}
	return nil
}

func (s *stubAgent) CleanupAgent() error { return nil }

var _ agent.Agent = (*stubAgent)(nil)

// testCA is a signing key plus its public half, for building certificates a
// test can present as "ours" or "somebody else's".
type testCA struct {
	signer xssh.Signer
	public xssh.PublicKey
}

func newTestCA(t *testing.T) testCA {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	signer, err := xssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("build CA signer: %v", err)
	}
	return testCA{signer: signer, public: signer.PublicKey()}
}

// newTestKey returns a fresh ed25519 public key, standing in for a user's own
// key or the key under a certificate.
func newTestKey(t *testing.T) xssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := xssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("convert key: %v", err)
	}
	return sshPub
}

// newTestCert signs a certificate over a fresh key, valid for validFor from
// now (pass a negative duration for an expired one).
func newTestCert(t *testing.T, ca testCA, principal string, validFor time.Duration) *xssh.Certificate {
	t.Helper()
	cert := &xssh.Certificate{
		Key:             newTestKey(t),
		Serial:          1,
		CertType:        xssh.UserCert,
		KeyId:           principal,
		ValidPrincipals: []string{principal},
		ValidAfter:      uint64(time.Now().Add(-time.Hour).Unix()),
		ValidBefore:     uint64(time.Now().Add(validFor).Unix()),
	}
	if err := cert.SignCert(rand.Reader, ca.signer); err != nil {
		t.Fatalf("sign certificate: %v", err)
	}
	return cert
}

// TestRunLogout_ShouldLeaveUnrelatedAgentKeysUntouched is the regression test
// for the worst thing this command could do. An earlier sketch of logout
// called RemoveAll, which empties the agent — the user's personal keys
// included. Only certificates signed by the configured CA are ours.
func TestRunLogout_ShouldLeaveUnrelatedAgentKeysUntouched(t *testing.T) {
	ours := newTestCA(t)
	theirs := newTestCA(t)

	personalKey := newTestKey(t)
	ourCert := newTestCert(t, ours, "alice", time.Hour)
	foreignCert := newTestCert(t, theirs, "alice", time.Hour)

	ag := &stubAgent{
		identities: []xssh.PublicKey{personalKey, ourCert, foreignCert},
		cas:        []xssh.PublicKey{ours.public},
	}

	var out bytes.Buffer
	if err := runLogout(&RootCommand{ssh: ag}, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ag.removeAllCalled {
		t.Error("logout called RemoveAll, which would take the user's own keys with it")
	}

	remaining := map[string]bool{}
	for _, key := range ag.identities {
		remaining[string(key.Marshal())] = true
	}
	if !remaining[string(personalKey.Marshal())] {
		t.Error("logout removed the user's personal key")
	}
	if !remaining[string(foreignCert.Marshal())] {
		t.Error("logout removed a certificate from an unrelated CA")
	}
	if remaining[string(ourCert.Marshal())] {
		t.Error("logout left ssoossh's own certificate in the agent")
	}
}

// TestRunLogout_ShouldRemoveExpiredCertificates covers the difference between
// "ours" and "usable": an expired ssoossh certificate is still ours, and
// leaving it behind means logout did not log out.
func TestRunLogout_ShouldRemoveExpiredCertificates(t *testing.T) {
	ours := newTestCA(t)
	expired := newTestCert(t, ours, "alice", -time.Minute)

	ag := &stubAgent{identities: []xssh.PublicKey{expired}, cas: []xssh.PublicKey{ours.public}}

	var out bytes.Buffer
	if err := runLogout(&RootCommand{ssh: ag}, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(ag.identities) != 0 {
		t.Errorf("expected the expired ssoossh certificate to be removed, %d identities remain", len(ag.identities))
	}
}

func TestRunLogout_ShouldReportWhenThereWasNothingToRemove(t *testing.T) {
	ours := newTestCA(t)
	ag := &stubAgent{identities: []xssh.PublicKey{newTestKey(t)}, cas: []xssh.PublicKey{ours.public}}

	var out bytes.Buffer
	if err := runLogout(&RootCommand{ssh: ag}, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out.String(), "Nothing to remove") {
		t.Errorf("got %q, want it to say there was nothing to remove", out.String())
	}
}

// TestRunLogout_ShouldDeleteKeyFilesForAFileAgent covers the other backend:
// a FileAgent owns exactly one identity — its own files — so its RemoveAll is
// already correctly scoped and is the right call there.
func TestRunLogout_ShouldDeleteKeyFilesForAFileAgent(t *testing.T) {
	ag := &stubAgent{agentType: agent.AgentTypeFile, identities: []xssh.PublicKey{newTestKey(t)}}

	var out bytes.Buffer
	if err := runLogout(&RootCommand{ssh: ag}, &out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !ag.removeAllCalled {
		t.Error("expected a file agent's own RemoveAll to be used")
	}
	if !strings.Contains(out.String(), "key file") {
		t.Errorf("got %q, want it to mention key files", out.String())
	}
}

func TestRunLogout_ShouldReportAFailedRemoval(t *testing.T) {
	ours := newTestCA(t)
	ag := &stubAgent{
		identities: []xssh.PublicKey{newTestCert(t, ours, "alice", time.Hour)},
		cas:        []xssh.PublicKey{ours.public},
		removeErr:  errors.New("agent refused"),
	}

	var out bytes.Buffer
	err := runLogout(&RootCommand{ssh: ag}, &out)
	if err == nil {
		t.Fatal("expected an error when the agent refuses a removal")
	}
	if !strings.Contains(err.Error(), "agent refused") {
		t.Errorf("got %q, want the underlying agent error surfaced", err)
	}
}
