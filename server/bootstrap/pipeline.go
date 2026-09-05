package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"

	"github.com/mnestor/ssoossh/internal/fipsmode"
	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/mail"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/signer"
)

// initPipeline registers the certificate pipeline's queue consumers on the
// pub/sub Router: the signer (sign queue → signed replies), the CA key
// listener (registry updates), and the listener/resolver (signed replies →
// audit row, delivery, terminal status). See
// https://mnestor.github.io/ssoossh/internals/architecture/.
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

	// Full and API modes: deliver notifications. Registered only when mail
	// is configured — an unconsumed topic would otherwise collect events
	// nothing ever drains.
	if mode == ServerModeFull || mode == ServerModeAPI {
		if err := a.initNotifications(); err != nil {
			return err
		}
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

// initNotifications registers the notification delivery consumer: it
// renders each queued event and hands it to the SMTP relay.
//
// Everything expensive about a notification happens here, on the broker's
// goroutines, and nothing about it happens on the request path — an
// approval or a redemption publishes an event and returns. A relay that is
// slow, greylisting, or down delays nothing a browser or an unattended job
// is waiting on.
//
// Templates are parsed and executed against a zero payload during
// NewRenderer, and the relay configuration is built once here, so a broken
// override or an unusable relay fails startup rather than producing mail
// that silently stops arriving.
func (a *app) initNotifications() error {
	if !a.config.Mail.Enabled {
		slog.Info("mail notifications are disabled (mail.enabled is false)")
		return nil
	}

	// Advice about a configuration that is valid but weaker than it should
	// be — a plaintext or unauthenticated relay reached over a network.
	// Warned rather than refused: the local-relay deployment legitimately
	// needs neither. See config.MailConfig.Warnings.
	for _, warning := range a.config.Mail.Warnings() {
		slog.Warn(warning.Msg, warning.Attrs...)
	}

	renderer, err := mail.NewRenderer(a.config.Mail.TemplateDir, a.config.Mail.SubjectPrefix)
	if err != nil {
		return fmt.Errorf("failed to load the notification templates: %w", err)
	}
	if overrides := renderer.Overrides(); len(overrides) > 0 {
		slog.Info("using local notification template overrides",
			"template_dir", a.config.Mail.TemplateDir, "templates", overrides)
	}

	sender, err := mail.NewSMTPSender(&a.config.Mail)
	if err != nil {
		return fmt.Errorf("failed to configure the mail relay: %w", err)
	}

	service.NewNotificationHandler(a.db, renderer, sender).Register(a.pubSub.Router, a.pubSub.Subscriber)

	slog.Info("mail notifications enabled",
		"relay", fmt.Sprintf("%s:%d", a.config.Mail.SMTP.Host, a.config.Mail.SMTP.Port),
		"tls", a.config.Mail.SMTP.TLS,
		"auth", a.config.Mail.SMTP.Auth)

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
		MaxCertLifetime:        a.config.Signer.MaxCertLifetime,
		MaxServiceCertLifetime: a.config.Signer.MaxServiceCertLifetime,
	}
	signer.NewHandler(keys, a.pubSub.Publisher, fipsEnabled, limits).Register(a.pubSub.Router, a.pubSub.Subscriber)

	return nil
}
