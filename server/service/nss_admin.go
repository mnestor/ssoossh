package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// NSSDirectoryAdmin is what the /api/admin/nss handlers drive: the pending
// id changes and their acceptance, the collisions report and the early
// tombstone purge. Implemented by *NSSDirectoryService; an interface so
// the handlers' authorization and wire mapping can be tested on their own.
type NSSDirectoryAdmin interface {
	PendingIDChanges(ctx context.Context) ([]model.PosixUser, []model.PosixGroup, error)
	AcceptUserIDChange(ctx context.Context, uid uint32, actor *AuditSubject) (*model.PosixUser, error)
	AcceptGroupIDChange(ctx context.Context, gid uint32, actor *AuditSubject) (*model.PosixGroup, error)
	Collisions(ctx context.Context) ([]NSSCollisionReport, error)
	PurgeUserTombstone(ctx context.Context, uid uint32, actor *AuditSubject) (*model.PosixUser, error)
	PurgeGroupTombstone(ctx context.Context, gid uint32, actor *AuditSubject) (*model.PosixGroup, error)
}

// NSSDirectorySync is the NSS half of POST /api/admin/ldap/sync: a full
// sync run on this instance. Implemented by *NSSDirectoryService.
type NSSDirectorySync interface {
	FullSync(ctx context.Context) (*NSSSyncResult, error)
}

// PendingIDChanges lists the accounts and the groups whose directory entry
// gives a stored uid or gid a different value, oldest first: what an admin
// has to accept (or get the directory reverted) before hosts see it.
func (s *NSSDirectoryService) PendingIDChanges(ctx context.Context) ([]model.PosixUser, []model.PosixGroup, error) {
	if s == nil {
		return nil, nil, errNSSDisabled()
	}
	db := s.db.WithContext(ctx)
	var accounts []model.PosixUser
	if err := db.Where("pending_uid IS NOT NULL OR pending_gid IS NOT NULL").
		Order("pending_since, name").Find(&accounts).Error; err != nil {
		return nil, nil, fmt.Errorf("list pending NSS account id changes: %w", err)
	}
	var groups []model.PosixGroup
	if err := db.Where("pending_gid IS NOT NULL").
		Order("pending_since, name").Find(&groups).Error; err != nil {
		return nil, nil, fmt.Errorf("list pending NSS group id changes: %w", err)
	}
	return accounts, groups, nil
}

// AcceptUserIDChange applies the pending uid and gid of the account
// stored with uid, in one transaction: the account's membership rows are
// re-keyed to the new uid, and the audit event is written. A NotFoundError
// when there is no such account or nothing is pending; a ConflictError when
// another row, a tombstone included, holds the new uid.
func (s *NSSDirectoryService) AcceptUserIDChange(ctx context.Context, uid uint32, actor *AuditSubject) (*model.PosixUser, error) {
	if s == nil {
		return nil, errNSSDisabled()
	}
	now := s.now().UTC()
	var accepted model.PosixUser
	var event AuditEvent
	var batch nssAuditBatch
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		event, err = s.acceptUserTx(tx, uid, actor, now, &accepted, &batch)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.logOnly(event)
	s.flush(&batch)
	return &accepted, nil
}

