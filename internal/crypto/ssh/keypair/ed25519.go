// Created by Mike Nestor <me@mikenestor.org>

package keypair

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"

	"golang.org/x/crypto/ssh"
)

// Ed25519KeyPair manages an Ed25519 SSH keypair.
type Ed25519KeyPair struct {
	PrivateKey   ed25519.PrivateKey
	PublicKey    ed25519.PublicKey
	_Certificate *ssh.Certificate
}

// NewEd25519KeyPair generates a new Ed25519 SSH keypair.
func NewEd25519KeyPair() (*SshKeypair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &SshKeypair{
		privateKey: priv,
		publicKey:  pub,
	}, nil
}

// LoadEd25519KeyPair loads an Ed25519KeyPair from private key bytes.
func LoadEd25519KeyPair(privBytes []byte) (*SshKeypair, error) {
	if len(privBytes) != ed25519.SeedSize {
		return nil, errors.New("invalid private key size")
	}
	priv := ed25519.NewKeyFromSeed(privBytes)
	pub := priv.Public().(ed25519.PublicKey)
	return &SshKeypair{
		privateKey: priv,
		publicKey:  pub,
	}, nil
}
