package keypair

import (
	"crypto/ed25519"
	"crypto/rand"
)

// NewEd25519KeyPair generates a new Ed25519 SSH keypair.
func NewEd25519KeyPair() (*SSHKeypair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err // excluded from coverage: crypto/rand.Reader failure isn't reproducible in tests, see exclude-from-coverage.txt
	}
	return &SSHKeypair{
		privateKey: priv,
		publicKey:  pub,
	}, nil
}
