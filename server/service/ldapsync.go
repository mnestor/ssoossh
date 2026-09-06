package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/go-ldap/ldap/v3"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// ErrSyncInProgress is returned when a sync is already running on this
// instance. One pass at a time, so an impatient operator cannot stack three
// passes over the same users and multiply their directory load.
var ErrSyncInProgress = errors.New("a directory sync is already running")

// SyncOptions describes one sync pass.
type SyncOptions struct {
	// Trigger records what started the pass. It changes nothing about how
	// the pass behaves: a manual sync that behaved differently from a
	// scheduled one would be a diagnostic that lies.
	Trigger model.LDAPSyncTrigger

	// ActorUserID is the admin who triggered it, empty for a scheduled
	// pass.
	ActorUserID string

	// DryRun reads the directory and reports what the pass would do,
	// writing nothing but the run row: no refreshed attributes, no group
	// rows, no miss windows, no disables and no re-enables. It is what
	// makes the button safe to press during an incident.
	DryRun bool
}

// Sync runs the scheduled pass. See SyncWithOptions.
func (s *LDAPService) Sync(ctx context.Context) error {
	_, err := s.SyncWithOptions(ctx, SyncOptions{Trigger: model.LDAPSyncTriggerSchedule})
	return err
}

// SyncWithOptions refreshes directory data for every known user and
// auto-disables those whose entry has stopped resolving, recording the pass
// as an ldap_sync_runs row.
//
// Scope is deliberately narrow: only users with a user_ldap row, meaning
// they have logged in at least once with LDAP enabled. The server never
// enumerates the directory, which keeps the user set self-selecting and
// leaves fan-out and directory views building on a bounded, consented
// population.
//
// The one rule that matters: a directory outage must never disable anyone.
// Only a search that *succeeds* and finds no entry counts as a miss.
//
// The returned run row is written whatever the outcome, so a pass that could
// not reach the directory is still visible as a pass that ran and failed
// rather than as one that never fired.
func (s *LDAPService) SyncWithOptions(ctx context.Context, opts SyncOptions) (*model.LDAPSyncRun, error) {
	if s == nil {
		return nil, nil //nolint:nilnil // a disabled directory has no pass to run and no run to report.
	}
	if !s.running.CompareAndSwap(false, true) {
		return nil, ErrSyncInProgress
	}
	defer s.running.Store(false)

	run := s.startRun(ctx, opts)

	var rows []model.UserLDAP
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		err = fmt.Errorf("failed to list directory-synced users: %w", err)
		return s.finishRun(ctx, run, err), err
	}
	run.UsersSeen = len(rows)
	if len(rows) == 0 {
		return s.finishRun(ctx, run, nil), nil
	}

	conn, err := s.dial(&s.config.LDAP)
	if err != nil {
		// Unreachable directory: update nothing, count nothing, log
		// loudly. This is the branch that keeps an outage from cascading
		// into a mass disable.
		s.log.ErrorContext(ctx, "directory sync could not reach the server; nothing was counted as a miss",
			"users", len(rows), "error", err)
		err = fmt.Errorf("directory sync could not connect: %w", err)
		return s.finishRun(ctx, run, err), err
	}
	defer func() {
		if err := conn.Close(); err != nil {
			s.log.WarnContext(ctx, "failed to close the directory connection", "error", err)
		}
	}()

	for i := range rows {
		switch s.syncUser(ctx, conn, &rows[i], run, opts) {
		case syncFound:
			run.Found++
		case syncMissing:
			run.Missing++
		case syncFailed:
			run.Failed++
		}
	}

	s.log.InfoContext(ctx, "directory sync completed",
		"run_id", run.ID, "trigger", string(run.Trigger), "dry_run", run.DryRun,
		"users", run.UsersSeen, "found", run.Found, "missing", run.Missing, "failed", run.Failed,
		"disabled", run.Disabled, "reenabled", run.Reenabled)
	return s.finishRun(ctx, run, nil), nil
}

// startRun opens the run row. A failed insert is logged, not fatal: the pass
// is worth running even when its bookkeeping cannot be written.
func (s *LDAPService) startRun(ctx context.Context, opts SyncOptions) *model.LDAPSyncRun {
	trigger := opts.Trigger
	if trigger == "" {
		trigger = model.LDAPSyncTriggerSchedule
	}
	host, err := os.Hostname()
	if err != nil {
		// not covered: os.Hostname only fails when the kernel refuses the
		// call, which no supported platform does.
		host = ""
	}

	run := &model.LDAPSyncRun{
		ID:        uuid.NewString(),
		StartedAt: time.Now(),
		Trigger:   trigger,
		DryRun:    opts.DryRun,
		Instance:  host,
	}
	if opts.ActorUserID != "" {
		run.ActorUserID = &opts.ActorUserID
	}

	if err := s.db.WithContext(ctx).Create(run).Error; err != nil {
		s.log.ErrorContext(ctx, "failed to record the start of a directory sync", "error", err)
	}
	return run
}

