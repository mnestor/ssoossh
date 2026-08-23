package signer

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// wrapCASigner converts an HSM-backed crypto.Signer into the ssh.Signer the
// pipeline signs certificates with. It gates algorithms to what the HSM path
// supports: ECDSA P-256/384/521 and RSA >= 2048 bits. RSA signers are
// restricted to rsa-sha2-512/256 — ssh.Certificate.SignCert uses
// MultiAlgorithmSigner.Algorithms()[0], and an unrestricted RSA signer would
// produce legacy SHA-1 ssh-rsa signatures. Ed25519 is rejected: the Go
// PKCS#11 stack (crypto11) cannot sign with it; use the ssh_key PEM source
// for Ed25519 CAs.
func wrapCASigner(s crypto.Signer) (ssh.Signer, error) {
	switch pub := s.Public().(type) {
	case *ecdsa.PublicKey:
		switch pub.Curve {
		case elliptic.P256(), elliptic.P384(), elliptic.P521():
		default:
			return nil, fmt.Errorf("unsupported ECDSA curve %q for HSM CA key", pub.Curve.Params().Name)
		}
		return ssh.NewSignerFromSigner(s)
	case *rsa.PublicKey:
		if pub.N.BitLen() < 2048 {
			return nil, fmt.Errorf("HSM CA RSA key is %d bits, must be at least 2048", pub.N.BitLen())
		}
		signer, err := ssh.NewSignerFromSigner(s)
		if err != nil {
			return nil, fmt.Errorf("wrap HSM RSA key: %w", err)
		}
		as, ok := signer.(ssh.AlgorithmSigner)
		if !ok {
			return nil, fmt.Errorf("HSM RSA signer does not support algorithm selection")
		}
		return ssh.NewSignerWithAlgorithms(as, []string{ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256})
	default:
		return nil, fmt.Errorf("key type %T is not supported for HSM CA keys (ECDSA P-256/384/521 or RSA >= 2048; Ed25519 requires the ssh_key source)", pub)
	}
}
