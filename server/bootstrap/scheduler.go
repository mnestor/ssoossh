package bootstrap

import (
	"context"
	"errors"
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

// auditSweepJobName identifies the audit-event retention sweep.
const auditSweepJobName = "audit-retention-sweep"

// ldapSyncJobName identifies the LDAP directory sync.
const ldapSyncJobName = "ldap-directory-sync"

// expiryReminderJobName identifies the enrollment expiry reminder sweep.
const expiryReminderJobName = "enrollment-expiry-reminder"

// NSS directory job names: the incremental and full syncs and the
// tombstone purge.
const (
	nssIncrementalSyncJobName = "nss-directory-incremental-sync"
	nssFullSyncJobName        = "nss-directory-full-sync"
	nssTombstonePurgeJobName  = "nss-tombstone-purge"
)

// nssTombstonePurgeInterval is how often the NSS tombstone purge runs.
// Retention is measured in days, so an hour late is nothing, and the sweep
// is one indexed query when nothing is due.
const nssTombstonePurgeInterval = time.Hour

// expiryReminderSweepDivisor sets the reminder sweep's interval as a
// fraction of the shortest reminder cadence (the daily one, or the lead
// itself when that is shorter), so a reminder is never systematically late
// by more than a small share of the gap it is meant to keep. Deriving it
// from the lead alone would sweep a 30-day lead every 30 hours and turn
// the daily reminders of the final week into every-other-day ones.
const expiryReminderSweepDivisor = 24

// minExpiryReminderInterval floors that interval. A very short lead (a lab
// setting it to an hour) would otherwise produce a sweep every couple of
// minutes, which is a query per few minutes forever to catch something
// whose deadline is measured in hours.
const minExpiryReminderInterval = 15 * time.Minute

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
	if err := a.registerAuditSweepJob(ctx); err != nil {
		return err
	}
	if err := a.registerExpiryReminderJob(ctx); err != nil {
		return err
	}
	if err := a.registerLDAPSyncJob(ctx); err != nil {
		return err
	}
	return a.registerNSSJobs(ctx)
}

// registerNSSJobs schedules the NSS directory's jobs: the incremental sync
// every nss.sync_interval, the full sync every nss.full_sync_interval
// (either skipped at zero), and the hourly tombstone purge. Nothing when
// NSS is disabled. Registered in serve and serve api, which call
// registerJobs; sign has no scheduler.
//
// Not run inline at startup, as the LDAP sync is not: the first
// incremental run, one interval after start, runs as a full sync because
// this instance has no earlier run to read changes since.
func (a *app) registerNSSJobs(ctx context.Context) error {
	if a.svc.nss == nil {
		return nil
	}
	nss := a.svc.nss
	jobs := []struct {
		name     string
		interval time.Duration
		run      func(context.Context) error
	}{
		{nssIncrementalSyncJobName, a.config.NSS.SyncInterval, func(c context.Context) error {
			_, err := nss.IncrementalSync(c)
			return nssSyncJobError(c, err)
		}},
		{nssFullSyncJobName, a.config.NSS.FullSyncInterval, func(c context.Context) error {
			_, err := nss.FullSync(c)
			return nssSyncJobError(c, err)
		}},
		{nssTombstonePurgeJobName, nssTombstonePurgeInterval, func(c context.Context) error {
			_, err := nss.PurgeTombstones(c)
			return err
		}},
	}
	for _, job := range jobs {
		if job.interval <= 0 {
			slog.DebugContext(ctx, "nss job not registered: its interval is zero", slog.String("job", job.name))
			continue
		}
		if err := a.scheduler.RegisterJob(ctx, job.name, gocron.DurationJob(job.interval), job.run, service.RegisterJobOpts{}); err != nil {
			return fmt.Errorf("failed to register %s: %w", job.name, err)
		}
		slog.DebugContext(ctx, "registered an NSS directory job",
			slog.String("job", job.name), slog.Duration("interval", job.interval))
	}
	return nil
}

// nssSyncJobError is a scheduled sync's result for the job wrapper. A run
// skipped because another is still going (a full sync outlasting the
// incremental interval) is routine, not a failure to log as one.
func nssSyncJobError(ctx context.Context, err error) error {
	if errors.Is(err, service.ErrNSSSyncInProgress) {
		slog.InfoContext(ctx, "nss sync skipped: another run is still going on this instance")
		return nil
	}
	return err
}

