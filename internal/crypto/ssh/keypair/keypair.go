// Created by Mike Nestor <me@mikenestor.org>

package keypair

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"

	"github.com/pkg/errors"
	"golang.org/x/crypto/ssh"
)

type SshKeypair struct {
	privateKey  crypto.PrivateKey
	publicKey   crypto.PublicKey
	certificate *ssh.Certificate
}

// LoadSshKeypairFromFile loads an unknown SSH key type from a file into the SshKeypair interface.
func LoadSshKeypair(data []byte) (*SshKeypair, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("failed to decode PEM block from file")
	}

	switch block.Type {
	case "RSA PRIVATE KEY":
		priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse RSA private key")
		}
		return &SshKeypair{
			privateKey: priv,
			publicKey:  &priv.PublicKey,
		}, nil
	case "PRIVATE KEY":
		priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse PKCS8 private key")
		}
		switch k := priv.(type) {
		case *rsa.PrivateKey:
			return &SshKeypair{
				privateKey: k,
				publicKey:  &k.PublicKey,
			}, nil
		case ed25519.PrivateKey:
			pub := k.Public().(ed25519.PublicKey)
			return &SshKeypair{
				privateKey: k,
				publicKey:  pub,
			}, nil
		case *ecdsa.PrivateKey:
			// Add ECDSAKeyPair if implemented
			return nil, errors.New("ECDSA key support not implemented")
		default:
			return nil, errors.Errorf("unsupported PKCS8 private key type: %T", k)
		}
	case "OPENSSH PRIVATE KEY":
		// Try to parse as OpenSSH private key (ed25519)
		// For simplicity, treat as Ed25519 seed if length matches
		if len(block.Bytes) == ed25519.SeedSize {
			priv := ed25519.NewKeyFromSeed(block.Bytes)
			pub := priv.Public().(ed25519.PublicKey)
			return &SshKeypair{
				privateKey: priv,
				publicKey:  pub,
			}, nil
		}
		return nil, errors.New("unsupported or invalid OPENSSH PRIVATE KEY format")
	default:
		return nil, errors.Errorf("unsupported key type: %s", block.Type)
	}
}

// NewSshKeypairFromConfig generates a new SshKeypair based on the config KeyType and KeySize.
func NewSshKeypair(keyType string, keySize int) (*SshKeypair, error) {
	switch keyType {
	case "rsa":
		return NewRSAKeyPair(keySize)
	case "ed25519":
		return NewEd25519KeyPair()
	default:
		return nil, errors.Errorf("unsupported key type: %s", keyType)
	}
}

func (k *SshKeypair) Private() interface{} {
	return k.privateKey
}

func (k *SshKeypair) Public() ssh.PublicKey {
	key, _ := ssh.NewPublicKey(k.publicKey)
	return key
}

// PublicKeySSH returns the public key in SSH authorized_keys format.
func (k *SshKeypair) MarshalAuthorizedKey() (string, error) {
	pub, err := ssh.NewPublicKey(k.publicKey)
	if err != nil {
		return "", err
	}
	return string(ssh.MarshalAuthorizedKey(pub)), nil
}

func (k *SshKeypair) Certificate() *ssh.Certificate {
	return k.certificate
}

// SetCertificate sets the SSH certificate for this keypair.
func (k *SshKeypair) SetCertificate(cert *ssh.Certificate) {
	k.certificate = cert
}

func (k SshKeypair) SignedBy(ca ssh.PublicKey) bool {
	if k.certificate == nil {
		return false
	}
	if k.certificate.SignatureKey == nil {
		return false
	}
	return bytes.Equal(k.certificate.SignatureKey.Marshal(), ca.Marshal())
}

// CertificateString returns the SSH certificate in authorized_keys format, if present.
func (k *SshKeypair) CertificateString() (string, error) {
	if k.certificate == nil {
		return "", errors.New("no certificate set")
	}
	return string(ssh.MarshalAuthorizedKey(k.certificate)), nil
}

// CertificateString returns the SSH certificate in authorized_keys format, if present.
func (k *SshKeypair) MarshalCertificate() []byte {
	if k.certificate == nil {
		return nil
	}
	return ssh.MarshalAuthorizedKey(k.certificate)
}

// ParseCertificateFromString parses an SSH certificate from an authorized_keys string and sets it.
func (k *SshKeypair) ParseCertificateFromString(certStr string) error {
	pub, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(certStr))
	if err != nil || len(rest) > 0 {
		return errors.New("failed to parse certificate string")
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		return errors.New("provided string is not an SSH certificate")
	}
	k.certificate = cert
	return nil
}

// PrivateKeyPEM returns the private key in PEM format.
func (k *SshKeypair) MarshalPrivateKey() ([]byte, error) {
	var block *pem.Block
	switch k.privateKey.(type) {
	case *rsa.PrivateKey:
		privBytes := x509.MarshalPKCS1PrivateKey(k.privateKey.(*rsa.PrivateKey))
		block = &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: privBytes,
		}
		// return pem.EncodeToMemory(block), nil
	case ed25519.PrivateKey:
		// For Ed25519, we use the OpenSSH format
		// Note: Ed25519 keys are not PEM encoded in the same way as RSA keys.
		var err error
		block, err = ssh.MarshalPrivateKey(crypto.PrivateKey(k.privateKey), "ssoossh")
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("unsupported private key type for PEM encoding")
	}
	return pem.EncodeToMemory(block), nil
}
