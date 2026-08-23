package signer

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"fmt"

	"github.com/eclipse-keypont/crypto11"
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

// HSMParams configures a connection to a PKCS#11 token.
type HSMParams struct {
	Module     string // path to PKCS#11 .so
	TokenLabel string
	PIN        string
	KeyID      []byte // nil when selecting by label only
	KeyLabel   string // "" when selecting by id only
}

// HSMKeySource is a CAKeySource whose private key lives in a PKCS#11 token.
// The key never enters process memory: crypto11 hands back a crypto.Signer
// that performs each signature inside the HSM. Construction connects, logs
// in, and locates the key so a misconfigured HSM fails at boot, matching
// ConfigKeySource's fail-at-startup behavior. Close releases the PKCS#11
// context; bootstrap runs it on shutdown.
type HSMKeySource struct {
	signer ssh.Signer
	ctx11  *crypto11.Context
}

// NewHSMKeySource opens the PKCS#11 module and resolves the CA key pair.
//
// No unit test exercises the crypto11 calls below — they require a real
// PKCS#11 module. hsmkeysource_softhsm_test.go covers them against SoftHSM2
// behind the softhsm build tag (run in CI by .github/workflows/hsm.yaml);
// the pure logic (algorithm gating) is unit-tested via wrapCASigner. The
// crypto11 call lines are listed in exclude-from-coverage.txt.
func NewHSMKeySource(p HSMParams) (*HSMKeySource, error) {
	ctx11, err := crypto11.Configure(&crypto11.Config{
		Path:       p.Module,
		TokenLabel: p.TokenLabel,
		Pin:        p.PIN,
	})
	if err != nil {
		return nil, fmt.Errorf("open PKCS#11 module %s: %w", p.Module, err)
	}
	var keyLabel []byte
	if p.KeyLabel != "" {
		keyLabel = []byte(p.KeyLabel)
	}
	kp, err := ctx11.FindKeyPair(p.KeyID, keyLabel)
	if err != nil {
		_ = ctx11.Close()
		return nil, fmt.Errorf("find CA key pair in HSM: %w", err)
	}
	if kp == nil { // crypto11 returns nil, nil when nothing matches
		_ = ctx11.Close()
		return nil, fmt.Errorf("no key pair found in HSM token %q matching label %q / id %x", p.TokenLabel, p.KeyLabel, p.KeyID)
	}
	signer, err := wrapCASigner(kp)
	if err != nil {
		_ = ctx11.Close()
		return nil, err
	}
	return &HSMKeySource{signer: signer, ctx11: ctx11}, nil
}

// Signer implements CAKeySource.
func (s *HSMKeySource) Signer(context.Context) (ssh.Signer, error) {
	return s.signer, nil
}

// Close releases the PKCS#11 sessions and unloads the module.
func (s *HSMKeySource) Close() error {
	return s.ctx11.Close()
}
