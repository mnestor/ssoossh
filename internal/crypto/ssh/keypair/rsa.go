package keypair

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"

	"golang.org/x/crypto/ssh"
)

// rsaKeyPair manages an RSA SSH keypair.
type rsaKeyPair struct {
	PrivateKey   *rsa.PrivateKey
	PublicKey    *rsa.PublicKey
	_Certificate *ssh.Certificate
}

// NewRSAKeyPair generates a new RSA SSH keypair of the given bit size.
func NewRSAKeyPair(bits int) (*SshKeypair, error) {
	if bits < 2048 {
		return nil, errors.New("key size too small, must be at least 2048 bits")
	}
	priv, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, err
	}
	return &SshKeypair{
		privateKey: priv,
		publicKey:  &priv.PublicKey,
	}, nil
}

// LoadrsaKeyPair loads an rsaKeyPair from PEM-encoded private key bytes.
func LoadRSAKeyPair(privPEM []byte) (*SshKeypair, error) {
	block, _ := pem.Decode(privPEM)
	if block == nil || block.Type != "RSA PRIVATE KEY" {
		return nil, errors.New("invalid PEM block for RSA private key")
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return &SshKeypair{
		privateKey: priv,
		publicKey:  &priv.PublicKey,
	}, nil
}
