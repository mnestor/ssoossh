package keypair

import (
	"crypto/ed25519"
	"crypto/rand"
)

// NewEd25519KeyPair generates a new Ed25519 SSH keypair.
func NewEd25519KeyPair() (*SSHKeypair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		// not covered: rand.Reader is crypto/rand's, which crashes the
		// process rather than returning an error (Go 1.24+), so there is
		// no way to make GenerateKey fail here from a test.
		return nil, err
	}
	return &SSHKeypair{
		privateKey: priv,
		publicKey:  pub,
	}, nil
}