// acceptUserTx is AcceptUserIDChange's transaction, leaving the account as
// accepted in out and returning the audit event it recorded. A changed uid
// moves the account between the member sets of its groups, which records
// nss.group_members_changed in b for each.
func (s *NSSDirectoryService) acceptUserTx(tx *gorm.DB, uid uint32, actor *AuditSubject, now time.Time, out *model.PosixUser, b *nssAuditBatch) (AuditEvent, error) {
	var row model.PosixUser
	err := tx.Where("uid = ?", uid).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && row.PendingUID == nil && row.PendingGID == nil) {
		return AuditEvent{}, &errorresponses.NotFoundError{Resource: fmt.Sprintf("pending id change for the NSS account with uid %d", uid)}
	}
	if err != nil {
		return AuditEvent{}, err
	}

	newUID, newGID := row.UID, row.GID
	if row.PendingUID != nil {
		newUID = *row.PendingUID
	}
	if row.PendingGID != nil {
		newGID = row.PendingGID
	}
	var before nssMembers
	if newUID != row.UID {
		if before, err = readMembers(tx, "uid IN ?", []uint32{row.UID, newUID}); err != nil {
			return AuditEvent{}, err
		}
		if err := s.rekeyAccount(tx, &row, newUID); err != nil {
			return AuditEvent{}, err
		}
	}
	if err := tx.Model(&model.PosixUser{}).Where("id = ?", row.ID).Updates(map[string]any{
		"uid": newUID, "gid": newGID, "pending_uid": nil, "pending_gid": nil, "pending_since": nil, "updated_at": now,
	}).Error; err != nil {
		return AuditEvent{}, fmt.Errorf("apply the pending id change: %w", err)
	}
	if err := tx.First(out, "id = ?", row.ID).Error; err != nil {
		return AuditEvent{}, err
	}
	if before != nil {
		after, err := readMembers(tx, "uid IN ?", []uint32{row.UID, newUID})
		if err != nil {
			return AuditEvent{}, err
		}
		if err := s.recordMemberChanges(tx, b, memberDeltas(before, after), nssMemberChange{
			via: nssViaIDChangeAccepted, actor: actor, userNames: map[uint32]string{row.UID: row.Name, newUID: row.Name},
			userIDs: map[uint32]string{row.UID: row.ID, newUID: row.ID},
		}); err != nil {
			return AuditEvent{}, err
		}
	}
	if err := s.releaseHolds(tx, accountTable(), &nssRowRef{ID: out.ID, Name: out.Name, Number: out.UID}, now); err != nil {
		return AuditEvent{}, err
	}

	event := AuditEvent{
		Action:     AuditNSSIDChangeAccepted,
		Actor:      actor,
		OccurredAt: now,
		NSSRefs:    []NSSAuditRef{accountTable().auditRef(row.ID)},
		Detail: map[string]any{
			"table": "posix_users", "name": row.Name, "dn": row.DN,
			"old_uid": row.UID, "new_uid": newUID, "old_gid": row.GID, "new_gid": newGID,
		},
	}
	return event, s.recordTx(tx, event)
}

// rekeyAccount moves row's membership rows from its uid to newUID, after
// refusing a newUID any other row holds.
func (s *NSSDirectoryService) rekeyAccount(tx *gorm.DB, row *model.PosixUser, newUID uint32) error {
	holder, err := accountTable().holderOf(tx, "uid", newUID, row.ID)
	if err != nil {
		return err
	}
	if holder != nil {
		return &errorresponses.ConflictError{Reason: fmt.Sprintf(
			"uid %d is held by the NSS account %s (%s); a tombstone keeps its uid until it is purged", newUID, holder.Name, holder.DN)}
	}
	// No row holds newUID, so any membership row under it is left over and
	// would collide with the re-keyed ones.
	if err := tx.Where("uid = ?", newUID).Delete(&model.PosixGroupMember{}).Error; err != nil {
		return fmt.Errorf("clear left-over memberships of uid %d: %w", newUID, err)
	}
	if err := tx.Model(&model.PosixGroupMember{}).Where("uid = ?", row.UID).Update("uid", newUID).Error; err != nil {
		return fmt.Errorf("re-key the memberships of uid %d: %w", row.UID, err)
	}
	return nil
}

