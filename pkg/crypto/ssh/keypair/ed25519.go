// Created by Mike Nestor <me@mikenestor.org>

package keypair

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
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
func NewEd25519KeyPair() (SshKeypair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Ed25519KeyPair{
		PrivateKey: priv,
		PublicKey:  pub,
	}, nil
}

func (k *Ed25519KeyPair) Private() crypto.PublicKey {
	return k.PrivateKey
}

// PublicKeySSH returns the public key in SSH authorized_keys format.
func (k *Ed25519KeyPair) PublicKeySSH() (string, error) {
	pub, err := ssh.NewPublicKey(k.PublicKey)
	if err != nil {
		return "", err
	}
	return string(ssh.MarshalAuthorizedKey(pub)), nil
}

// PrivateKeyPEM returns the private key in PEM format.
func (k *Ed25519KeyPair) PrivateKeyPEM() ([]byte, error) {
	p, e := ssh.MarshalPrivateKey(crypto.PrivateKey(k.PrivateKey), "ssoossh")
	if e != nil {
		return nil, e
	}
	return pem.EncodeToMemory(p), nil
}

// LoadEd25519KeyPair loads an Ed25519KeyPair from private key bytes.
func LoadEd25519KeyPair(privBytes []byte) (SshKeypair, error) {
	if len(privBytes) != ed25519.SeedSize {
		return nil, errors.New("invalid private key size")
	}
	priv := ed25519.NewKeyFromSeed(privBytes)
	pub := priv.Public().(ed25519.PublicKey)
	return &Ed25519KeyPair{
		PrivateKey: priv,
		PublicKey:  pub,
	}, nil
}

// SetCertificate sets the SSH certificate for this keypair.
func (k *Ed25519KeyPair) SetCertificate(cert *ssh.Certificate) {
	k._Certificate = cert
}

// CertificateString returns the SSH certificate in authorized_keys format, if present.
func (k *Ed25519KeyPair) CertificateString() (string, error) {
	if k._Certificate == nil {
		return "", errors.New("no certificate set")
	}
	return string(ssh.MarshalAuthorizedKey(k._Certificate)), nil
}

// CertificateString returns the SSH certificate in authorized_keys format, if present.
func (k *Ed25519KeyPair) MarshalCertificate() []byte {
	if k._Certificate == nil {
		return nil
	}
	return ssh.MarshalAuthorizedKey(k._Certificate)
}

// ParseCertificateFromString parses an SSH certificate from an authorized_keys string and sets it.
func (k *Ed25519KeyPair) ParseCertificateFromString(certStr string) error {
	pub, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(certStr))
	if err != nil || len(rest) > 0 {
		return errors.New("failed to parse certificate string")
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return errors.New("provided string is not an SSH certificate")
	}
	k._Certificate = cert
	return nil
}

func (k *Ed25519KeyPair) Certificate() *ssh.Certificate {
	return k._Certificate
}
