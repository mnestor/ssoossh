package signer

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"fmt"

	"github.com/eclipse-keypont/crypto11"
	"golang.org/x/crypto/ssh"
)

// wrapCASigner converts an HSM-backed crypto.Signer into the ssh.Signer the
// pipeline signs certificates with, then applies the CA key algorithm policy
// shared with every other key source (gateCASigner).
//
// Ed25519 is rejected here rather than allowed as it is on the agent path:
// crypto11 cannot sign with an Ed25519 token key at all. In practice
// FindKeyPair rejects one first, with "unsupported key type: 40"; this is
// the belt to that braces. Use ssh_key, ssh_key_file or ssh_key_agent for
// an Ed25519 CA.
func wrapCASigner(s crypto.Signer) (ssh.Signer, error) {
	// Ed25519 has to be caught before NewSignerFromSigner, which would
	// happily wrap a key crypto11 cannot then use.
	if _, ok := s.Public().(ed25519.PublicKey); ok {
		return nil, fmt.Errorf("key type %T is not supported for HSM CA keys (ECDSA P-256/384/521 or RSA >= 2048; Ed25519 requires the ssh_key, ssh_key_file or ssh_key_agent source)", s.Public())
	}
	signer, err := ssh.NewSignerFromSigner(s)
	if err != nil {
		return nil, fmt.Errorf("wrap HSM CA key: %w", err)
	}
	return gateCASigner(signer, ed25519Rejected)
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
// not covered: the crypto11 calls below need a real PKCS#11 module, so no
// unit test exercises them. hsmkeysource_softhsm_test.go covers them
// against SoftHSM2 behind the softhsm build tag, which CI runs in
// .github/workflows/hsm.yaml and the plain unit profile does not; the pure
// logic (algorithm gating) is unit-tested via wrapCASigner.
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