// AcceptGroupIDChange applies the pending gid of the group stored with
// gid, in one transaction: the group's membership rows and every account
// whose primary gid was the old one move to the new gid (and an account
// whose own pending gid was that new gid has nothing pending any more),
// and the audit event is written. Errors as AcceptUserIDChange.
func (s *NSSDirectoryService) AcceptGroupIDChange(ctx context.Context, gid uint32, actor *AuditSubject) (*model.PosixGroup, error) {
	if s == nil {
		return nil, errNSSDisabled()
	}
	now := s.now().UTC()
	var accepted model.PosixGroup
	var event AuditEvent
	var batch nssAuditBatch
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.PosixGroup
		err := tx.Where("gid = ?", gid).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && row.PendingGID == nil) {
			return &errorresponses.NotFoundError{Resource: fmt.Sprintf("pending id change for the NSS group with gid %d", gid)}
		}
		if err != nil {
			return err
		}
		newGID := *row.PendingGID

		holder, err := groupTable().holderOf(tx, "gid", newGID, row.ID)
		if err != nil {
			return err
		}
		if holder != nil {
			return &errorresponses.ConflictError{Reason: fmt.Sprintf(
				"gid %d is held by the NSS group %s (%s); a tombstone keeps its gid until it is purged", newGID, holder.Name, holder.DN)}
		}
		accounts, err := s.rekeyGroupAudited(tx, &batch, &row, newGID, actor, now)
		if err != nil {
			return err
		}
		if err := tx.Model(&model.PosixGroup{}).Where("id = ?", row.ID).Updates(map[string]any{
			"gid": newGID, "pending_gid": nil, "pending_since": nil, "updated_at": now,
		}).Error; err != nil {
			return fmt.Errorf("apply the pending gid change: %w", err)
		}
		if err := tx.First(&accepted, "id = ?", row.ID).Error; err != nil {
			return err
		}
		if err := s.releaseHolds(tx, groupTable(), &nssRowRef{ID: accepted.ID, Name: accepted.Name, Number: accepted.GID}, now); err != nil {
			return err
		}

		event = AuditEvent{
			Action:     AuditNSSIDChangeAccepted,
			Actor:      actor,
			OccurredAt: now,
			NSSRefs:    []NSSAuditRef{groupTable().auditRef(row.ID)},
			Detail: map[string]any{
				"table": "posix_groups", "name": row.Name, "dn": row.DN,
				"old_gid": row.GID, "new_gid": newGID, "accounts_rekeyed": accounts,
			},
		}
		return s.recordTx(tx, event)
	})
	if err != nil {
		return nil, err
	}
	s.logOnly(event)
	s.flush(&batch)
	return &accepted, nil
}

// rekeyGroupAudited is rekeyGroup for row, recording in b the change to
// its member set if there is one: the set moves with the group, so only
// left-over rows under the new gid could make it differ.
func (s *NSSDirectoryService) rekeyGroupAudited(tx *gorm.DB, b *nssAuditBatch, row *model.PosixGroup, newGID uint32, actor *AuditSubject, now time.Time) (int64, error) {
	before, err := readMembers(tx, "gid = ?", row.GID)
	if err != nil {
		return 0, err
	}
	accounts, err := s.rekeyGroup(tx, row.GID, newGID, now)
	if err != nil {
		return 0, err
	}
	after, err := readMembers(tx, "gid = ?", newGID)
	if err != nil {
		return 0, err
	}
	moved := nssMembers{newGID: before[row.GID]}
	return accounts, s.recordMemberChanges(tx, b, memberDeltas(moved, after), nssMemberChange{
		via: nssViaIDChangeAccepted, actor: actor, groupNames: map[uint32]string{newGID: row.Name},
		groupIDs: map[uint32]string{newGID: row.ID},
	})
}

// rekeyGroup moves the memberships of oldGID, and every account whose
// primary gid it was, to newGID, returning how many accounts moved.
func (s *NSSDirectoryService) rekeyGroup(tx *gorm.DB, oldGID, newGID uint32, now time.Time) (int64, error) {
	if err := tx.Where("gid = ?", newGID).Delete(&model.PosixGroupMember{}).Error; err != nil {
		return 0, fmt.Errorf("clear left-over members of gid %d: %w", newGID, err)
	}
	if err := tx.Model(&model.PosixGroupMember{}).Where("gid = ?", oldGID).Update("gid", newGID).Error; err != nil {
		return 0, fmt.Errorf("re-key the members of gid %d: %w", oldGID, err)
	}
	moved := tx.Model(&model.PosixUser{}).Where("gid = ?", oldGID).Updates(map[string]any{"gid": newGID, "updated_at": now})
	if moved.Error != nil {
		return 0, fmt.Errorf("re-key the accounts with primary gid %d: %w", oldGID, moved.Error)
	}
	// The directory moved these accounts' gid attribute along with the
	// group's, so what they had pending is now what they hold.
	if err := tx.Model(&model.PosixUser{}).Where("gid = ? AND pending_gid = ?", newGID, newGID).
		Update("pending_gid", nil).Error; err != nil {
		return 0, fmt.Errorf("clear pending gids that the accept applied: %w", err)
	}
	if err := tx.Model(&model.PosixUser{}).
		Where("pending_uid IS NULL AND pending_gid IS NULL AND pending_since IS NOT NULL").
		Update("pending_since", nil).Error; err != nil {
		return 0, fmt.Errorf("clear settled pending clocks: %w", err)
	}
	return moved.RowsAffected, nil
}

