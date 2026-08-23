package keypair

import (
	"crypto/rand"
	"crypto/rsa"
	"errors"
)

// NewRSAKeyPair generates a new RSA SSH keypair of the given bit size.
func NewRSAKeyPair(bits int) (*SSHKeypair, error) {
	if bits < 2048 {
		return nil, errors.New("key size too small, must be at least 2048 bits")
	}
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		// not covered: rand.Reader is crypto/rand's, which crashes the
		// process rather than returning an error (Go 1.24+), and the
		// 2048-bit floor above rules out the no-primes-found failure.
		return nil, err
	}
	return &SSHKeypair{
		privateKey: priv,
		publicKey:  &priv.PublicKey,
	}, nil
}
