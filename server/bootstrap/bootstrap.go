// Package bootstrap contains server bootstrap initialization helpers.
// this sets up all the threads needed.
// observability, httpd, scheduled tasks
package bootstrap

import (
	"fmt"
	"log/slog"
	"net/http"

	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/italypaleale/go-kit/servicerunner"
	"github.com/spf13/cobra"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/job"
	"github.com/mnestor/ssoossh/server/pubsub"
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
}

// Bootstrap wires up configuration, logging, observability, the database,
// services, and the HTTP router, then runs the server until cmd's context
// is canceled.
func Bootstrap(cmd *cobra.Command) error {
	serviceRunners := make([]servicerunner.Service, 0, 3)
	shutdowns := &shutdownManager{
		fns: make([]servicerunner.Service, 0, 4),
	}

	// Initialize config
	c, err := config.NewConfig(cmd)
	if err != nil {
		return err
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
	slog.InfoContext(ctx, "ssoosshd is starting")
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
	if err := a.initPipeline(); err != nil {
		return fmt.Errorf("failed to initialize certificate pipeline: %w", err)
	}

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
	if mode == ServerModeAPI && c.PubSub.Backend != config.PubSubBackendNATS {
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
	if c.PubSub.Backend != config.PubSubBackendNATS {
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
	serviceRunners = append(serviceRunners, a.pubSub.Run)
	shutdowns.Add(a.pubSub.Close)

	// Register just the signer handler (this registers it on a.pubSub.Router)
	if err := a.initSignerHandler(); err != nil {
		return fmt.Errorf("failed to initialize signer: %w", err)
	}

	// Start the pub/sub router
	serviceRunners = append(serviceRunners, a.pubSub.Run)

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