// finishRun closes the run row out with its counts and any error that
// stopped the pass, and returns it for the caller to report.
func (s *LDAPService) finishRun(ctx context.Context, run *model.LDAPSyncRun, cause error) *model.LDAPSyncRun {
	now := time.Now()
	run.FinishedAt = &now
	if cause != nil {
		run.ErrorMessage = cause.Error()
	}

	if err := s.db.WithContext(ctx).Model(&model.LDAPSyncRun{}).
		Where("id = ?", run.ID).
		Updates(map[string]any{
			"finished_at":   run.FinishedAt,
			"users_seen":    run.UsersSeen,
			"found":         run.Found,
			"missing":       run.Missing,
			"failed":        run.Failed,
			"disabled":      run.Disabled,
			"reenabled":     run.Reenabled,
			"error_message": run.ErrorMessage,
		}).Error; err != nil {
		s.log.ErrorContext(ctx, "failed to record the outcome of a directory sync",
			"run_id", run.ID, "error", err)
	}
	return run
}

// LastSyncRun returns the most recently started pass, or nil when none has
// run on any instance. It is what answers "is the sync even running".
func (s *LDAPService) LastSyncRun(ctx context.Context) (*model.LDAPSyncRun, error) {
	if s == nil {
		return nil, nil //nolint:nilnil // a disabled directory has never run a pass.
	}
	var run model.LDAPSyncRun
	err := s.db.WithContext(ctx).Order("started_at DESC").First(&run).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, nil //nolint:nilnil // no pass has run, which is an answer rather than an error.
	case err != nil:
		return nil, fmt.Errorf("failed to read the last directory sync: %w", err)
	}
	return &run, nil
}

// SyncRunning reports whether a pass is in progress on this instance.
func (s *LDAPService) SyncRunning() bool {
	return s != nil && s.running.Load()
}

// syncOutcome is what one user's sync pass concluded.
type syncOutcome int

const (
	// syncFound: the entry resolved and its data was refreshed.
	syncFound syncOutcome = iota
	// syncMissing: the search succeeded and found nothing — the only
	// outcome that counts toward auto-disable.
	syncMissing
	// syncFailed: something went wrong that is not evidence about the
	// user, so nothing is counted.
	syncFailed
)

// syncUser refreshes one user, returning what the pass concluded and
// counting any containment action onto run.
func (s *LDAPService) syncUser(ctx context.Context, conn ldapConn, row *model.UserLDAP, run *model.LDAPSyncRun, opts SyncOptions) syncOutcome {
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, "id = ?", row.UserID).Error; err != nil {
		// The bookkeeping row outlived its user. Nothing to sync, and not
		// a miss: there is no account to disable.
		s.log.WarnContext(ctx, "directory sync skipped a bookkeeping row with no user",
			"user_id", row.UserID, "error", err)
		return syncFailed
	}

	entry, outcome := s.resolveEntry(ctx, conn, &user, row)
	switch outcome {
	case syncFailed:
		return syncFailed
	case syncMissing:
		s.recordMiss(ctx, &user, row, run, opts)
		return syncMissing
	}

	// Found: refresh everything and close the missing window. A dry run
	// stops here, since refreshing attributes is a write like any other.
	if !opts.DryRun {
		if err := s.persistSync(ctx, row.UserID, entry); err != nil {
			s.log.ErrorContext(ctx, "failed to persist a directory sync result",
				"user_id", row.UserID, "error", err)
			return syncFailed
		}
	}
	s.maybeReenable(ctx, &user, run, opts)
	return syncFound
}

