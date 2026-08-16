package keypair

import (
	"crypto/ed25519"
	"testing"
)

// should build a keypair from a raw 32-byte seed and reject any other length
func TestLoadEd25519KeyPair(t *testing.T) {
	t.Parallel()

	t.Run("should load a valid seed", func(t *testing.T) {
		t.Parallel()
		seed := make([]byte, ed25519.SeedSize)
		kp, err := LoadEd25519KeyPair(seed)
		if err != nil {
			t.Fatalf("LoadEd25519KeyPair() error = %v", err)
		}
		if kp.Public() == nil {
			t.Error("LoadEd25519KeyPair() returned a keypair with no public key")
		}
	})

	t.Run("should reject a seed of the wrong length", func(t *testing.T) {
		t.Parallel()
		if _, err := LoadEd25519KeyPair([]byte("too short")); err == nil {
			t.Error("LoadEd25519KeyPair() error = nil, want error for invalid seed length")
		}
	})
}
