//go:build pam

package main

// Test methodology: check 2 (key binding) is written and tested first, per
// docs/release-phase5-pam-client.md — it is the check that would otherwise
// be missing entirely. Its test uses a genuinely CA-signed certificate (via
// testCA.sign, a real (*ssh.Certificate).SignCert call), not a hand-built
// struct with a fake SignatureKey: the point is that checks 1, 3, and 4 all
// pass and only check 2 catches the substitution.

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// testCA is a throwaway CA used to build genuinely-signed certificates.
type testCA struct {
	signer ssh.Signer
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()

	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate CA keypair: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(kp.Private())
	if err != nil {
		t.Fatalf("failed to build CA signer: %v", err)
	}
	return &testCA{signer: signer}
}

func (ca *testCA) publicKey() ssh.PublicKey { return ca.signer.PublicKey() }

// sign issues a certificate for subjectKey, genuinely signed by ca.
func (ca *testCA) sign(t *testing.T, subjectKey ssh.PublicKey, principals []string, validAfter, validBefore time.Time) *ssh.Certificate {
	t.Helper()

	cert := &ssh.Certificate{
		Key:             subjectKey,
		CertType:        ssh.UserCert,
		ValidPrincipals: principals,
		ValidAfter:      uint64(validAfter.Unix()),  //nolint:gosec // test fixture, always a real date
		ValidBefore:     uint64(validBefore.Unix()), //nolint:gosec // test fixture, always a real date
	}
	if err := cert.SignCert(rand.Reader, ca.signer); err != nil {
		t.Fatalf("failed to sign test certificate: %v", err)
	}
	return cert
}

// newTestKeypair generates a fresh ephemeral keypair, standing in for the
// one Authenticate generates per attempt.
func newTestKeypair(t *testing.T) *keypair.SSHKeypair {
	t.Helper()

	kp, err := keypair.NewSSHKeypair("ed25519", 0)
	if err != nil {
		t.Fatalf("failed to generate keypair: %v", err)
	}
	return kp
}

func TestCheckKeyBinding_ShouldRejectGenuineCertificateIssuedToADifferentKey(t *testing.T) {
	ca := newTestCA(t)
	attacker := newTestKeypair(t)    // a certificate legitimately issued to this key
	thisAttempt := newTestKeypair(t) // the key generated for the attempt under test

	now := time.Now()
	// CA-signed, right principal, valid window — checks 1, 3, and 4 would
	// all pass. Only check 2 notices the key is wrong.
	cert := ca.sign(t, attacker.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour))

	if err := checkKeyBinding(cert, thisAttempt); err == nil {
		t.Fatal("expected the certificate to be rejected: signed by the CA for the right principal, but issued to a different key")
	}
}

func TestCheckKeyBinding_ShouldAcceptCertificateIssuedToTheSameKey(t *testing.T) {
	ca := newTestCA(t)
	kp := newTestKeypair(t)

	now := time.Now()
	cert := ca.sign(t, kp.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour))

	if err := checkKeyBinding(cert, kp); err != nil {
		t.Errorf("expected the certificate to be accepted, got %v", err)
	}
}

func TestCheckCASignature_ShouldAcceptACertificateSignedByAnyTrustedCA(t *testing.T) {
	trusted1 := newTestCA(t)
	trusted2 := newTestCA(t)
	kp := newTestKeypair(t)

	now := time.Now()
	cert := trusted2.sign(t, kp.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour))
	kp.SetCertificate(cert)

	if err := checkCASignature(kp, []ssh.PublicKey{trusted1.publicKey(), trusted2.publicKey()}); err != nil {
		t.Errorf("expected a certificate signed by the second of several trusted CAs to be accepted, got %v", err)
	}
}

func TestCheckCASignature_ShouldRejectACertificateSignedByAnUntrustedCA(t *testing.T) {
	untrusted := newTestCA(t)
	trusted := newTestCA(t)
	kp := newTestKeypair(t)

	now := time.Now()
	cert := untrusted.sign(t, kp.Public(), []string{"alice"}, now.Add(-time.Minute), now.Add(time.Hour))
	kp.SetCertificate(cert)

	if err := checkCASignature(kp, []ssh.PublicKey{trusted.publicKey()}); err == nil {
		t.Fatal("expected a certificate signed by an untrusted CA to be rejected")
	}
}

func TestCheckPrincipal_ShouldRejectWhenPrincipalsOmitTheAuthenticatingUser(t *testing.T) {
	cert := &ssh.Certificate{ValidPrincipals: []string{"bob", "carol"}}

	if err := checkPrincipal(cert, "alice"); err == nil {
		t.Fatal("expected rejection: alice is not among the certificate's principals")
	}
}

