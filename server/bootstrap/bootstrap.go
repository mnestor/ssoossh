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
	"github.com/mnestor/ssoossh/server/logging"
)

// app holds the dependencies shared across the server's bootstrap sequence.
// It never leaves this package, so its fields are unexported.
type app struct {
	config     *config.Config
	httpClient *http.Client
	db         *gorm.DB
	svc        *services
	router     *Server
	scheduler  *job.Scheduler
}

// Bootstrap wires up configuration, logging, observability, the database,
// services, and the HTTP router, then runs the server until cmd's context
// is canceled.
func Bootstrap(cmd *cobra.Command) error {
	serviceRunners := make([]servicerunner.Service, 0, 2)
	shutdowns := &shutdownManager{
		fns: make([]servicerunner.Service, 0, 4),
	}

	// Initialize config
	c, err := config.NewConfig(cmd)
	if err != nil {
		return err
	}

	a := &app{config: c}

	if err := logging.Setup(a.config); err != nil {
		return err
	}

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

	// Create all services
	a.svc, err = a.initServices()
	if err != nil {
		return fmt.Errorf("failed to initialize services: %w", err)
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
	err = a.registerJobs()
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
