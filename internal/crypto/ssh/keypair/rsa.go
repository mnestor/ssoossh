package keypair

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
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

// LoadRSAKeyPair loads an RSA keypair from PEM-encoded ("RSA PRIVATE KEY") private key bytes.
func LoadRSAKeyPair(privPEM []byte) (*SSHKeypair, error) {
	block, _ := pem.Decode(privPEM)
	if block == nil || block.Type != "RSA PRIVATE KEY" {
		return nil, errors.New("invalid PEM block for RSA private key")
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return &SSHKeypair{
		privateKey: priv,
		publicKey:  &priv.PublicKey,
	}, nil
}
