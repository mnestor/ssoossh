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
		return nil, err // excluded from coverage: crypto/rand.Reader failure isn't reproducible in tests, see exclude-from-coverage.txt
	}
	return &SSHKeypair{
		privateKey: priv,
		publicKey:  &priv.PublicKey,
	}, nil
}
