package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// PurgeTombstones deletes every account and group tombstoned longer ago
// than nss.tombstone_retention, with their membership rows and
// collisions, freeing their numbers and names. One nss.tombstone_purged
// audit event (System) per row, in the transaction that deletes it. A row
// that cannot be purged is logged and left for the next sweep; the first
// such error is returned after the rest have been tried.
func (s *NSSDirectoryService) PurgeTombstones(ctx context.Context) (int, error) {
	if s == nil {
		return 0, nil
	}
	cutoff := s.now().UTC().Add(-s.config.NSS.TombstoneRetention)
	db := s.db.WithContext(ctx)

	var accounts []model.PosixUser
	if err := db.Where("deleted_at IS NOT NULL AND deleted_at <= ?", cutoff).Find(&accounts).Error; err != nil {
		return 0, fmt.Errorf("find NSS accounts past tombstone retention: %w", err)
	}
	var groups []model.PosixGroup
	if err := db.Where("deleted_at IS NOT NULL AND deleted_at <= ?", cutoff).Find(&groups).Error; err != nil {
		return 0, fmt.Errorf("find NSS groups past tombstone retention: %w", err)
	}

	purged := 0
	var firstErr error
	note := func(err error, label, dn string) {
		if err == nil {
			purged++
			return
		}
		s.log.ErrorContext(ctx, "could not purge an NSS tombstone; it is tried again at the next sweep",
			"kind", label, "dn", dn, "error", err)
		if firstErr == nil {
			firstErr = err
		}
	}
	for i := range accounts {
		note(s.purgeAccount(ctx, &accounts[i], nil), "account", accounts[i].DN)
	}
	for i := range groups {
		note(s.purgeGroup(ctx, &groups[i], nil), "group", groups[i].DN)
	}
	if purged > 0 {
		s.log.InfoContext(ctx, "purged NSS tombstones past nss.tombstone_retention", "rows", purged)
	}
	return purged, firstErr
}

// PurgeUserTombstone purges the tombstoned account with uid before
// nss.tombstone_retention, for an admin handing its number or name on
// deliberately. A NotFoundError unless an account with that uid exists and
// is tombstoned. Returns the row as it was before the purge.
func (s *NSSDirectoryService) PurgeUserTombstone(ctx context.Context, uid uint32, actor *AuditSubject) (*model.PosixUser, error) {
	if s == nil {
		return nil, errNSSDisabled()
	}
	var row model.PosixUser
	err := s.db.WithContext(ctx).Where("uid = ? AND deleted_at IS NOT NULL", uid).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &errorresponses.NotFoundError{Resource: fmt.Sprintf("tombstoned NSS account with uid %d", uid)}
	}
	if err != nil {
		return nil, fmt.Errorf("read the NSS account with uid %d: %w", uid, err)
	}
	return &row, s.purgeAccount(ctx, &row, actor)
}

// PurgeGroupTombstone is PurgeUserTombstone for the group with gid.
func (s *NSSDirectoryService) PurgeGroupTombstone(ctx context.Context, gid uint32, actor *AuditSubject) (*model.PosixGroup, error) {
	if s == nil {
		return nil, errNSSDisabled()
	}
	var row model.PosixGroup
	err := s.db.WithContext(ctx).Where("gid = ? AND deleted_at IS NOT NULL", gid).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, &errorresponses.NotFoundError{Resource: fmt.Sprintf("tombstoned NSS group with gid %d", gid)}
	}
	if err != nil {
		return nil, fmt.Errorf("read the NSS group with gid %d: %w", gid, err)
	}
	return &row, s.purgeGroup(ctx, &row, actor)
}

// errNSSDisabled is what the admin actions answer with NSS off.
func errNSSDisabled() error {
	return &errorresponses.NotFoundError{Resource: "the NSS directory (nss.enabled is false)"}
}

// purgeEvent is the nss.tombstone_purged event for row id of t: System
// when actor is nil (the retention sweep), the admin's otherwise. Its ref
// outlives the row, so the history stays until the events age out.
func (s *NSSDirectoryService) purgeEvent(t nssTable, id string, actor *AuditSubject, detail map[string]any) AuditEvent {
	return AuditEvent{
		Action:     AuditNSSTombstonePurged,
		Actor:      actor,
		System:     actor == nil,
		Detail:     detail,
		OccurredAt: s.now(),
		NSSRefs:    []NSSAuditRef{t.auditRef(id)},
	}
}