// releaseHolds deletes the collisions row holds over a value it no longer
// has, after its number changed, and recomputes the rows they withheld.
func (s *NSSDirectoryService) releaseHolds(tx *gorm.DB, t nssTable, row *nssRowRef, now time.Time) error {
	pruned, err := pruneStaleHolds(tx, t, row)
	if err != nil {
		return err
	}
	affected := map[string]bool{row.ID: true}
	markAffected(affected, pruned)
	return s.recomputeWithheld(tx, t, affected, now)
}

// NSSCollisionReport is one collision for the admin report: the contested
// value, the row holding it and the other entry.
type NSSCollisionReport struct {
	Kind        model.NSSCollisionKind
	Value       string
	Holder      NSSCollisionEntry
	Other       NSSCollisionEntry
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

// NSSCollisionEntry is one side of a collision. Stored is false for an
// entry the sync refused to store, which is known only by what it carried.
type NSSCollisionEntry struct {
	Name         string
	DN           string
	Number       uint32
	Stored       bool
	MissingSince *time.Time
	DeletedAt    *time.Time
}

// nssCollisionRow is a stored side of a collision as the report reads it.
type nssCollisionRow struct {
	ID           string     `gorm:"column:id"`
	Name         string     `gorm:"column:name"`
	DN           string     `gorm:"column:dn"`
	Number       uint32     `gorm:"column:number"`
	MissingSince *time.Time `gorm:"column:missing_since"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`
}

// Collisions lists every current collision, oldest first, with both sides
// described: whether each is stored, missing from the directory or a
// tombstone.
func (s *NSSDirectoryService) Collisions(ctx context.Context) ([]NSSCollisionReport, error) {
	if s == nil {
		return nil, errNSSDisabled()
	}
	db := s.db.WithContext(ctx)
	var pairs []model.NSSCollision
	if err := db.Order("first_seen_at, kind, value, other_dn").Find(&pairs).Error; err != nil {
		return nil, fmt.Errorf("list NSS collisions: %w", err)
	}

	ids := map[string][]string{}
	for _, p := range pairs {
		table := collisionTable(p.Kind).table
		ids[table] = append(ids[table], p.HolderID)
		if p.OtherID != nil {
			ids[table] = append(ids[table], *p.OtherID)
		}
	}
	rows := map[string]nssCollisionRow{}
	for _, t := range []nssTable{accountTable(), groupTable()} {
		named := dedupe(ids[t.table])
		for start := 0; start < len(named); start += nssBatchSize {
			batch := named[start:min(start+nssBatchSize, len(named))]
			var found []nssCollisionRow
			if err := db.Table(t.table).
				Select("id, name, dn, "+t.numberColumn+" AS number, missing_since, deleted_at").
				Where("id IN ?", batch).Find(&found).Error; err != nil {
				return nil, fmt.Errorf("read the NSS %s rows in collisions: %w", t.label, err)
			}
			for _, r := range found {
				rows[t.table+"/"+r.ID] = r
			}
		}
	}

	out := make([]NSSCollisionReport, 0, len(pairs))
	for _, p := range pairs {
		table := collisionTable(p.Kind).table
		report := NSSCollisionReport{
			Kind:        p.Kind,
			Value:       p.Value,
			Holder:      collisionSide(rows, table, p.HolderID),
			Other:       NSSCollisionEntry{Name: p.OtherName, DN: p.OtherDN, Number: p.OtherNumber},
			FirstSeenAt: p.FirstSeenAt,
			LastSeenAt:  p.LastSeenAt,
		}
		if p.OtherID != nil {
			if other := collisionSide(rows, table, *p.OtherID); other.Stored {
				other.DN = p.OtherDN
				report.Other = other
			}
		}
		out = append(out, report)
	}
	return out, nil
}

// collisionTable is the table a collision kind's rows belong to.
func collisionTable(kind model.NSSCollisionKind) nssTable {
	if kind.ForGroups() {
		return groupTable()
	}
	return accountTable()
}

// collisionSide describes the stored row id of table, as Collisions read
// it. A row that has gone since the collision was written is reported as
// not stored.
func collisionSide(rows map[string]nssCollisionRow, table, id string) NSSCollisionEntry {
	r, ok := rows[table+"/"+id]
	if !ok {
		return NSSCollisionEntry{}
	}
	return NSSCollisionEntry{
		Name: r.Name, DN: r.DN, Number: r.Number, Stored: true,
		MissingSince: r.MissingSince, DeletedAt: r.DeletedAt,
	}
}
