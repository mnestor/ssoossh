package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	// "github.com/mnestor/ssoossh/internal/job"

	"github.com/mnestor/ssoossh/internal/app/server/config"
	"github.com/mnestor/ssoossh/internal/common"
	"github.com/mnestor/ssoossh/internal/common/db"
	"github.com/mnestor/ssoossh/internal/utils"
	"github.com/spf13/cobra"
)

func Bootstrap(cmd *cobra.Command) error {
	ctx := cmd.Context()

	// Initialize the observability stack, including the logger, distributed tracing, and metrics
	shutdownFns, httpClient, err := initObservability(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize OpenTelemetry: %w", err)
	}
	slog.InfoContext(ctx, "ssoossh is starting")

	cfg := ctx.Value(common.ContextConfig).(*config.Config)

	// Connect to the database
	dbConn, err := db.NewDatabase(cfg.Db)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	// Create all services
	svc, err := initServices(ctx, dbConn, httpClient)
	if err != nil {
		return fmt.Errorf("failed to initialize services: %w", err)
	}

	// // Init the job scheduler
	// scheduler, err := job.NewScheduler()
	// if err != nil {
	// 	return fmt.Errorf("failed to create job scheduler: %w", err)
	// }
	// err = registerScheduledJobs(ctx, db, svc, httpClient, scheduler)
	// if err != nil {
	// 	return fmt.Errorf("failed to register scheduled jobs: %w", err)
	// }

	// Init the router
	router, err := initRouter(ctx, dbConn, svc)
	if err != nil {
		slog.Error("Failed to init router", "error", err)
		os.Exit(1)
	}

	// Run all background services
	// This call blocks until the context is canceled
	err = utils.
		NewServiceRunner(router). //, scheduler.Run).
		Run(ctx)
	if err != nil {
		return fmt.Errorf("failed to run services: %w", err)
	}

	// Invoke all shutdown functions
	// We give these a timeout of 5s
	// Note: we use a background context because the run context has been canceled already
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	err = utils.
		NewServiceRunner(shutdownFns...).
		Run(shutdownCtx) //nolint:contextcheck
	if err != nil {
		slog.Error("Error shutting down services", slog.Any("error", err))
	}

	return nil
}
