package bootstrap

import (
	"context"
	"fmt"

	"github.com/mnestor/ssoossh/internal/fipsmode"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/signer"
)

// initPipeline registers the certificate pipeline's queue consumers on the
// pub/sub Router: the signer (sign queue → signed replies) and the
// listener/resolver (signed replies → audit row, delivery, terminal status).
// See docs/signing-pipeline.md.
//
// Ordering: must run after initPubSub (needs the Router) and initServices
// (the listener needs certRequest). It's safe that a.pubSub.Run was already
// appended to serviceRunners by then — nothing in that slice actually starts
// until servicerunner runs it at the end of Bootstrap, so every handler is
// registered well before the Router begins consuming, and before the HTTP
// router can accept a request that publishes a job.
func (a *app) initPipeline() error {
	// Parsed here rather than reusing CAService's: that service deliberately
	// keeps only the public half. Failing startup on a bad key is the point —
	// a server that can't sign is misconfigured, and finding out at the first
	// approval instead of at boot would be worse.
	keys, err := signer.NewConfigKeySource(a.config.SSHKey)
	if err != nil {
		return fmt.Errorf("failed to load CA signing key: %w", err)
	}

	fipsEnabled := a.config.FIPSEnabled()
	if fipsEnabled {
		caSigner, err := keys.Signer(context.Background())
		if err != nil {
			return fmt.Errorf("failed to load CA signing key: %w", err) // excluded from coverage: keys is a *signer.ConfigKeySource constructed just above, whose Signer always returns the already-parsed key with a nil error, see exclude-from-coverage.txt
		}
		keyType, ok := fipsmode.FromSSHAlgorithm(caSigner.PublicKey().Type())
		if !ok || !fipsmode.IsApprovedInFIPS(keyType) {
			return fmt.Errorf("CA key algorithm %q is not FIPS-approved", caSigner.PublicKey().Type())
		}
	}

	signer.NewHandler(keys, a.pubSub.Publisher, fipsEnabled).Register(a.pubSub.Router, a.pubSub.Subscriber)
	service.NewSignedReplyHandler(a.db, a.svc.certRequest).Register(a.pubSub.Router, a.pubSub.Subscriber)

	return nil
}
