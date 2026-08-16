package keypair

import (
	"testing"
)

// should reject malformed or mistyped PEM input
func TestLoadECDSAKeyPair_RejectsInvalidInput(t *testing.T) {
	t.Parallel()

	other, err := NewRSAKeyPair(2048)
	if err != nil {
		t.Fatalf("NewRSAKeyPair() error = %v", err)
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
		{name: "should reject a PEM block with malformed EC bytes", in: []byte("-----BEGIN EC PRIVATE KEY-----\nAAAA\n-----END EC PRIVATE KEY-----\n")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := LoadECDSAKeyPair(tt.in); err == nil {
				t.Error("LoadECDSAKeyPair() error = nil, want error")
			}
		})
	}
}
