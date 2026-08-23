package bootstrap

import (
	"context"
	"fmt"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"

	"github.com/mnestor/ssoossh/internal/fipsmode"
	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/signer"
)

// initPipeline registers the certificate pipeline's queue consumers on the
// pub/sub Router: the signer (sign queue → signed replies), the CA key
// listener (registry updates), and the listener/resolver (signed replies →
// audit row, delivery, terminal status). See docs/signing-pipeline.md.
//
// For full mode, the signer, listener, and CA key listener are registered.
// For API mode, only the listener and CA key listener are registered.
// Signer-only mode is handled separately.
//
// Ordering: must run after initPubSub (needs the Router) and initServices
// (the listener and CA registry need certRequest and the registry).
// It's safe that a.pubSub.Run was already appended to serviceRunners by then
// — nothing in that slice actually starts until servicerunner runs it at the
// end of Bootstrap, so every handler is registered well before the Router
// begins consuming.
func (a *app) initPipeline(mode ServerMode) error {
	// Full mode: register both signer and listener
	if mode == ServerModeFull {
		if err := a.initSignerHandler(); err != nil {
			return err
		}
	}

	// Full and API modes: register the listener
	if mode == ServerModeFull || mode == ServerModeAPI {
		service.NewSignedReplyHandler(a.db, a.svc.certRequest).Register(a.pubSub.Router, a.pubSub.Subscriber)
	}

	// Full and API modes: register the CA key listener
	if mode == ServerModeFull || mode == ServerModeAPI {
		service.NewCAKeyListener(a.svc.caKeyRegistry).Register(a.pubSub.Router, a.pubSub.Subscriber)

		// Publish one CAKeyRequestTopic message so already-running signers
		// re-announce immediately (startup order stops mattering).
		if err := a.pubSub.Publisher.Publish(
			certmsg.CAKeyRequestTopic,
			message.NewMessage(watermill.NewUUID(), []byte{}),
		); err != nil {
			return fmt.Errorf("failed to publish CA key request at startup: %w", err)
		}
	}

	return nil
}

// initSignerHandler registers the signer handler on the pub/sub Router.
// Extracted so it can be called independently by signer-only mode. Uses the
// memoized CA key source built by newCAKeySource (which also gates to HSM or
// config sources based on configuration).
func (a *app) initSignerHandler() error {
	// Load the memoized CA key source (built once on first call, cached for reuse).
	// Failing startup on a bad key is the point — a server that can't sign is
	// misconfigured, and finding out at the first approval instead of at boot would
	// be worse.
	keys, err := a.newCAKeySource()
	if err != nil {
		return fmt.Errorf("failed to load CA signing key: %w", err)
	}

	fipsEnabled := a.config.FIPSEnabled()
	if fipsEnabled {
		caSigner, err := keys.Signer(context.Background())
		if err != nil {
			// not covered: keys is the *signer.ConfigKeySource constructed
			// just above, and its Signer returns the already-parsed key
			// with a nil error.
			return fmt.Errorf("failed to load CA signing key: %w", err)
		}
		keyType, ok := fipsmode.FromSSHAlgorithm(caSigner.PublicKey().Type())
		if !ok || !fipsmode.IsApprovedInFIPS(keyType) {
			return fmt.Errorf("CA key algorithm %q is not FIPS-approved", caSigner.PublicKey().Type())
		}
	}

	limits := signer.SignLimits{
		MaxCertLifetime:     a.config.Signer.MaxCertLifetime,
		MaxHostCertLifetime: a.config.Signer.MaxHostCertLifetime,
	}
	signer.NewHandler(keys, a.pubSub.Publisher, fipsEnabled, limits).Register(a.pubSub.Router, a.pubSub.Subscriber)

	return nil
}
