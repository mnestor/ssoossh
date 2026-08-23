package agent

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// should parse a CA public key from either authorized_keys format or raw base64, and reject anything else.
func TestParseCAPublicKey(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	authorizedKey, err := ca.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("MarshalAuthorizedKey() error = %v", err)
	}
	rawBase64 := base64.StdEncoding.EncodeToString(ca.Public().Marshal())

	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{name: "should parse an authorized_keys-format string", in: authorizedKey, wantErr: false},
		{name: "should parse an authorized_keys-format string with surrounding whitespace", in: "  " + authorizedKey + "\n", wantErr: false},
		{name: "should fall back to raw base64-encoded key bytes", in: rawBase64, wantErr: false},
		{name: "should reject an empty string", in: "", wantErr: true},
		{name: "should reject whitespace-only input", in: "   ", wantErr: true},
		{name: "should reject a string that is neither authorized_keys format nor valid base64", in: "not a key", wantErr: true},
		{name: "should reject valid base64 that does not decode to a public key", in: "aGVsbG8gd29ybGQ=", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := parseCAPublicKey(tt.in)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseCAPublicKey(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
		})
	}
}

// should parse every key in a multi-line CA string, as served by the
// server's /api/ca endpoint (one authorized_keys-format key per line).
func TestParseCAPublicKeys(t *testing.T) {
	t.Parallel()

	caA, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caB, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	keyA, err := caA.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("MarshalAuthorizedKey() error = %v", err)
	}
	keyB, err := caB.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("MarshalAuthorizedKey() error = %v", err)
	}
	rawBase64 := base64.StdEncoding.EncodeToString(caA.Public().Marshal())

	tests := []struct {
		name     string
		in       []string
		wantKeys int
		wantErr  bool
	}{
		{name: "should parse a single-key string", in: []string{keyA}, wantKeys: 1},
		{name: "should parse two keys joined by a newline in one string", in: []string{keyA + "\n" + keyB}, wantKeys: 2},
		{name: "should skip blank lines between keys", in: []string{keyA + "\n\n" + keyB + "\n"}, wantKeys: 2},
		{name: "should parse keys split across separate strings", in: []string{keyA, keyB}, wantKeys: 2},
		{name: "should fall back to raw base64 per line", in: []string{rawBase64 + "\n" + keyB}, wantKeys: 2},
		{name: "should reject a string of only blank lines", in: []string{"\n\n"}, wantErr: true},
		{name: "should reject when any line is unparseable", in: []string{keyA + "\nnot a key"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseCAPublicKeys(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseCAPublicKeys(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if !tt.wantErr && len(got) != tt.wantKeys {
				t.Errorf("parseCAPublicKeys(%q) returned %d keys, want %d", tt.in, len(got), tt.wantKeys)
			}
		})
	}
}

// should validate certificate time bounds and CA trust independently.
func TestCertificateValid(t *testing.T) {
	t.Parallel()

	trustedCA, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	untrustedCA, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	trustedSigner, err := ssh.NewSignerFromKey(trustedCA.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}

	sign := func(t *testing.T, validAfter, validBefore uint64) *ssh.Certificate {
		t.Helper()
		cert := &ssh.Certificate{
			Key:         leaf.Public(),
			CertType:    ssh.UserCert,
			ValidAfter:  validAfter,
			ValidBefore: validBefore,
		}
		if err := cert.SignCert(rand.Reader, trustedSigner); err != nil {
			t.Fatalf("SignCert() error = %v", err)
		}
		return cert
	}

	now := uint64(time.Now().Unix()) //nolint:gosec // test fixture, always a real date

	tests := []struct {
		name string
		cert *ssh.Certificate
		cas  []ssh.PublicKey
		want bool
	}{
		{name: "should reject a nil certificate", cert: nil, cas: []ssh.PublicKey{trustedCA.Public()}, want: false},
		{
			name: "should reject a certificate with no SignatureKey (never signed)",
			cert: &ssh.Certificate{Key: leaf.Public(), CertType: ssh.UserCert, ValidAfter: now - 3600, ValidBefore: now + 3600},
			cas:  []ssh.PublicKey{trustedCA.Public()},
			want: false,
		},
		{
			name: "should reject a certificate with ValidBefore == 0",
			cert: sign(t, now-3600, 0),
			cas:  []ssh.PublicKey{trustedCA.Public()},
			want: false,
		},
		{
			name: "should reject a certificate with ValidAfter == 0",
			cert: sign(t, 0, now+3600),
			cas:  []ssh.PublicKey{trustedCA.Public()},
			want: false,
		},
		{
			name: "should reject a certificate where ValidBefore precedes ValidAfter",
			cert: sign(t, now+3600, now-3600),
			cas:  []ssh.PublicKey{trustedCA.Public()},
			want: false,
		},
		{
			name: "should reject an expired certificate",
			cert: sign(t, now-7200, now-3600),
			cas:  []ssh.PublicKey{trustedCA.Public()},
			want: false,
		},
		{
			name: "should reject a certificate signed by an untrusted CA",
			cert: sign(t, now-3600, now+3600),
			cas:  []ssh.PublicKey{untrustedCA.Public()},
			want: false,
		},
		{
			name: "should reject a certificate when no CAs are registered",
			cert: sign(t, now-3600, now+3600),
			cas:  nil,
			want: false,
		},
		{
			name: "should accept a time-valid certificate signed by a trusted CA",
			cert: sign(t, now-3600, now+3600),
			cas:  []ssh.PublicKey{untrustedCA.Public(), trustedCA.Public()},
			want: true,
		},
		{
			// Regression test: SignatureKey alone is not proof of trust — it
			// is a field on the certificate an attacker fully controls, not
			// something the CA vouches for. A certificate genuinely signed
			// by untrustedCA, with SignatureKey then overwritten to claim
			// trustedCA, must still be rejected: Signature was never
			// produced by trustedCA's private key.
			name: "should reject a certificate whose SignatureKey was swapped to a trusted CA without re-signing",
			cert: func() *ssh.Certificate {
				untrustedSigner, err := ssh.NewSignerFromKey(untrustedCA.Private())
				if err != nil {
					t.Fatalf("NewSignerFromKey() error = %v", err)
				}
				cert := &ssh.Certificate{
					Key:         leaf.Public(),
					CertType:    ssh.UserCert,
					ValidAfter:  now - 3600,
					ValidBefore: now + 3600,
				}
				if err := cert.SignCert(rand.Reader, untrustedSigner); err != nil {
					t.Fatalf("SignCert() error = %v", err)
				}
				cert.SignatureKey = trustedCA.Public()
				return cert
			}(),
			cas:  []ssh.PublicKey{trustedCA.Public()},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := CertificateValid(tt.cert, tt.cas); got != tt.want {
				t.Errorf("CertificateValid() = %v, want %v", got, tt.want)
			}
		})
	}
}
