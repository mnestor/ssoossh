//go:build hsm

package bootstrap

import (
	"context"

	"github.com/mnestor/ssoossh/server/signer"
)

// newHSMCAKeySource builds the PKCS#11-backed CA key source. Built only with
// -tags=hsm, which is also the only configuration that needs cgo.
func (a *app) newHSMCAKeySource() (signer.CAKeySource, error) {
	pin, err := a.config.Signer.HSM.ResolvePIN()
	if err != nil {
		return nil, err
	}
	keyID, err := a.config.Signer.HSM.KeyIDBytes()
	if err != nil {
		return nil, err
	}
	ks, err := signer.NewHSMKeySource(signer.HSMParams{
		Module:     a.config.Signer.HSM.Module,
		TokenLabel: a.config.Signer.HSM.TokenLabel,
		PIN:        pin,
		KeyID:      keyID,
		KeyLabel:   a.config.Signer.HSM.KeyLabel,
	})
	if err != nil {
		return nil, err
	}
	a.caKeySource = ks
	// Wrap ks.Close to match servicerunner.Service signature (accepts
	// context, returns error).
	a.closeCAKeySource = func(context.Context) error { return ks.Close() }
	return ks, nil
}
