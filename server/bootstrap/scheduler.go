package bootstrap

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/go-co-op/gocron/v2"

	"github.com/mnestor/ssoossh/server/service"
)

// sweepJobName identifies the stranded-request sweep to the scheduler.
const sweepJobName = "certrequest-sweep"

// registerJobs registers the server's scheduled jobs. Called before any
// service runner starts, so anything it runs inline here happens before the
// HTTP server accepts a request.
func (a *app) registerJobs(ctx context.Context) error {
	return a.registerSweepJob(ctx)
}

// registerSweepJob schedules the stranded-request sweep, which fails
// requests left awaiting a signature that will never arrive (see
// service.SweepStrandedRequests and
// docs/signing-pipeline.md).
//
// Two shapes, because the sweep's bound is derived from RequestTTL:
//
//   - RequestTTL set (the normal case): a recurring job, run immediately on
//     start as well, so a restart's stranded requests are cleaned up
//     promptly and in-process ones are caught as they arise.
//   - RequestTTL disabled: no bound can be derived, so the sweep would
//     treat healthy in-flight requests as stranded. Run it once here
//     instead — inline, before the HTTP server starts, where "everything
//     in signing is stranded" is true by definition because this process
//     hasn't queued anything yet — and skip the recurring pass.
func (a *app) registerSweepJob(ctx context.Context) error {
	certOptions := a.config.CertOptions

	if certOptions.RequestTTL <= 0 {
		slog.WarnContext(ctx,
			"cert_options.request_ttl is disabled, so stranded certificate requests can only be swept at startup; "+
				"a request whose signing job is lost while running will wait indefinitely",
			slog.String("job", sweepJobName),
		)
		if err := a.svc.certRequest.SweepStrandedRequests(ctx); err != nil {
			return fmt.Errorf("failed to sweep stranded certificate requests: %w", err)
		}
		return nil
	}

	// Sweeping on the signing-timeout interval keeps detection latency to
	// about one extra interval. Falls back to RequestTTL (non-zero here) if
	// the timeout is unset, since gocron rejects a zero interval.
	interval := certOptions.SigningTimeout
	if interval <= 0 {
		interval = certOptions.RequestTTL
	}

	err := a.scheduler.RegisterJob(ctx, sweepJobName, gocron.DurationJob(interval),
		a.svc.certRequest.SweepStrandedRequests,
		service.RegisterJobOpts{RunImmediately: true},
	)
	if err != nil {
		return fmt.Errorf("failed to register the stranded certificate request sweep: %w", err)
	}

	slog.DebugContext(ctx, "registered stranded certificate request sweep",
		slog.String("job", sweepJobName),
		slog.Duration("interval", interval),
	)
	return nil
}