// registerExpiryReminderJob schedules the enrollment expiry reminder sweep,
// which sends the one-per-enrollment warning that an enrollment code is
// about to stop working (see service.EnrollmentService.SweepExpiryReminders).
//
// Not registered when mail is disabled or mail.expiry_reminder_lead is
// zero: with no relay there is nowhere for a reminder to go, and the sweep
// would be a recurring query producing events nothing consumes.
//
// Not run inline at startup either. The reminder is about a deadline days
// away, so nothing is lost by waiting one interval, and a restart loop
// would otherwise re-run a table scan on every boot.
func (a *app) registerExpiryReminderJob(ctx context.Context) error {
	if !a.config.Mail.Enabled {
		slog.DebugContext(ctx, "enrollment expiry reminder not registered: mail is disabled")
		return nil
	}
	lead := a.config.Mail.ExpiryReminderLead
	if lead <= 0 {
		slog.DebugContext(ctx, "enrollment expiry reminder not registered: mail.expiry_reminder_lead is zero")
		return nil
	}

	interval := expiryReminderInterval(lead)

	err := a.scheduler.RegisterJob(ctx, expiryReminderJobName,
		gocron.DurationJob(interval),
		a.svc.enrollment.SweepExpiryReminders,
		service.RegisterJobOpts{},
	)
	if err != nil {
		return fmt.Errorf("failed to register the enrollment expiry reminder sweep: %w", err)
	}

	slog.DebugContext(ctx, "registered enrollment expiry reminder sweep",
		slog.String("job", expiryReminderJobName),
		slog.Duration("interval", interval),
		slog.Duration("lead", lead),
	)
	return nil
}

// expiryReminderInterval is how often the reminder sweep runs for a given
// lead: a fraction of the shortest cadence in play, floored so a lab lead
// of an hour does not query every couple of minutes.
func expiryReminderInterval(lead time.Duration) time.Duration {
	cadence := min(lead, service.ExpiryReminderDailyInterval)
	interval := cadence / expiryReminderSweepDivisor
	if interval < minExpiryReminderInterval {
		interval = minExpiryReminderInterval
	}
	return interval
}

// registerLDAPSyncJob schedules the directory sync, which refreshes
// enrichment data for known users and auto-disables those whose entry has
// stopped resolving (see service.LDAPService.Sync).
//
// Not registered when LDAP is disabled or the interval is zero. Not run
// inline at startup either: the sync can disable accounts, and doing that
// during boot — before the process is even serving — makes a
// misconfiguration maximally hard to interrupt.
func (a *app) registerLDAPSyncJob(ctx context.Context) error {
	if a.svc.ldap == nil {
		return nil
	}
	interval := a.config.LDAP.Sync.Interval
	if interval <= 0 {
		slog.DebugContext(ctx, "ldap directory sync not registered: ldap.sync.interval is zero")
		return nil
	}

	err := a.scheduler.RegisterJob(ctx, ldapSyncJobName,
		gocron.DurationJob(interval),
		func(jobCtx context.Context) error {
			return a.svc.ldap.Sync(jobCtx)
		},
		service.RegisterJobOpts{},
	)
	if err != nil {
		return fmt.Errorf("failed to register the LDAP directory sync: %w", err)
	}

	slog.DebugContext(ctx, "registered LDAP directory sync",
		slog.String("job", ldapSyncJobName),
		slog.Duration("interval", interval),
		slog.Duration("disable_after", a.config.LDAP.Sync.DisableAfter),
	)
	return nil
}

// registerAuditSweepJob schedules the audit-event retention sweep, which
// prunes the database copy of the audit stream by age and then by row count
// (see service.SweepAuditEvents).
//
// The table is a bounded cache serving the UI's recent-history views; the
// shipped type=audit log is the archive. Pruning is therefore never urgent,
// which is why the interval is measured in hours and the job is not run
// inline at startup the way the stranded-request sweep is.
func (a *app) registerAuditSweepJob(ctx context.Context) error {
	retention := a.config.Audit.Retention
	maxRows := a.config.Audit.MaxRows
	if retention <= 0 && maxRows <= 0 {
		slog.DebugContext(ctx, "audit retention sweep not registered: both audit.retention and audit.max_rows are disabled")
		return nil
	}

	interval := a.config.Audit.SweepInterval
	if interval <= 0 {
		interval = time.Hour
	}

	err := a.scheduler.RegisterJob(ctx, auditSweepJobName,
		gocron.DurationJob(interval),
		func(jobCtx context.Context) error {
			return service.SweepAuditEvents(jobCtx, a.db, retention, maxRows)
		},
		service.RegisterJobOpts{},
	)
	if err != nil {
		return fmt.Errorf("failed to register the audit retention sweep: %w", err)
	}

	slog.DebugContext(ctx, "registered audit retention sweep",
		slog.String("job", auditSweepJobName),
		slog.Duration("interval", interval),
		slog.Duration("retention", retention),
		slog.Int64("max_rows", maxRows),
	)
	return nil
}

// registerSweepJob schedules the stranded-request sweep, which fails requests
// left awaiting a signature that will never arrive (see
// service.SweepStrandedRequests and
// https://mnestor.github.io/ssoossh/internals/architecture/).
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
