//go:build hsm

// PKCS#11 CA key support, built only with -tags=hsm.
//
// This is the one file in ssoosshd that requires cgo: crypto11 binds
// libpkcs11 and dlopen()s a vendor module at runtime. Gating it behind a
// tag is what lets the default build be CGO_ENABLED=0 and fully static --
// no glibc/musl split, no libstdc++ in the runtime image, no C++ vendor
// library sharing an address space with the signer.
//
// The default build reaches an HSM through ssh-agent instead
// (AgentKeySource): `ssh-add -s <module>` puts the token behind an agent,
// and the module loads in the agent's process rather than this one. See
// docs/proposals/hsm-cloud-readiness.md.

package signer

import (
	"context"
	"fmt"

	"github.com/eclipse-keypont/crypto11"
	"golang.org/x/crypto/ssh"
)

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
