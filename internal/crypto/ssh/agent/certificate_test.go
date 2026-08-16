package agent

import (
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"golang.org/x/crypto/ssh"
)

// should parse a CA public key from either authorized_keys format or raw base64, and reject anything else
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

// should validate certificate time bounds and CA trust independently
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

	now := uint64(time.Now().Unix())

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
