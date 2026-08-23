// Package bootstrap contains server bootstrap initialization helpers.
// this sets up all the threads needed.
// observability, httpd, scheduled tasks
package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/italypaleale/go-kit/servicerunner"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/job"
	"github.com/mnestor/ssoossh/server/pubsub"
	"github.com/mnestor/ssoossh/server/signer"
)

// app holds the dependencies shared across the server's bootstrap sequence.
// It never leaves this package, so its fields are unexported.
type app struct {
	config     *config.Config
	httpClient *http.Client
	db         *gorm.DB
	pubSub     *pubsub.PubSub
	svc        *services
	router     *Server
	scheduler  *job.Scheduler

	// stopSessionCleanup stops the session store's periodic-cleanup
	// goroutine. Set while building the router (initEngine); registered as a
	// shutdown hook by BootstrapServe. nil in modes that build no router.
	stopSessionCleanup func(context.Context) error

	// caKeySource caches the memoized CA key source (config or HSM), built
	// once and shared by both the signer handler and CA key announcer.
	caKeySource signer.CAKeySource
	// closeCAKeySource is a nil-safe shutdown hook to close the key source
	// if it requires cleanup (HSM sources do; config sources don't). Set by
	// newCAKeySource if the source has a Close() method; registered with
	// shutdowns.Add in BootstrapServe and BootstrapSigner.
	closeCAKeySource func(context.Context) error
}

// newCAKeySource builds and memoizes the CAKeySource the signer handler signs
// with and the CA key announcer publishes: PKCS#11-backed when the HSM block
// is configured, otherwise the inline ssh_key PEM. Only constructed once per
// process, regardless of how many times this method is called. Config
// validation has already enforced exactly one source is configured for
// signing modes.
func (a *app) newCAKeySource() (signer.CAKeySource, error) {
	// Memoized: if already built, return the cached instance.
	if a.caKeySource != nil {
		return a.caKeySource, nil
	}

	// Select the key source based on config.
	if !a.config.Signer.HSM.Enabled() {
		// PEM-backed source from config
		ks, err := signer.NewConfigKeySource(a.config.Signer.SSHKey)
		if err != nil {
			return nil, err
		}
		a.caKeySource = ks
		return ks, nil
	}

	// HSM-backed source
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
	// Wrap ks.Close to match servicerunner.Service signature (accepts context, returns error).
	a.closeCAKeySource = func(context.Context) error { return ks.Close() }
	return ks, nil
}

// BootstrapServe wires up and runs the server (full or API mode).
// mode determines whether the signer runs in-process (ServerModeFull) or
// not at all (ServerModeAPI).
func BootstrapServe(cmd *cobra.Command, mode ServerMode) error {
	serviceRunners := make([]servicerunner.Service, 0, 3)
	shutdowns := &shutdownManager{
		fns: make([]servicerunner.Service, 0, 4),
	}

	// Initialize config
	c, err := config.NewConfig(cmd)
	if err != nil {
		return err
	}

	// API mode requires NATS for publishing signing jobs to the separate signer.
	if mode == ServerModeAPI && c.Signer.PubSub.Backend != config.PubSubBackendNATS {
		return fmt.Errorf("api mode publishes signing jobs to the pub/sub broker; gochannel is in-process only — set pubsub.backend to 'nats' or use full mode with an in-process signer")
	}

	a := &app{config: c}

	loggingShutdownFns, err := a.initLogging()
	if err != nil {
		return err
	}
	shutdowns.Add(loggingShutdownFns...)

	ctx := cmd.Context()

	// Initialize the observability stack, including the logger, distributed tracing, and metrics
	shutdownFns, err := a.initObservability(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize OpenTelemetry: %w", err)
	}
	slog.InfoContext(ctx, "ssoosshd is starting", "mode", mode.String())
	shutdowns.Add(shutdownFns...)

	// Connect to the database
	a.db, err = a.initDatabase()
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	// Build the message-broker primitives (gochannel-only for now — see
	// docs/signing-pipeline.md). Its Router needs to run for the
	// duration of the server (serviceRunners) and be closed on shutdown
	// (shutdowns), same as the other long-running components below.
	a.pubSub, err = a.initPubSub()
	if err != nil {
		return fmt.Errorf("failed to initialize pub/sub: %w", err)
	}
	// Appending Run here only schedules it; nothing in serviceRunners starts
	// until the servicerunner call at the bottom of this function. That's
	// what lets initPipeline register its handlers further down and still
	// have them live before the Router consumes anything — don't reorder
	// these without checking that.
	serviceRunners = append(serviceRunners, a.pubSub.Run)
	shutdowns.Add(a.pubSub.Close)

	// Create all services
	a.svc, err = a.initServices()
	if err != nil {
		return fmt.Errorf("failed to initialize services: %w", err)
	}

	// Register the certificate pipeline's queue consumers. Must come after
	// initServices; still well before anything in serviceRunners actually
	// starts (see initPipeline).
	if err := a.initPipeline(mode); err != nil {
		return fmt.Errorf("failed to initialize certificate pipeline: %w", err)
	}

	// For full mode, set up the CA key Announcer (which also seeds the registry)
	if mode == ServerModeFull {
		announcer, err := a.initCAKeyAnnouncer(mode)
		if err != nil {
			return fmt.Errorf("failed to initialize CA key announcer: %w", err)
		}
		// Register the announcer's request handler
		announcer.Register(a.pubSub.Router, a.pubSub.Subscriber)
		// Add it to serviceRunners
		serviceRunners = append(serviceRunners, announcer.Run)
	}

	// Register CA key source cleanup on shutdown (nil-safe).
	shutdowns.Add(a.closeCAKeySource)

	// Init the job scheduler
	//
	// Not unit-testable: job.NewScheduler calls gocron.NewScheduler with no
	// options, and gocron.NewScheduler's only error path comes from
	// validating options — with none passed, it always succeeds.
	a.scheduler, err = job.NewScheduler()
	if err != nil {
		return fmt.Errorf("failed to initialize job scheduler: %w", err)
	}
	serviceRunners = append(serviceRunners, a.scheduler.Run)

	// Register all scheduled jobs
	err = a.registerJobs(ctx)
	if err != nil {
		return fmt.Errorf("failed to register scheduled jobs: %w", err)
	}

	// Init the router
	a.router, err = a.initRouter()
	if err != nil {
		return err
	}
	serviceRunners = append(serviceRunners, a.router.Run)
	// Stop the session store's cleanup goroutine on shutdown (set while
	// building the router). Add is nil-safe if a mode ever skips the router.
	shutdowns.Add(a.stopSessionCleanup)

	// Run all background services
	// This call blocks until the context is canceled
	// Run the service
	err = servicerunner.
		NewServiceRunner(serviceRunners...).
		Run(ctx)
	if err != nil {
		return err
	}

	// Run all shutdown functions
	shutdowns.Run(ctx)

	return nil
}

