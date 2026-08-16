package keypair

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
)

// NewECDSAKeyPair generates a new ECDSA SSH keypair on the NIST curve
// matching bits: 256 (P-256), 384 (P-384), or 521 (P-521).
func NewECDSAKeyPair(bits int) (*SSHKeypair, error) {
	curve, err := ecdsaCurve(bits)
	if err != nil {
		return nil, err
	}
	priv, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		return nil, err // excluded from coverage: crypto/rand.Reader failure isn't reproducible in tests, see exclude-from-coverage.txt
	}
	return &SSHKeypair{
		privateKey: priv,
		publicKey:  &priv.PublicKey,
	}, nil
}

// LoadECDSAKeyPair loads an ECDSA keypair from PEM-encoded ("EC PRIVATE
// KEY") private key bytes.
func LoadECDSAKeyPair(privPEM []byte) (*SSHKeypair, error) {
	block, _ := pem.Decode(privPEM)
	if block == nil || block.Type != "EC PRIVATE KEY" {
		return nil, errors.New("invalid PEM block for EC private key")
	}
	priv, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	return &SSHKeypair{
		privateKey: priv,
		publicKey:  &priv.PublicKey,
	}, nil
}

// ecdsaCurve maps a bit size to the corresponding NIST curve used by SSH's
// ecdsa-sha2-nistp{256,384,521} key types.
func ecdsaCurve(bits int) (elliptic.Curve, error) {
	switch bits {
	case 256:
		return elliptic.P256(), nil
	case 384:
		return elliptic.P384(), nil
	case 521:
		return elliptic.P521(), nil
	default:
		return nil, errors.New("unsupported ECDSA curve size, must be 256, 384, or 521")
	}
}
