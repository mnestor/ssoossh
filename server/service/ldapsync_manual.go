package service

import (
	"context"
	"errors"
	"time"

	"github.com/mnestor/ssoossh/server/model"
)

// AuditCLIActor is the subject and username of the actor recorded for an
// action run from the ssoosshd command line on a server host. Nobody signed
// in to run it, so the actor has no users row; the operating-system account
// that did is in the event's detail (os_user, and sudo_user under sudo).
const AuditCLIActor = "local-cli"

// AuditSubjectForCLI is the actor of an action run from the ssoosshd
// command line. It has no UserID, so it never groups with, or is mistaken
// for, a person's own events.
func AuditSubjectForCLI() *AuditSubject {
	return &AuditSubject{Subject: AuditCLIActor, Username: AuditCLIActor}
}

// ManualSyncLDAP is the half of the directory service a manual pass drives.
// LDAPDiagnostics and *LDAPService both satisfy it.
type ManualSyncLDAP interface {
	SyncWithOptions(ctx context.Context, opts SyncOptions) (*model.LDAPSyncRun, error)
}

// ManualSyncRequest is one directory sync started by hand: from the admin
// console's "Sync now", or from `ssoosshd ldap sync`.
type ManualSyncRequest struct {
	// DryRun reads the directory and changes nothing, and skips the NSS
	// sync, which has no dry run.
	DryRun bool

	// Actor is who started the pass, recorded as the audit event's actor.
	// Its UserID, when set, is also the run row's actor.
	Actor *AuditSubject

	// Detail is added to the audit event's detail to say how the pass was
	// started (the command line records via, os_user and host). The pass's
	// own keys win over anything here.
	Detail map[string]any
}

// ManualSyncResult is what a manual pass concluded.
type ManualSyncResult struct {
	// Run is the LDAP pass's run row. Nil only when the pass never started:
	// another was already running on this instance (Err is
	// ErrSyncInProgress), or the directory is disabled.
	Run *model.LDAPSyncRun
	// Err is why the LDAP pass failed. A pass that failed still has its
	// run row, which records the same error.
	Err error

	// NSSRan reports that the NSS full sync ran: nss.enabled is on and the
	// pass was not a dry run. NSS and NSSErr are its outcome.
	NSSRan bool
	NSS    *NSSSyncResult
	NSSErr error
}

// RunManualSync runs the directory sync an operator asked for, in this
// process: the LDAP enrichment pass (refresh, disable_after, reenable), then
// the NSS directory's full sync when nss is non-nil and the pass is not a
// dry run, and records one ldap.sync_triggered audit event with what both
// concluded. auditor and nss may be nil.
//
// A pass refused because another is running on this instance stops there:
// nothing ran, so nothing is synced or recorded.
//
// The NSS sync runs whatever the LDAP pass concluded: it reads the
// directory on its own connection, and an LDAP pass that found nobody to
// refresh says nothing about the POSIX entries.
//
// Safe beside other instances. Neither pass is leader-gated: every ssoosshd
// instance runs its own scheduled passes against the shared database
// (bootstrap.registerLDAPSyncJob, registerNSSDirectoryJobs), and the only
// lock is per process (LDAPService.running, NSSDirectoryService.syncing).
// What makes concurrent passes safe is in the passes themselves: the LDAP
// miss threshold is a time window rather than a count of passes
// (recordMiss), the disable and re-enable are conditional updates inside a
// transaction (autoDisable, maybeReenable), and the NSS full sync marks
// missing only the rows it did not itself see after it began (markMissing),
// tombstoning them only once nss.tombstone_after has passed. A separate
// process, `ssoosshd ldap sync`, is one more such instance.
func RunManualSync(ctx context.Context, ldap ManualSyncLDAP, nss NSSDirectorySync, auditor *AuditService, req ManualSyncRequest) *ManualSyncResult {
	opts := SyncOptions{Trigger: model.LDAPSyncTriggerManual, DryRun: req.DryRun}
	if req.Actor != nil {
		opts.ActorUserID = req.Actor.UserID
	}

	result := &ManualSyncResult{}
	result.Run, result.Err = ldap.SyncWithOptions(ctx, opts)
	if errors.Is(result.Err, ErrSyncInProgress) {
		return result
	}

	if nss != nil && !req.DryRun {
		result.NSSRan = true
		result.NSS, result.NSSErr = nss.FullSync(ctx)
	}

	// A pass that could not reach the directory still produced a run row,
	// and the event records it with its error: it says the pass ran and why
	// it failed, which is what the operator started it to find out.
	if auditor != nil {
		auditor.Record(ctx, AuditEvent{
			Action:     AuditLDAPSyncTriggered,
			Actor:      req.Actor,
			OccurredAt: time.Now(),
			Detail:     manualSyncAuditDetail(req, result),
		})
	}
	return result
}

// manualSyncAuditDetail is the ldap.sync_triggered event's detail: the
// caller's keys, then the LDAP pass's counts, the NSS outcome when it ran,
// and the LDAP pass's error.
func manualSyncAuditDetail(req ManualSyncRequest, result *ManualSyncResult) map[string]any {
	detail := make(map[string]any, len(req.Detail)+10)
	for k, v := range req.Detail {
		detail[k] = v
	}

	run := result.Run
	detail["dry_run"] = run != nil && run.DryRun
	if result.NSSRan {
		detail["nss"] = newNSSSyncAudit(result.NSS, result.NSSErr)
	}
	if run != nil {
		detail["run_id"] = run.ID
		detail["users"] = run.UsersSeen
		detail["found"] = run.Found
		detail["missing"] = run.Missing
		detail["failed"] = run.Failed
		detail["disabled"] = run.Disabled
		detail["reenabled"] = run.Reenabled
	}
	if result.Err != nil {
		detail["error"] = result.Err.Error()
	}
	return detail
}

// nssSyncAudit is the NSS sync's outcome as the ldap.sync_triggered event
// records it. Its JSON is webtypes.NSSSyncRunResponse's, which is what the
// event recorded while the sync endpoint built it.
type nssSyncAudit struct {
	Full       bool          `json:"full"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt *time.Time    `json:"finished_at,omitempty"`
	Accounts   NSSSyncCounts `json:"accounts"`
	Groups     NSSSyncCounts `json:"groups"`
	Error      string        `json:"error,omitempty"`
}

// newNSSSyncAudit maps an NSS sync's outcome to its audit shape. A run
// refused because another is in progress has no result, only the error.
func newNSSSyncAudit(result *NSSSyncResult, err error) nssSyncAudit {
	out := nssSyncAudit{}
	if result != nil {
		out.Full = result.Full
		out.StartedAt = result.StartedAt
		if !result.FinishedAt.IsZero() {
			finished := result.FinishedAt
			out.FinishedAt = &finished
		}
		out.Accounts = result.Accounts
		out.Groups = result.Groups
	}
	if err != nil {
		out.Error = err.Error()
	}
	return out
}