// resolveEntry re-reads a user's entry, by DN first and falling back to one
// filter search.
//
// By DN because it is cheaper and because it distinguishes "entry deleted"
// from "filter no longer matches". The filter fallback is what lets a moved
// entry re-anchor instead of being disabled.
func (s *LDAPService) resolveEntry(ctx context.Context, conn ldapConn, user *model.User, row *model.UserLDAP) (*ldapEntry, syncOutcome) {
	identity := &Identity{
		Subject:  user.Subject,
		Username: user.Username,
		Email:    user.Email,
		Extra:    decodeExtraFields(user.ExtraFields),
	}

	if row.DN != "" {
		entry, err := s.readByDN(conn, row.DN)
		switch {
		case err != nil:
			s.log.WarnContext(ctx, "directory read by DN failed; falling back to a filter search",
				"user_id", row.UserID, "dn", row.DN, "error", err)
		case entry != nil:
			resolved, err := s.resolveFields(ctx, conn, identity, entry)
			if err != nil {
				s.log.WarnContext(ctx, "directory field searches failed; keeping the cached values",
					"user_id", row.UserID, "error", err)
				return nil, syncFailed
			}
			return resolved, syncFound
		}
	}

	// Fall back to the filter, which also re-anchors a moved entry.
	filter, err := s.userFilter.execute(s.filterData(identity, "", nil))
	if err != nil {
		s.log.ErrorContext(ctx, "failed to render the user filter during sync",
			"user_id", row.UserID, "error", err)
		return nil, syncFailed
	}
	entry, err := s.searchOne(conn, s.config.LDAP.BaseDN, filter, s.primaryAttrs)
	if err != nil {
		s.log.WarnContext(ctx, "directory search failed during sync; nothing counted as a miss",
			"user_id", row.UserID, "error", err)
		return nil, syncFailed
	}
	if entry == nil {
		// The search succeeded and found nothing. This, and only this, is
		// a miss.
		return nil, syncMissing
	}

	resolved, err := s.resolveFields(ctx, conn, identity, entry)
	if err != nil {
		s.log.WarnContext(ctx, "directory field searches failed; keeping the cached values",
			"user_id", row.UserID, "error", err)
		return nil, syncFailed
	}
	return resolved, syncFound
}

// readByDN fetches one entry by its distinguished name. A "no such object"
// result is not an error here: it is the answer, and the caller falls back
// to a filter search.
func (s *LDAPService) readByDN(conn ldapConn, dn string) (*ldap.Entry, error) {
	req := ldap.NewSearchRequest(
		dn,
		ldap.ScopeBaseObject, ldap.NeverDerefAliases,
		1, int(s.config.LDAP.Timeout.Seconds()), false,
		"(objectClass=*)", s.primaryAttrs, nil,
	)
	result, err := conn.Search(req)
	if err != nil {
		if ldap.IsErrorWithCode(err, ldap.LDAPResultNoSuchObject) {
			return nil, nil
		}
		return nil, err
	}
	if len(result.Entries) == 0 {
		return nil, nil
	}
	return result.Entries[0], nil
}

// persistSync writes a successful sync read: refreshed attributes, a new
// DN if the entry moved, a cleared miss counter, and replaced LDAP group
// rows.
func (s *LDAPService) persistSync(ctx context.Context, userID string, entry *ldapEntry) error {
	return s.persist(ctx, userID, entry)
}

// recordMiss opens or continues the missing window and disables the user
// once the entry has been missing for longer than the configured duration.
//
// The window is what the threshold measures, not the number of passes that
// observed it: every instance runs its own sync and an operator can trigger
// one by hand, so a count would mean different things in different
// deployments and would shrink every time someone pressed the button. The
// counter is still written, for the operator reading a row.
func (s *LDAPService) recordMiss(ctx context.Context, user *model.User, row *model.UserLDAP, run *model.LDAPSyncRun, opts SyncOptions) {
	now := time.Now()
	misses := row.ConsecutiveMisses + 1

	// The first miss opens the window; later ones leave it where it is, so
	// the elapsed time keeps growing rather than restarting each pass.
	missingSince := row.FirstMissingAt
	if missingSince == nil {
		missingSince = &now
	}

	// A dry run leaves the window exactly where it is, including not
	// opening one. It reports against the window as it stands, which for a
	// newly missing user is zero elapsed — the honest answer to "what would
	// this pass do right now".
	if !opts.DryRun {
		if err := s.db.WithContext(ctx).Model(&model.UserLDAP{}).
			Where("user_id = ?", row.UserID).
			Updates(map[string]any{
				"consecutive_misses": misses,
				"first_missing_at":   missingSince,
				"last_synced_at":     now,
				"updated_at":         now,
			}).Error; err != nil {
			s.log.ErrorContext(ctx, "failed to record a directory miss", "user_id", row.UserID, "error", err)
			return
		}
		row.FirstMissingAt = missingSince
	}

	threshold := s.config.LDAP.Sync.DisableAfter
	missingFor := now.Sub(*missingSince)
	if threshold <= 0 || missingFor < threshold || user.DisabledAt != nil {
		s.log.InfoContext(ctx, "directory entry not found",
			"user_id", row.UserID, "username", user.Username,
			"first_missing_at", missingSince, "missing_for", missingFor.String(),
			"consecutive_misses", misses, "disable_after", threshold.String(),
			"dry_run", opts.DryRun)
		return
	}

	// A dry run counts the disable it would have performed and performs
	// none.
	if opts.DryRun {
		run.Disabled++
		s.log.InfoContext(ctx, "directory sync dry run: would auto-disable a user whose entry is gone",
			"user_id", user.ID, "username", user.Username, "missing_for", missingFor.String())
		return
	}

	if s.autoDisable(ctx, user, missingFor, now) {
		run.Disabled++
	}
}

