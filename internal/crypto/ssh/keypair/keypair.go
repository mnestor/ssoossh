// Created by Mike Nestor <me@mikenestor.org>

// Package keypair generates, loads, and marshals SSH keypairs (RSA, ECDSA,
// Ed25519) and their associated signed certificates. See the package README
// (README.md) for a full usage guide.
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

// Keypair is the generic interface satisfied by SSHKeypair, independent of
// the underlying algorithm (RSA, ECDSA, Ed25519). Callers that only need to
// hold, marshal, or sign with a keypair — e.g. internal/crypto/ssh/agent —
// should program against this interface rather than the concrete type.
type Keypair interface {
	// Private returns the raw private key (e.g. *rsa.PrivateKey,
	// *ecdsa.PrivateKey, ed25519.PrivateKey).
	Private() any
	// Public returns the SSH public key.
	Public() ssh.PublicKey
	// MarshalAuthorizedKey returns the public key in authorized_keys format.
	MarshalAuthorizedKey() (string, error)
	// MarshalPrivateKey returns the private key in PEM format.
	MarshalPrivateKey() ([]byte, error)
	// Certificate returns the associated SSH certificate, if any.
	Certificate() *ssh.Certificate
	// SetCertificate associates an SSH certificate with this keypair.
	SetCertificate(cert *ssh.Certificate)
	// SignedBy reports whether the associated certificate was signed by ca.
	SignedBy(ca ssh.PublicKey) bool
	// CertificateString returns the certificate in authorized_keys format.
	CertificateString() (string, error)
	// MarshalCertificate returns the certificate in authorized_keys format,
	// or nil if there is no certificate.
	MarshalCertificate() []byte
	// ParseCertificateFromString parses and sets the certificate from an
	// authorized_keys-format string.
	ParseCertificateFromString(certStr string) error
}

// SSHKeypair holds a private/public SSH keypair of any supported algorithm
// (RSA, ECDSA, Ed25519) plus an optional signed certificate. It implements Keypair.
type SSHKeypair struct {
	privateKey  crypto.PrivateKey
	publicKey   crypto.PublicKey
	certificate *ssh.Certificate
}

var _ Keypair = (*SSHKeypair)(nil)

// LoadSSHKeypair parses a PEM-encoded private key of unknown type — "RSA
// PRIVATE KEY" (PKCS#1), "EC PRIVATE KEY" (SEC1), "PRIVATE KEY" (PKCS#8, any
// of RSA/ECDSA/Ed25519), or "OPENSSH PRIVATE KEY" (any of
// RSA/ECDSA/Ed25519) — into an SSHKeypair. The public key is derived from
// the private key; no certificate is set.
func LoadSSHKeypair(data []byte) (*SSHKeypair, error) {
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
		return &SSHKeypair{
			privateKey: priv,
			publicKey:  &priv.PublicKey,
		}, nil
	case "EC PRIVATE KEY":
		priv, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse EC private key")
		}
		return &SSHKeypair{
			privateKey: priv,
			publicKey:  &priv.PublicKey,
		}, nil
	case "PRIVATE KEY":
		priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse PKCS8 private key")
		}
		return keypairFromPKCS8(priv)
	case "OPENSSH PRIVATE KEY":
		priv, err := ssh.ParseRawPrivateKey(data)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse OpenSSH private key")
		}
		return keypairFromOpenSSHRaw(priv)
	default:
		return nil, errors.Errorf("unsupported key type: %s", block.Type)
	}
}

// keypairFromPKCS8 builds an SSHKeypair from a key parsed by
// x509.ParsePKCS8PrivateKey (used by LoadSSHKeypair's "PRIVATE KEY" case).
func keypairFromPKCS8(priv any) (*SSHKeypair, error) {
	switch k := priv.(type) {
	case *rsa.PrivateKey:
		return &SSHKeypair{privateKey: k, publicKey: &k.PublicKey}, nil
	case ed25519.PrivateKey:
		pub, ok := k.Public().(ed25519.PublicKey)
		if !ok {
			// excluded from coverage: ed25519.PrivateKey.Public() always returns ed25519.PublicKey, see exclude-from-coverage.txt
			return nil, errors.New("ed25519 private key returned an unexpected public key type")
		}
		return &SSHKeypair{privateKey: k, publicKey: pub}, nil
	case *ecdsa.PrivateKey:
		return &SSHKeypair{privateKey: k, publicKey: &k.PublicKey}, nil
	default:
		return nil, errors.Errorf("unsupported PKCS8 private key type: %T", k)
	}
}

// keypairFromOpenSSHRaw builds an SSHKeypair from a key parsed by
// ssh.ParseRawPrivateKey (used by LoadSSHKeypair's "OPENSSH PRIVATE KEY"
// case).
func keypairFromOpenSSHRaw(priv any) (*SSHKeypair, error) {
	switch k := priv.(type) {
	case *ed25519.PrivateKey:
		pub, ok := k.Public().(ed25519.PublicKey)
		if !ok {
			// excluded from coverage: ed25519.PrivateKey.Public() always returns ed25519.PublicKey, see exclude-from-coverage.txt
			return nil, errors.New("ed25519 private key returned an unexpected public key type")
		}
		return &SSHKeypair{privateKey: *k, publicKey: pub}, nil
	case *rsa.PrivateKey:
		return &SSHKeypair{privateKey: k, publicKey: &k.PublicKey}, nil
	case *ecdsa.PrivateKey:
		return &SSHKeypair{privateKey: k, publicKey: &k.PublicKey}, nil
	default:
		return nil, errors.Errorf("unsupported OpenSSH private key type: %T", k)
	}
}

