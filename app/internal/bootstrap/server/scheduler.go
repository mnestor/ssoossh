package bootstrap

import (
	"context"
	"net/http"
	// "github.com/mnestor/ssoossh/internal/job"
)

func registerScheduledJobs(ctx context.Context /*db *gorm.DB, svc *services, */, httpClient *http.Client /*, scheduler *job.Scheduler*/) error {
	// err = scheduler.RegisterDbCleanupJobs(ctx, db)
	// if err != nil {
	// 	return fmt.Errorf("failed to register DB cleanup jobs in scheduler: %w", err)
	// }
	// err = scheduler.RegisterApiKeyExpiryJob(ctx, svc.apiKeyService, svc.appConfigService)
	// if err != nil {
	// 	return fmt.Errorf("failed to register API key expiration jobs in scheduler: %w", err)
	// }
	// err = scheduler.RegisterAnalyticsJob(ctx, svc.appConfigService, httpClient)
	// if err != nil {
	// 	return fmt.Errorf("failed to register analytics job in scheduler: %w", err)
	// }

	return nil
}
