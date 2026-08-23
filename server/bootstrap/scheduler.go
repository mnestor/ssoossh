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

// evictJobName identifies the resolved-outcome cache eviction pass.
const evictJobName = "certrequest-evict-resolved"

// registerJobs registers the server's scheduled jobs. Called before any
// service runner starts, so anything it runs inline here happens before the
// HTTP server accepts a request.
func (a *app) registerJobs(ctx context.Context) error {
	if err := a.registerSweepJob(ctx); err != nil {
		return err
	}
	return a.registerEvictJob(ctx)
}

// registerSweepJob schedules the stranded-request sweep, which fails
// requests left awaiting a signature that will never arrive (see
// service.SweepStrandedRequests and docs/signing-pipeline.md).
//
// A recurring job, run immediately on start as well, so a restart's
// stranded requests are cleaned up promptly and in-process ones are caught
// as they arise.
//
// There used to be a second shape here for RequestTTL = 0, which ran the
// sweep once inline and skipped the recurring pass, because a disabled TTL
// gives the sweep no bound to derive. That case no longer exists:
// config.CertificateOptions.Validate rejects a non-positive request_ttl at
// startup. Removing it also removes a genuine multi-instance hazard — with
// no bound the sweep treats every signing row as stranded, so a restarting
// instance would invalidate another instance's live in-flight requests
// (docs/dev/multi-instance-safety-plan.md, item 2).
func (a *app) registerSweepJob(ctx context.Context) error {
	certOptions := a.config.CertOptions

	// Sweeping on the signing-timeout interval keeps detection latency to
	// about one extra interval. Falls back to RequestTTL (guaranteed
	// non-zero by config validation) if the timeout is unset, since gocron
	// rejects a zero interval.
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

// registerEvictJob schedules the resolved-outcome cache eviction pass (see
// service.EvictResolved), which bounds a map that otherwise grows one entry
// per request for the life of the process, each holding a signed
// certificate.
//
// Kept separate from the sweep rather than folded into it, deliberately.
// The sweep does database work and is a candidate for leader election once
// multiple instances are supported (docs/dev/multi-instance-safety-plan.md,
// item 3). This pass operates on process-local memory, so it must run on
// every instance — if it ever inherited the sweep's leader gating, every
// non-leader would silently resume leaking.
//
// RunImmediately is pointless here (the cache is empty at startup) and the
// interval is RequestTTL rather than the signing timeout: entries only
// become evictable once they are a full TTL old, so sweeping faster than
// that just walks the map for nothing.
func (a *app) registerEvictJob(ctx context.Context) error {
	interval := a.config.CertOptions.RequestTTL

	err := a.scheduler.RegisterJob(ctx, evictJobName, gocron.DurationJob(interval),
		a.svc.certRequest.EvictResolved,
		service.RegisterJobOpts{},
	)
	if err != nil {
		return fmt.Errorf("failed to register the resolved certificate outcome eviction pass: %w", err)
	}

	slog.DebugContext(ctx, "registered resolved certificate outcome eviction",
		slog.String("job", evictJobName),
		slog.Duration("interval", interval),
	)
	return nil
}
