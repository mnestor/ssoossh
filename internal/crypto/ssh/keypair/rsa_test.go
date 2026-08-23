package keypair

import (
	"testing"
)

// should reject a key size below the 2048-bit minimum
func TestNewRSAKeyPair_RejectsSmallKeySize(t *testing.T) {
	t.Parallel()

	if _, err := NewRSAKeyPair(1024); err == nil {
		t.Error("NewRSAKeyPair(1024) error = nil, want error")
	}
}
