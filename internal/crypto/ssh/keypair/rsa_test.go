package keypair

import (
	"testing"
)

// should reject a key size below 2048 bits, and generate a keypair otherwise
func TestNewRSAKeyPair_RejectsSmallKeySize(t *testing.T) {
	t.Parallel()

	if _, err := NewRSAKeyPair(1024); err == nil {
		t.Error("NewRSAKeyPair(1024) error = nil, want error for key size below 2048 bits")
	}
}

// should reject malformed or mistyped PEM input
func TestLoadRSAKeyPair_RejectsInvalidInput(t *testing.T) {
	t.Parallel()

	other, err := NewECDSAKeyPair(256)
	if err != nil {
		t.Fatalf("NewECDSAKeyPair() error = %v", err)
	}
	wrongTypePEM, err := other.MarshalPrivateKey()
	if err != nil {
		t.Fatalf("MarshalPrivateKey() error = %v", err)
	}

	tests := []struct {
		name string
		in   []byte
	}{
		{name: "should reject data that is not PEM-encoded", in: []byte("not pem data")},
		{name: "should reject a PEM block of the wrong type", in: wrongTypePEM},
		{name: "should reject a PEM block with malformed RSA bytes", in: []byte("-----BEGIN RSA PRIVATE KEY-----\nAAAA\n-----END RSA PRIVATE KEY-----\n")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := LoadRSAKeyPair(tt.in); err == nil {
				t.Error("LoadRSAKeyPair() error = nil, want error")
			}
		})
	}
}
