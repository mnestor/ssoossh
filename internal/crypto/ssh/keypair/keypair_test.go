package keypair

import (
	"testing"
)

// should satisfy the generic Keypair interface
func TestSSHKeypair_ImplementsKeypair(t *testing.T) {
	t.Parallel()

	var _ Keypair = (*SSHKeypair)(nil)
}

// should generate a keypair and round-trip its private key through PEM for every supported algorithm
func TestNewSSHKeypair_GenerateAndMarshalRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		keyType string
		keySize int
		load    func(pem []byte) (*SSHKeypair, error)
	}{
		{name: "should round-trip an RSA keypair", keyType: "rsa", keySize: 2048, load: LoadRSAKeyPair},
		{name: "should round-trip an ECDSA P-256 keypair", keyType: "ecdsa", keySize: 256, load: LoadECDSAKeyPair},
		{name: "should round-trip an ECDSA P-384 keypair", keyType: "ecdsa", keySize: 384, load: LoadECDSAKeyPair},
		{name: "should round-trip an ECDSA P-521 keypair", keyType: "ecdsa", keySize: 521, load: LoadECDSAKeyPair},
		{name: "should round-trip an Ed25519 keypair", keyType: "ed25519", keySize: 0, load: LoadSSHKeypair},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			kp, err := NewSSHKeypair(tt.keyType, tt.keySize)
			if err != nil {
				t.Fatalf("NewSSHKeypair(%q, %d) error = %v", tt.keyType, tt.keySize, err)
			}

			authKey, err := kp.MarshalAuthorizedKey()
			if err != nil {
				t.Fatalf("MarshalAuthorizedKey() error = %v", err)
			}
			if authKey == "" {
				t.Error("MarshalAuthorizedKey() returned empty string")
			}

			privPEM, err := kp.MarshalPrivateKey()
			if err != nil {
				t.Fatalf("MarshalPrivateKey() error = %v", err)
			}

			loaded, err := tt.load(privPEM)
			if err != nil {
				t.Fatalf("load(privPEM) error = %v", err)
			}

			gotKey, err := loaded.MarshalAuthorizedKey()
			if err != nil {
				t.Fatalf("loaded.MarshalAuthorizedKey() error = %v", err)
			}
			if gotKey != authKey {
				t.Errorf("round-tripped public key = %q, want %q", gotKey, authKey)
			}
		})
	}
}

// should reject unsupported ECDSA curve sizes
func TestNewECDSAKeyPair_RejectsUnsupportedBits(t *testing.T) {
	t.Parallel()

	if _, err := NewECDSAKeyPair(224); err == nil {
		t.Error("NewECDSAKeyPair(224) error = nil, want error for unsupported curve size")
	}
}

// should reject an unknown key type
func TestNewSSHKeypair_RejectsUnknownType(t *testing.T) {
	t.Parallel()

	if _, err := NewSSHKeypair("dsa", 1024); err == nil {
		t.Error(`NewSSHKeypair("dsa", 1024) error = nil, want error for unsupported key type`)
	}
}