// BootstrapSigner wires up and runs the signer-only mode: pub/sub connection,
// CA key access, no database, HTTP server, OIDC, or LDAP.
func BootstrapSigner(cmd *cobra.Command) error {
	serviceRunners := make([]servicerunner.Service, 0, 2)
	shutdowns := &shutdownManager{
		fns: make([]servicerunner.Service, 0, 3),
	}

	// Initialize config
	c, err := config.NewConfig(cmd)
	if err != nil {
		return err
	}

	// Signer mode requires NATS for receiving signing requests from API instances.
	if c.Signer.PubSub.Backend != config.PubSubBackendNATS {
		return fmt.Errorf("sign mode receives signing jobs from the pub/sub broker; gochannel is in-process only — set pubsub.backend to 'nats' to run the signer as a separate process, or use full mode with an in-process signer")
	}

	a := &app{config: c}

	loggingShutdownFns, err := a.initLogging()
	if err != nil {
		return err
	}
	shutdowns.Add(loggingShutdownFns...)

	ctx := cmd.Context()

	// Initialize the observability stack
	shutdownFns, err := a.initObservability(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize OpenTelemetry: %w", err)
	}
	slog.InfoContext(ctx, "ssoosshd signer is starting")
	shutdowns.Add(shutdownFns...)

	// Build the pub/sub broker connection (NATS only at this point)
	a.pubSub, err = a.initPubSub()
	if err != nil {
		return fmt.Errorf("failed to initialize pub/sub: %w", err)
	}
	shutdowns.Add(a.pubSub.Close)

	// Register just the signer handler (this registers it on a.pubSub.Router)
	if err := a.initSignerHandler(); err != nil {
		return fmt.Errorf("failed to initialize signer: %w", err)
	}

	// Set up the CA key Announcer for signer-only mode
	announcer, err := a.initCAKeyAnnouncer(SignerModeOnly)
	if err != nil {
		return fmt.Errorf("failed to initialize CA key announcer: %w", err)
	}
	// Register the announcer's request handler
	announcer.Register(a.pubSub.Router, a.pubSub.Subscriber)

	// Register CA key source cleanup on shutdown (nil-safe).
	shutdowns.Add(a.closeCAKeySource)

	// Start the pub/sub router - exactly once, and only after the handler is
	// registered. This was appended a second time above initSignerHandler
	// too, and the duplicate runner's "router is already running" error tore
	// the whole process down: sign mode had never actually been started.
	serviceRunners = append(serviceRunners, a.pubSub.Run)
	// Add the announcer to serviceRunners
	serviceRunners = append(serviceRunners, announcer.Run)

	// Run all background services
	err = servicerunner.
		NewServiceRunner(serviceRunners...).
		Run(ctx)
	if err != nil {
		return err
	}

	// Run all shutdown functions
	shutdowns.Run(ctx)

	return nil
}

// initCAKeyAnnouncer creates the CA key Announcer for modes that need it
// (full and signer-only). For full mode, it also seeds the registry
// synchronously so a fresh boot never serves an empty key list. The CA key
// source is built and memoized by newCAKeySource, so this call returns the
// same instance on every invocation.
func (a *app) initCAKeyAnnouncer(mode ServerMode) (*signer.Announcer, error) {
	// Load the memoized CA key source (built once, cached for reuse)
	keys, err := a.newCAKeySource()
	if err != nil {
		return nil, fmt.Errorf("failed to load CA signing key: %w", err)
	}

	// For full mode, seed the registry from the in-process key source
	if mode == ServerModeFull && a.svc != nil && a.svc.caKeyRegistry != nil {
		ctx := context.Background()
		// Get the public key from the signer
		caSigner, err := keys.Signer(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get signer for registry seed: %w", err)
		}

		// Create an announcement and upsert it into the registry
		pubKey := ssh.MarshalAuthorizedKey(caSigner.PublicKey())
		publicKeyStr := strings.TrimSpace(string(pubKey))

		announce := certmsg.CAKeyAnnounce{
			PublicKey:   publicKeyStr,
			AnnouncedAt: time.Now(),
		}

		if err := a.svc.caKeyRegistry.Upsert(ctx, announce); err != nil {
			return nil, fmt.Errorf("failed to seed CA key registry: %w", err)
		}
	}

	// Create the Announcer
	announcer := signer.NewAnnouncer(keys, a.pubSub.Publisher, 5*time.Minute)

	return announcer, nil
}
