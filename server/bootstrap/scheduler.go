package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-co-op/gocron/v2"

	"github.com/mnestor/ssoossh/server/service"
)

// sweepJobName identifies the stranded-request sweep to the scheduler.
const sweepJobName = "certrequest-sweep"

// evictJobName identifies the resolved-outcome cache eviction pass.
const evictJobName = "certrequest-evict-resolved"

// caKeyExpirySweepJobName identifies the CA signer key expiry sweep.
const caKeyExpirySweepJobName = "ca-key-expiry-sweep"

// disabledUserEnrollmentSweepJobName identifies the disabled user enrollment expiry sweep.
const disabledUserEnrollmentSweepJobName = "disabled-user-enrollment-sweep"

// registerJobs registers the server's scheduled jobs. Called before any
// service runner starts, so anything it runs inline here happens before the
// HTTP server accepts a request.
func (a *app) registerJobs(ctx context.Context) error {
	if err := a.registerSweepJob(ctx); err != nil {
		return err
	}
	if err := a.registerEvictJob(ctx); err != nil {
		return err
	}
	if err := a.registerCAKeyExpirySweepJob(ctx); err != nil {
		return err
	}
	return a.registerDisabledUserEnrollmentSweepJob(ctx)
}

// registerSweepJob schedules the stranded-request sweep, which fails
// requests left awaiting a signature that will never arrive (see
// service.SweepStrandedRequests and docs/internals/signing-pipeline.md).
//
// A recurring job, run immediately on start as well, so a restart's
// stranded requests are cleaned up promptly and in-process ones are caught
// as they arise.
//
// The sweep always has a bound to derive, because
// config.CertificateOptions.Validate rejects a non-positive client_timeout
// at startup. Without one it would treat every signing row as stranded, and
// a restarting instance would invalidate another instance's live in-flight
// requests (docs/dev/multi-instance-safety-plan.md, item 2).
func (a *app) registerSweepJob(ctx context.Context) error {
	certOptions := a.config.CertOptions

	// Sweeping on the signing grace keeps detection latency to about one
	// extra interval — which is why the worst-case client wait spends two
	// of those shares, and why ApprovalTTL reserves for both. Falls back to
	// the approval TTL if the grace rounds to nothing, since gocron rejects
	// a zero interval.
	interval := certOptions.SigningGrace()
	if interval <= 0 {
		interval = certOptions.ApprovalTTL()
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
// interval is the approval TTL rather than the signing grace: entries only
// become evictable once they are a full TTL old, so sweeping faster than
// that just walks the map for nothing.
func (a *app) registerEvictJob(ctx context.Context) error {
	interval := a.config.CertOptions.ApprovalTTL()

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

// registerCAKeyExpirySweepJob schedules the hourly CA signer key expiry sweep
// (see service.CAKeyRegistry.SweepExpired), which removes keys that haven't
// been announced in the configured TTL. This is hygiene, not correctness —
// ActiveKeys never returns expired rows, so the sweep is safe at any cadence.
func (a *app) registerCAKeyExpirySweepJob(ctx context.Context) error {
	const interval = time.Hour

	err := a.scheduler.RegisterJob(ctx, caKeyExpirySweepJobName, gocron.DurationJob(interval),
		a.svc.caKeyRegistry.SweepExpired,
		service.RegisterJobOpts{},
	)
	if err != nil {
		return fmt.Errorf("failed to register the ca signer key expiry sweep: %w", err)
	}

	slog.DebugContext(ctx, "registered ca signer key expiry sweep",
		slog.String("job", caKeyExpirySweepJobName),
		slog.Duration("interval", interval),
	)
	return nil
}

// registerDisabledUserEnrollmentSweepJob schedules the disabled user
// enrollment expiry sweep, which expires service enrollments for users
// disabled more than the configured grace period ago.
//
// The sweep only runs when the grace period is set (non-zero). If not
// configured, disabled user enrollments never expire, and the admin must
// manually expire them or they persist until the standard enrollment
// expiration.
//
// The interval is the grace period itself (a good cadence for expiring
// enrollments that became eligible), or the approval TTL if the grace period
// rounds to zero, similar to the stranded request sweep. This run is not
// immediate since disabled users are typically rare and there's no urgency.
func (a *app) registerDisabledUserEnrollmentSweepJob(ctx context.Context) error {
	gracePeriod := a.config.Admin.DisableGracePeriod

	// Only register if grace period is configured (non-zero)
	if gracePeriod <= 0 {
		slog.DebugContext(ctx, "disabled user enrollment sweep disabled (admin.disable_grace_period not configured)")
		return nil
	}

	// Use the grace period as the sweep interval, but fall back to approval
	// TTL if it's very short.
	interval := gracePeriod
	if interval <= 0 {
		interval = a.config.CertOptions.ApprovalTTL()
	}

	err := a.scheduler.RegisterJob(ctx, disabledUserEnrollmentSweepJobName,
		gocron.DurationJob(interval),
		func(jobCtx context.Context) error {
			return service.SweepDisabledUserEnrollments(jobCtx, a.db, gracePeriod)
		},
		service.RegisterJobOpts{},
	)
	if err != nil {
		return fmt.Errorf("failed to register the disabled user enrollment expiry sweep: %w", err)
	}

	slog.DebugContext(ctx, "registered disabled user enrollment expiry sweep",
		slog.String("job", disabledUserEnrollmentSweepJobName),
		slog.Duration("interval", interval),
		slog.Duration("grace_period", gracePeriod),
	)
	return nil
}