// purgeAccount deletes one tombstoned account row, its membership rows and
// its collisions, and records the purge, in one transaction. The rows its
// collisions withheld are served again unless another holds them.
func (s *NSSDirectoryService) purgeAccount(ctx context.Context, row *model.PosixUser, actor *AuditSubject) error {
	event := s.purgeEvent(accountTable(), row.ID, actor, map[string]any{
		"table": "posix_users", "name": row.Name, "uid": row.UID, "gid": row.GID, "dn": row.DN,
		"deleted_at": row.DeletedAt,
	})
	var batch nssAuditBatch
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND deleted_at IS NOT NULL", row.ID).Delete(&model.PosixUser{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return &errorresponses.NotFoundError{Resource: fmt.Sprintf("tombstoned NSS account with uid %d", row.UID)}
		}
		if err := s.dropMembers(tx, &batch, "uid = ?", row.UID, nssMemberChange{
			actor: actor, userNames: map[uint32]string{row.UID: row.Name}, userIDs: map[uint32]string{row.UID: row.ID},
		}); err != nil {
			return err
		}
		if err := s.dropCollisionsOf(tx, accountTable(), row.ID); err != nil {
			return err
		}
		return s.recordTx(tx, event)
	})
	if err != nil {
		return fmt.Errorf("purge the NSS account %s: %w", row.DN, err)
	}
	s.logOnly(event)
	s.flush(&batch)
	return nil
}

// purgeGroup is purgeAccount for a group.
func (s *NSSDirectoryService) purgeGroup(ctx context.Context, row *model.PosixGroup, actor *AuditSubject) error {
	event := s.purgeEvent(groupTable(), row.ID, actor, map[string]any{
		"table": "posix_groups", "name": row.Name, "gid": row.GID, "dn": row.DN, "deleted_at": row.DeletedAt,
	})
	var batch nssAuditBatch
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Where("id = ? AND deleted_at IS NOT NULL", row.ID).Delete(&model.PosixGroup{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return &errorresponses.NotFoundError{Resource: fmt.Sprintf("tombstoned NSS group with gid %d", row.GID)}
		}
		if err := s.dropMembers(tx, &batch, "gid = ?", row.GID, nssMemberChange{
			actor: actor, groupNames: map[uint32]string{row.GID: row.Name}, groupIDs: map[uint32]string{row.GID: row.ID},
		}); err != nil {
			return err
		}
		if err := s.dropCollisionsOf(tx, groupTable(), row.ID); err != nil {
			return err
		}
		return s.recordTx(tx, event)
	})
	if err != nil {
		return fmt.Errorf("purge the NSS group %s: %w", row.DN, err)
	}
	s.logOnly(event)
	s.flush(&batch)
	return nil
}

// dropMembers deletes the membership rows matching where for a purge and
// records nss.group_members_changed for each group that lost members. The
// purged row is already gone, so its name and id come from c, whose via
// is set here.
func (s *NSSDirectoryService) dropMembers(tx *gorm.DB, b *nssAuditBatch, where string, value uint32, c nssMemberChange) error {
	before, err := readMembers(tx, where, value)
	if err != nil {
		return err
	}
	if err := tx.Where(where, value).Delete(&model.PosixGroupMember{}).Error; err != nil {
		return err
	}
	c.via = nssViaTombstonePurged
	return s.recordMemberChanges(tx, b, memberDeltas(before, nssMembers{}), c)
}

// dropCollisionsOf deletes every collision naming the row id of t, which
// is going away, and recomputes the rows they withheld.
func (s *NSSDirectoryService) dropCollisionsOf(tx *gorm.DB, t nssTable, id string) error {
	var pairs []model.NSSCollision
	if err := tx.Where("(holder_id = ? OR other_id = ?) AND kind IN ?", id, id, t.kinds()).Find(&pairs).Error; err != nil {
		return fmt.Errorf("read the collisions of a purged %s: %w", t.label, err)
	}
	if err := deleteCollisions(tx, pairs); err != nil {
		return err
	}
	affected := map[string]bool{}
	markAffected(affected, pairs)
	delete(affected, id)
	return s.recomputeWithheld(tx, t, affected, s.now().UTC())
}

// recordTx writes event in tx when an auditor is attached.
func (s *NSSDirectoryService) recordTx(tx *gorm.DB, event AuditEvent) error {
	if s.auditor == nil {
		return nil
	}
	return s.auditor.RecordTx(tx, event)
}

// logOnly emits event's shipped-log line after its transaction commits.
func (s *NSSDirectoryService) logOnly(event AuditEvent) {
	if s.auditor != nil {
		s.auditor.LogOnly(event)
	}
}
