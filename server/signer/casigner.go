package signer

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// Ed25519 policy for gateCASigner, named rather than passed as a bare bool
// so call sites say which rule they are applying and why.
const (
	// ed25519Rejected is the PKCS#11 rule: crypto11 cannot sign with an
	// Ed25519 key, so a token holding one is a configuration error.
	ed25519Rejected = false
	// ed25519Allowed is the rule everywhere the signer is handed a working
	// ssh.Signer it did not build itself -- an ssh-agent, today. The agent
	// signs Ed25519 perfectly well, and it is the only way to hold an
	// Ed25519 CA key outside the config file.
	ed25519Allowed = true
)

// gateCASigner enforces the CA key algorithm policy every key source shares:
// ECDSA on P-256/384/521, RSA of at least 2048 bits, and nothing else.
//
// The RSA branch is the reason this exists rather than being an assertion.
// ssh.Certificate.SignCert uses MultiAlgorithmSigner.Algorithms()[0], and an
// unconstrained RSA signer offers ssh-rsa first -- which is SHA-1. Both key
// sources that reach this function default that way if left alone: crypto11
// wraps the raw key, and the agent's signer falls back to
// underlyingAlgo(pub.Type()). Restricting to the SHA-2 algorithms here is
// what keeps a SHA-1 signature off the wire.
func gateCASigner(s ssh.Signer, allowEd25519 bool) (ssh.Signer, error) {
	pubKey, err := cryptoPublicKey(s.PublicKey())
	if err != nil {
		return nil, err
	}

	switch pub := pubKey.(type) {
	case *ecdsa.PublicKey:
		switch pub.Curve {
		case elliptic.P256(), elliptic.P384(), elliptic.P521():
		default:
			return nil, fmt.Errorf("unsupported ECDSA curve %q for CA key", pub.Curve.Params().Name)
		}
		return s, nil

	case *rsa.PublicKey:
		if pub.N.BitLen() < 2048 {
			return nil, fmt.Errorf("CA RSA key is %d bits, must be at least 2048", pub.N.BitLen())
		}
		as, ok := s.(ssh.AlgorithmSigner)
		if !ok {
			return nil, fmt.Errorf("CA RSA signer does not support algorithm selection")
		}
		return ssh.NewSignerWithAlgorithms(as, []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256})

	default:
		if allowEd25519 && s.PublicKey().Type() == ssh.KeyAlgoED25519 {
			return s, nil
		}
		return nil, fmt.Errorf("key type %T is not supported for CA keys (ECDSA P-256/384/521 or RSA >= 2048)", pub)
	}
}

// cryptoPublicKey extracts the standard-library public key behind an
// ssh.PublicKey so the checks above can inspect curve and modulus size.
//
// The re-parse is not redundant. An ssh-agent's Signers() hands back
// *agent.Key, which implements ssh.PublicKey from a wire blob and does not
// implement ssh.CryptoPublicKey at all -- so a gate that only type-asserts
// would reject every agent-held key. Marshalling and re-parsing yields the
// concrete key type that does. Discovered by testing against a real
// in-process agent rather than a stand-in.
func cryptoPublicKey(pub ssh.PublicKey) (crypto.PublicKey, error) {
	if cpk, ok := pub.(ssh.CryptoPublicKey); ok {
		return cpk.CryptoPublicKey(), nil
	}
	parsed, err := ssh.ParsePublicKey(pub.Marshal())
	if err != nil {
		return nil, fmt.Errorf("parse CA public key of type %q: %w", pub.Type(), err)
	}
	cpk, ok := parsed.(ssh.CryptoPublicKey)
	if !ok {
		return nil, fmt.Errorf("CA key type %q does not expose a crypto public key, so it cannot be checked", pub.Type())
	}
	return cpk.CryptoPublicKey(), nil
}