// NewSSHKeypair generates a new SSHKeypair based on the config KeyType and KeySize.
// keySize is bits for "rsa" (minimum 2048) and the curve size (256, 384, or
// 521) for "ecdsa"; it is ignored for "ed25519".
func NewSSHKeypair(keyType string, keySize int) (*SSHKeypair, error) {
	switch keyType {
	case "rsa":
		return NewRSAKeyPair(keySize)
	case "ecdsa":
		return NewECDSAKeyPair(keySize)
	case "ed25519":
		return NewEd25519KeyPair()
	default:
		return nil, errors.Errorf("unsupported key type: %s", keyType)
	}
}

// Private returns the raw private key: one of *rsa.PrivateKey,
// *ecdsa.PrivateKey, or ed25519.PrivateKey.
func (k *SSHKeypair) Private() any {
	return k.privateKey
}

// Public returns the SSH public key derived from the private key.
func (k *SSHKeypair) Public() ssh.PublicKey {
	// k.publicKey is always one of the types ssh.NewPublicKey accepts —
	// every constructor validates this before storing it — so this can't
	// actually fail. Kept error-free to match the Keypair interface.
	key, _ := ssh.NewPublicKey(k.publicKey) //nolint:errcheck // see comment above
	return key
}

// MarshalAuthorizedKey returns the public key in authorized_keys format.
func (k *SSHKeypair) MarshalAuthorizedKey() (string, error) {
	pub, err := ssh.NewPublicKey(k.publicKey)
	if err != nil {
		return "", err // excluded from coverage: k.publicKey is always a type ssh.NewPublicKey accepts, every constructor validates this, see exclude-from-coverage.txt
	}
	return string(ssh.MarshalAuthorizedKey(pub)), nil
}

// Certificate returns the certificate previously set via SetCertificate or
// ParseCertificateFromString, or nil if none has been set.
func (k *SSHKeypair) Certificate() *ssh.Certificate {
	return k.certificate
}

// SetCertificate sets the SSH certificate for this keypair.
func (k *SSHKeypair) SetCertificate(cert *ssh.Certificate) {
	k.certificate = cert
}

// SignedBy reports whether this keypair's certificate exists and was signed
// by ca. It returns false if there is no certificate.
func (k SSHKeypair) SignedBy(ca ssh.PublicKey) bool {
	if k.certificate == nil {
		return false
	}
	if k.certificate.SignatureKey == nil {
		return false
	}
	return bytes.Equal(k.certificate.SignatureKey.Marshal(), ca.Marshal())
}

// CertificateString returns the SSH certificate in authorized_keys format, if present.
func (k *SSHKeypair) CertificateString() (string, error) {
	if k.certificate == nil {
		return "", errors.New("no certificate set")
	}
	return string(ssh.MarshalAuthorizedKey(k.certificate)), nil
}

// MarshalCertificate returns the SSH certificate in authorized_keys format
// as a byte slice, or nil if there is no certificate.
func (k *SSHKeypair) MarshalCertificate() []byte {
	if k.certificate == nil {
		return nil
	}
	return ssh.MarshalAuthorizedKey(k.certificate)
}

// ParseCertificateFromString parses an SSH certificate from an authorized_keys string and sets it.
func (k *SSHKeypair) ParseCertificateFromString(certStr string) error {
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

// MarshalPrivateKey returns the private key PEM-encoded: "RSA PRIVATE KEY"
// (PKCS#1) for RSA, "EC PRIVATE KEY" (SEC1) for ECDSA, or an
// OpenSSH-format "OPENSSH PRIVATE KEY" block for Ed25519 (which has no
// standard PKCS#1/SEC1-style PEM encoding). Use LoadSSHKeypair to read it
// back.
func (k *SSHKeypair) MarshalPrivateKey() ([]byte, error) {
	var block *pem.Block
	switch priv := k.privateKey.(type) {
	case *rsa.PrivateKey:
		privBytes := x509.MarshalPKCS1PrivateKey(priv)
		block = &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: privBytes,
		}
	case *ecdsa.PrivateKey:
		privBytes, err := x509.MarshalECPrivateKey(priv)
		if err != nil {
			return nil, err // excluded from coverage: priv was just generated or parsed by this package, marshaling it back can't fail, see exclude-from-coverage.txt
		}
		block = &pem.Block{
			Type:  "EC PRIVATE KEY",
			Bytes: privBytes,
		}
	case ed25519.PrivateKey:
		// For Ed25519, we use the OpenSSH format
		// Note: Ed25519 keys are not PEM encoded in the same way as RSA keys.
		var err error
		block, err = ssh.MarshalPrivateKey(crypto.PrivateKey(k.privateKey), "ssoossh")
		if err != nil {
			return nil, err // excluded from coverage: k.privateKey was just generated or parsed by this package, marshaling it back can't fail, see exclude-from-coverage.txt
		}
	default:
		return nil, errors.New("unsupported private key type for PEM encoding")
	}
	return pem.EncodeToMemory(block), nil
}
