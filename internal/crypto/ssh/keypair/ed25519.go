// Created by Mike Nestor <me@mikenestor.org>

package keypair

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
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

// LoadEd25519KeyPair loads an Ed25519 keypair from a raw 32-byte private key
// seed (crypto/ed25519.SeedSize). For PEM-encoded Ed25519 keys (as produced
// by MarshalPrivateKey), use LoadSSHKeypair instead.
func LoadEd25519KeyPair(privBytes []byte) (*SSHKeypair, error) {
	if len(privBytes) != ed25519.SeedSize {
		return nil, errors.New("invalid private key size")
	}
	priv := ed25519.NewKeyFromSeed(privBytes)
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		// excluded from coverage: ed25519.PrivateKey.Public() always returns ed25519.PublicKey, see exclude-from-coverage.txt
		return nil, errors.New("ed25519 private key returned an unexpected public key type")
	}
	return &SSHKeypair{
		privateKey: priv,
		publicKey:  pub,
	}, nil
}