func TestCheckPrincipal_ShouldAcceptWhenPrincipalsIncludeTheAuthenticatingUser(t *testing.T) {
	cert := &ssh.Certificate{ValidPrincipals: []string{"bob", "alice"}}

	if err := checkPrincipal(cert, "alice"); err != nil {
		t.Errorf("expected acceptance, got %v", err)
	}
}

func TestCheckValidityWindow(t *testing.T) {
	now := time.Now()
	tolerance := 2 * time.Second

	tests := []struct {
		name        string
		validAfter  time.Time
		validBefore time.Time
		wantErr     bool
	}{
		{
			name:        "should accept a certificate inside its validity window",
			validAfter:  now.Add(-time.Minute),
			validBefore: now.Add(time.Minute),
			wantErr:     false,
		},
		{
			name:        "should reject a certificate not yet valid, outside tolerance",
			validAfter:  now.Add(5 * time.Second),
			validBefore: now.Add(time.Minute),
			wantErr:     true,
		},
		{
			name:        "should accept a certificate not yet valid but within tolerance",
			validAfter:  now.Add(time.Second),
			validBefore: now.Add(time.Minute),
			wantErr:     false,
		},
		{
			name:        "should reject an expired certificate, outside tolerance",
			validAfter:  now.Add(-time.Minute),
			validBefore: now.Add(-5 * time.Second),
			wantErr:     true,
		},
		{
			name:        "should accept an expired certificate within tolerance",
			validAfter:  now.Add(-time.Minute),
			validBefore: now.Add(-time.Second),
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cert := &ssh.Certificate{
				ValidAfter:  uint64(tt.validAfter.Unix()),  //nolint:gosec // test fixture, always a real date
				ValidBefore: uint64(tt.validBefore.Unix()), //nolint:gosec // test fixture, always a real date
			}
			err := checkValidityWindow(cert, now, tolerance)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkValidityWindow() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckValidityWindow_ShouldDescribeObservedSkewOnFailure(t *testing.T) {
	now := time.Now()
	cert := &ssh.Certificate{
		ValidAfter:  uint64(now.Add(10 * time.Second).Unix()), //nolint:gosec // test fixture, always a real date
		ValidBefore: uint64(now.Add(time.Minute).Unix()),      //nolint:gosec // test fixture, always a real date
	}

	err := checkValidityWindow(cert, now, 2*time.Second)
	if err == nil {
		t.Fatal("expected an error for a not-yet-valid certificate")
	}
	if !strings.Contains(err.Error(), "not yet valid") || !strings.Contains(err.Error(), "tolerance 2s") {
		t.Errorf("expected the error to describe the observed skew and configured tolerance, got %q", err.Error())
	}
}

func TestParseTrustedCAs(t *testing.T) {
	t.Run("should parse a single CA", func(t *testing.T) {
		ca := newTestCA(t)
		path := writeAuthorizedKeysFile(t, ca.publicKey())

		got, err := parseTrustedCAs(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d CAs, want 1", len(got))
		}
	})

	t.Run("should parse several CAs, one per line, for rotation", func(t *testing.T) {
		ca1 := newTestCA(t)
		ca2 := newTestCA(t)
		path := writeAuthorizedKeysFile(t, ca1.publicKey(), ca2.publicKey())

		got, err := parseTrustedCAs(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d CAs, want 2", len(got))
		}
	})

	t.Run("should error when the file does not exist", func(t *testing.T) {
		if _, err := parseTrustedCAs(filepath.Join(t.TempDir(), "missing.pub")); err == nil {
			t.Fatal("expected an error for a missing file")
		}
	})

	t.Run("should error when the file is empty", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.pub")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}
		if _, err := parseTrustedCAs(path); err == nil {
			t.Fatal("expected an error for an empty file")
		}
	})

	t.Run("should error when the file is not authorized_keys format", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "garbage.pub")
		if err := os.WriteFile(path, []byte("not a key\n"), 0o600); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}
		if _, err := parseTrustedCAs(path); err == nil {
			t.Fatal("expected an error for unparseable content")
		}
	})
}

// writeAuthorizedKeysFile writes keys, one per line in authorized_keys
// format, to a temp file and returns its path.
func writeAuthorizedKeysFile(t *testing.T, keys ...ssh.PublicKey) string {
	t.Helper()

	var buf []byte
	for _, k := range keys {
		buf = append(buf, ssh.MarshalAuthorizedKey(k)...)
	}
	path := filepath.Join(t.TempDir(), "cas.pub")
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}
	return path
}