// autoDisable disables a user whose directory entry has been missing for
// longer than the configured duration.
//
// disabled_source is what makes this reversible safely: the sync clears
// only disables it caused, so an operator's disable is never undone
// automatically. DisabledByUserID stays NULL, since it is a users.id and
// cannot represent the system actor.
// Returns whether the disable actually happened, so the run's count
// reflects containment actions rather than attempts.
func (s *LDAPService) autoDisable(ctx context.Context, user *model.User, missingFor time.Duration, now time.Time) bool {
	source := model.DisabledSourceLDAPSync
	reason := fmt.Sprintf("directory entry not found for %s of successful searches", missingFor.Round(time.Second))

	auditEvent := AuditEvent{
		Action:     AuditUserAutoDisabled,
		System:     true,
		Target:     AuditSubjectFromUser(user),
		Reason:     reason,
		OccurredAt: now,
		Detail: map[string]any{
			"missing_for": missingFor.Round(time.Second).String(),
			"trigger":     "ldap-directory-sync",
		},
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.User{}).
			Where("id = ? AND disabled_at IS NULL", user.ID).
			Updates(map[string]any{
				"disabled_at":     now,
				"disabled_source": source,
				"disabled_reason": reason,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// Someone disabled them between the read and here. Their
			// disable stands, and this one is not recorded as if it had
			// happened.
			return errAlreadyDisabled
		}
		if s.auditor == nil {
			return nil
		}
		return s.auditor.RecordTx(tx, auditEvent)
	})
	switch {
	case errors.Is(err, errAlreadyDisabled):
		return false
	case err != nil:
		s.log.ErrorContext(ctx, "failed to auto-disable a user whose directory entry is gone",
			"user_id", user.ID, "error", err)
		return false
	}

	if s.auditor != nil {
		s.auditor.LogOnly(auditEvent)
	}
	s.log.WarnContext(ctx, "auto-disabled a user whose directory entry is gone",
		"user_id", user.ID, "username", user.Username, "missing_for", missingFor.String())
	return true
}

// errAlreadyDisabled unwinds the auto-disable transaction when someone else
// disabled the account first. Not an error condition: their disable stands.
var errAlreadyDisabled = errors.New("user was already disabled")

// maybeReenable clears an auto-disable when the directory entry reappears.
//
// Only ever clears a disable whose source is exactly ldap_sync. A user
// disabled by an admin or a SOC operator is never touched by the sync, in
// either direction — and a NULL source (a row predating the column) can
// never match, which is the safe direction.
func (s *LDAPService) maybeReenable(ctx context.Context, user *model.User, run *model.LDAPSyncRun, opts SyncOptions) {
	if !s.config.LDAP.Sync.Reenable || user.DisabledAt == nil {
		return
	}
	if user.DisabledSource == nil || *user.DisabledSource != model.DisabledSourceLDAPSync {
		return
	}

	// A dry run counts the re-enable it would have performed. Restoring
	// access is as much a change as removing it, and an operator pressing
	// dry run wants to see both.
	if opts.DryRun {
		run.Reenabled++
		s.log.InfoContext(ctx, "directory sync dry run: would clear an automatic disable",
			"user_id", user.ID, "username", user.Username)
		return
	}

	now := time.Now()
	auditEvent := AuditEvent{
		Action:     AuditUserEnabled,
		System:     true,
		Target:     AuditSubjectFromUser(user),
		Reason:     "directory entry reappeared; clearing the automatic disable",
		OccurredAt: now,
		Detail:     map[string]any{"trigger": "ldap-directory-sync"},
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.User{}).
			Where("id = ? AND disabled_source = ?", user.ID, model.DisabledSourceLDAPSync).
			Updates(map[string]any{
				"disabled_at":     nil,
				"disabled_source": nil,
				"disabled_reason": "",
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			// The source changed under us, which means an operator took
			// over the disable. Leave it alone.
			return errAlreadyDisabled
		}
		if s.auditor == nil {
			return nil
		}
		return s.auditor.RecordTx(tx, auditEvent)
	})
	switch {
	case errors.Is(err, errAlreadyDisabled):
		return
	case err != nil:
		s.log.ErrorContext(ctx, "failed to clear an automatic disable after the directory entry reappeared",
			"user_id", user.ID, "error", err)
		return
	}

	if s.auditor != nil {
		s.auditor.LogOnly(auditEvent)
	}
	run.Reenabled++
	s.log.InfoContext(ctx, "cleared an automatic disable after the directory entry reappeared",
		"user_id", user.ID, "username", user.Username)
}
