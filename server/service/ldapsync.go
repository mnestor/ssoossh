package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-ldap/ldap/v3"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// Sync refreshes directory data for every known user and auto-disables
// those whose entry has stopped resolving.
//
// Scope is deliberately narrow: only users with a user_ldap row, meaning
// they have logged in at least once with LDAP enabled. The server never
// enumerates the directory, which keeps the user set self-selecting and
// leaves fan-out and directory views building on a bounded, consented
// population.
//
// The one rule that matters: a directory outage must never disable anyone.
// Only a search that *succeeds* and finds no entry counts as a miss.
func (s *LDAPService) Sync(ctx context.Context) error {
	if s == nil {
		return nil
	}

	var rows []model.UserLDAP
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return fmt.Errorf("failed to list directory-synced users: %w", err)
	}
	if len(rows) == 0 {
		return nil
	}

	conn, err := s.dial(&s.config.LDAP)
	if err != nil {
		// Unreachable directory: update nothing, count nothing, log
		// loudly. This is the branch that keeps an outage from cascading
		// into a mass disable.
		s.log.ErrorContext(ctx, "directory sync could not reach the server; nothing was counted as a miss",
			"users", len(rows), "error", err)
		return fmt.Errorf("directory sync could not connect: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			s.log.WarnContext(ctx, "failed to close the directory connection", "error", err)
		}
	}()

	var found, missing, failed int
	for i := range rows {
		switch s.syncUser(ctx, conn, &rows[i]) {
		case syncFound:
			found++
		case syncMissing:
			missing++
		case syncFailed:
			failed++
		}
	}

	s.log.InfoContext(ctx, "directory sync completed",
		"users", len(rows), "found", found, "missing", missing, "failed", failed)
	return nil
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

// syncUser refreshes one user, returning what the pass concluded.
func (s *LDAPService) syncUser(ctx context.Context, conn ldapConn, row *model.UserLDAP) syncOutcome {
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
		s.recordMiss(ctx, &user, row)
		return syncMissing
	}

	// Found: refresh everything and clear the miss counter.
	if err := s.persistSync(ctx, row.UserID, entry); err != nil {
		s.log.ErrorContext(ctx, "failed to persist a directory sync result",
			"user_id", row.UserID, "error", err)
		return syncFailed
	}
	s.maybeReenable(ctx, &user)
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

// recordMiss increments the miss counter and disables the user once it
// reaches the configured threshold.
func (s *LDAPService) recordMiss(ctx context.Context, user *model.User, row *model.UserLDAP) {
	now := time.Now()
	misses := row.ConsecutiveMisses + 1

	if err := s.db.WithContext(ctx).Model(&model.UserLDAP{}).
		Where("user_id = ?", row.UserID).
		Updates(map[string]any{
			"consecutive_misses": misses,
			"last_synced_at":     now,
			"updated_at":         now,
		}).Error; err != nil {
		s.log.ErrorContext(ctx, "failed to record a directory miss", "user_id", row.UserID, "error", err)
		return
	}

	threshold := s.config.LDAP.Sync.DisableAfter
	if threshold <= 0 || misses < threshold || user.DisabledAt != nil {
		s.log.InfoContext(ctx, "directory entry not found",
			"user_id", row.UserID, "username", user.Username,
			"consecutive_misses", misses, "disable_after", threshold)
		return
	}

	s.autoDisable(ctx, user, misses, now)
}

// autoDisable disables a user whose directory entry has been missing for
// the configured number of consecutive successful searches.
//
// disabled_source is what makes this reversible safely: the sync clears
// only disables it caused, so an operator's disable is never undone
// automatically. DisabledByUserID stays NULL, since it is a users.id and
// cannot represent the system actor.
func (s *LDAPService) autoDisable(ctx context.Context, user *model.User, misses int, now time.Time) {
	source := model.DisabledSourceLDAPSync
	reason := fmt.Sprintf("directory entry not found on %d consecutive successful searches", misses)

	auditEvent := AuditEvent{
		Action:     AuditUserAutoDisabled,
		System:     true,
		Target:     AuditSubjectFromUser(user),
		Reason:     reason,
		OccurredAt: now,
		Detail: map[string]any{
			"consecutive_misses": misses,
			"trigger":            "ldap-directory-sync",
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
		return
	case err != nil:
		s.log.ErrorContext(ctx, "failed to auto-disable a user whose directory entry is gone",
			"user_id", user.ID, "error", err)
		return
	}

	if s.auditor != nil {
		s.auditor.LogOnly(auditEvent)
	}
	s.log.WarnContext(ctx, "auto-disabled a user whose directory entry is gone",
		"user_id", user.ID, "username", user.Username, "consecutive_misses", misses)
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
func (s *LDAPService) maybeReenable(ctx context.Context, user *model.User) {
	if !s.config.LDAP.Sync.Reenable || user.DisabledAt == nil {
		return
	}
	if user.DisabledSource == nil || *user.DisabledSource != model.DisabledSourceLDAPSync {
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
	s.log.InfoContext(ctx, "cleared an automatic disable after the directory entry reappeared",
		"user_id", user.ID, "username", user.Username)
}
