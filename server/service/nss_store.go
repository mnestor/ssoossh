package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// nssTable describes posix_users or posix_groups to the code both share:
// anchor matching, the uniqueness checks, collisions and withholding.
type nssTable struct {
	// table is the SQL table.
	table string
	// numberColumn is the frozen number: uid or gid.
	numberColumn string
	// numberKind and nameKind are the two collision kinds rows of this
	// table can hold.
	numberKind model.NSSCollisionKind
	nameKind   model.NSSCollisionKind
	// label names a row in log lines and audit details: account or group.
	label string
}

// accountTable describes posix_users.
func accountTable() nssTable {
	return nssTable{
		table: "posix_users", numberColumn: "uid",
		numberKind: model.NSSCollisionKindUID, nameKind: model.NSSCollisionKindUserName,
		label: "account",
	}
}

// groupTable describes posix_groups.
func groupTable() nssTable {
	return nssTable{
		table: "posix_groups", numberColumn: "gid",
		numberKind: model.NSSCollisionKindGID, nameKind: model.NSSCollisionKindGroupName,
		label: "group",
	}
}

// auditRef is the nss_audit_refs entry naming row id of this table on an
// audit event.
func (t nssTable) auditRef(id string) NSSAuditRef {
	kind := model.NSSAuditRefGroup
	if t.numberColumn == "uid" {
		kind = model.NSSAuditRefAccount
	}
	return NSSAuditRef{Kind: kind, RowID: id}
}

// kinds lists the table's two collision kinds.
func (t nssTable) kinds() []model.NSSCollisionKind {
	return []model.NSSCollisionKind{t.numberKind, t.nameKind}
}

// nssKinds says for which of a table's two contested values a read saw
// every directory entry that carries them. Only for those may the store
// delete a collision the read no longer shows; a read that saw only part
// of the picture (an on-demand search by name says nothing about who else
// carries the uid) leaves the others to the next read that sees it all.
type nssKinds struct {
	number bool
	name   bool
}

// nssAllKinds is a read that saw everything: a full sync, or an
// incremental one after its uniqueness search.
func nssAllKinds() nssKinds { return nssKinds{number: true, name: true} }

// has reports whether kind is one the read saw completely.
func (k nssKinds) has(t nssTable, kind model.NSSCollisionKind) bool {
	switch kind {
	case t.numberKind:
		return k.number
	case t.nameKind:
		return k.name
	default:
		return false
	}
}

// nssEntryRef is what the shared code needs of a parsed entry.
type nssEntryRef struct {
	DN          string
	DirectoryID string
	Name        string
	Number      uint32
}

// ref is the account's shared view.
func (a *nssAccount) ref() nssEntryRef {
	return nssEntryRef{DN: a.DN, DirectoryID: a.DirectoryID, Name: a.Name, Number: a.UID}
}

// ref is the group's shared view.
func (g *nssGroup) ref() nssEntryRef {
	return nssEntryRef{DN: g.DN, DirectoryID: g.DirectoryID, Name: g.Name, Number: g.GID}
}

// nssRowRef is what the shared code needs of a stored row.
type nssRowRef struct {
	ID          string `gorm:"column:id"`
	DirectoryID string `gorm:"column:directory_id"`
	DN          string `gorm:"column:dn"`
	Name        string `gorm:"column:name"`
	Number      uint32 `gorm:"column:number"`
	// withheld is whether the row was withheld before this write; only a
	// stored row the caller just read sets it.
	withheld bool `gorm:"-"`
}

// refs reads the rows matching where as nssRowRefs.
func (t nssTable) refs(tx *gorm.DB, where string, args ...any) ([]nssRowRef, error) {
	var out []nssRowRef
	err := tx.Table(t.table).
		Select("id, directory_id, dn, name, "+t.numberColumn+" AS number").
		Where(where, args...).
		Order("created_at").
		Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("read %s rows: %w", t.label, err)
	}
	return out, nil
}

// sameEntry reports whether a row and an entry found at the same DN are
// the same directory entry. With an ID on both sides they must agree: two
// entries cannot share a DN at one time, so a different ID at a stored DN
// is a new entry where an old one used to be. Either ID missing (no
// ldap.id_attribute, or a row stored before it was configured) leaves the
// DN as the only anchor.
func sameEntry(rowID, entryID string) bool {
	return rowID == "" || entryID == "" || rowID == entryID
}

// match finds the stored row for an entry: by directory_id when
// ldap.id_attribute is configured and the entry carries it, then by DN
// when that row is the same entry (sameEntry). Nil when neither anchor
// finds one, which makes the entry new.
func (t nssTable) match(tx *gorm.DB, idConfigured bool, e nssEntryRef) (*nssRowRef, error) {
	if idConfigured && e.DirectoryID != "" {
		rows, err := t.refs(tx, "directory_id = ?", e.DirectoryID)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			return &rows[0], nil
		}
	}
	rows, err := t.refs(tx, "dn = ?", e.DN)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 && sameEntry(rows[0].DirectoryID, e.DirectoryID) {
		return &rows[0], nil
	}
	return nil, nil //nolint:nilnil // no stored row is the answer "this entry is new", not an error.
}

// holderOf returns the row holding value in column (the number or the
// name), other than exceptID, or nil.
func (t nssTable) holderOf(tx *gorm.DB, column string, value any, exceptID string) (*nssRowRef, error) {
	rows, err := t.refs(tx, column+" = ? AND id <> ?", value, exceptID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

// releaseDN clears dn from every row but exceptID. Reached only when an
// entry with another directory_id now lives at dn, which makes their copy
// stale: two entries cannot share a DN, and the unique index on dn would
// refuse the new one. The released rows are matched by directory_id from
// then on.
func (s *NSSDirectoryService) releaseDN(ctx context.Context, tx *gorm.DB, t nssTable, dn, exceptID string, now time.Time) error {
	result := tx.Table(t.table).
		Where("dn = ? AND id <> ?", dn, exceptID).
		Updates(map[string]any{"dn": "", "updated_at": now})
	if result.Error != nil {
		return fmt.Errorf("release the stale DN %s: %w", dn, result.Error)
	}
	if result.RowsAffected > 0 {
		s.log.InfoContext(ctx, "released a stale DN: another directory entry now lives there",
			"table", t.table, "dn", dn)
	}
	return nil
}

// nssStoredAccount is what storing one account entry did.
type nssStoredAccount struct {
	// row is the account as stored after the write, nil when the entry was
	// refused because another row holds its uid or name.
	row     *model.PosixUser
	created bool
	// observed are the collisions this store saw, for the alerts.
	observed []model.NSSCollision
	// change is what updating a matched row changed, for the audit.
	change nssRowChange
	// audit is the events the write recorded.
	audit nssAuditBatch
}

// nssRowChange is what updating a matched row changed that the audit
// records: its name and DN before, whether it took a new name, and
// whether it was missing or a tombstone.
type nssRowChange struct {
	oldName      string
	oldDN        string
	renamed      bool
	missingSince *time.Time
	deletedAt    *time.Time
}

// revived reports whether the row was missing or a tombstone.
func (c nssRowChange) revived() bool { return c.missingSince != nil || c.deletedAt != nil }

// nssStoredGroup is what storing one group entry did.
type nssStoredGroup struct {
	row      *model.PosixGroup
	created  bool
	observed []model.NSSCollision
	// renamed is set when the entry re-anchored a stored group renamed or
	// moved in the directory (reanchorGroup).
	renamed *nssGroupRename
	change  nssRowChange
	audit   nssAuditBatch
}

// Ways a read learns that a stored group's entry has left the directory,
// as the nss.group_renamed event records them.
const (
	// nssGoneByFullSync is a full sync that enumerated every entry of
	// nss.group_filter without the row's DN among them.
	nssGoneByFullSync = "full_sync_absence"
	// nssGoneByBaseRead is a base-scope read of the row's DN with
	// nss.group_filter that found nothing, after a search for the gid
	// found no entry but the new one.
	nssGoneByBaseRead = "base_read"
)

// nssGroupGone is a read's evidence that stored groups' entries have left
// the directory, which is what lets the store re-anchor a group renamed or
// moved with its gid kept (decided 2026-10-18 by the owner). Accounts never
// get one: a uid handed to a different entry is what the collision and the
// tombstone exist to catch. A nil *nssGroupGone is no evidence, and the
// store then treats every unanchored entry as new.
type nssGroupGone struct {
	// via is how the absence was established: nssGoneByFullSync or
	// nssGoneByBaseRead.
	via string
	// gone reports whether the entry at dn is no longer a group the
	// filter selects.
	gone func(dn string) bool
}

// nssGroupRename is one stored group re-anchored on its renamed or moved
// directory entry, for the audit event.
type nssGroupRename struct {
	oldName string
	oldDN   string
	via     string
	// revived is whether the row was a tombstone.
	revived bool
	// resolved are the collisions the re-anchoring deleted.
	resolved []model.NSSCollision
}

// nssPending works out a row's pending columns after a read: a number the
// directory now gives differently is pending, the stored one stays. The
// clock restarts whenever a pending value appears or changes, and stops
// when nothing is pending.
func nssPending(storedNumber, incoming uint32, previous *uint32) *uint32 {
	if incoming == storedNumber {
		return nil
	}
	if previous != nil && *previous == incoming {
		return previous
	}
	v := incoming
	return &v
}

// nssPendingSince is the pending_since that goes with the new pending
// values: kept while every pending value is one that was already pending,
// now when one is new, nil when none is pending.
func nssPendingSince(since *time.Time, now time.Time, pairs ...[2]*uint32) *time.Time {
	anyPending, anyNew := false, false
	for _, p := range pairs {
		next, prev := p[0], p[1]
		if next == nil {
			continue
		}
		anyPending = true
		if prev == nil || *prev != *next {
			anyNew = true
		}
	}
	switch {
	case !anyPending:
		return nil
	case anyNew || since == nil:
		return &now
	default:
		return since
	}
}

// storeAccount writes one account entry the way every path writes it: the
// matched row follows the directory in everything but its frozen uid and
// gid (a differing value waits in pending_uid or pending_gid), a new entry
// is inserted unless another row (a tombstone included) holds its uid or
// name, and every collision seen is recorded and withholds the rows it
// involves. contenders are other entries the same read found carrying
// this entry's uid or name; complete says for which of the two the read
// saw every such entry; w is the path, for the audit events recorded with
// the write. The alerts are raised after the write commits.
func (s *NSSDirectoryService) storeAccount(ctx context.Context, account *nssAccount, disabled bool, contenders []*nssAccount, complete nssKinds, w nssWrite) (*nssStoredAccount, error) {
	now := s.now().UTC()
	out := &nssStoredAccount{}
	gecosChanged := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stored, contended, changed, err := s.writeAccount(ctx, tx, account, disabled, now, out)
		if err != nil {
			return err
		}
		gecosChanged = changed
		refs := make([]nssEntryRef, 0, len(contenders))
		for _, c := range contenders {
			refs = append(refs, c.ref())
		}
		if out.observed, err = s.recordCollisions(tx, accountTable(), account.ref(), accountRowRef(stored), contended, refs, complete, now); err != nil {
			return err
		}
		if stored == nil {
			return nil
		}
		// A fresh struct: GORM does not reset a field the row now holds as
		// NULL, so re-reading into stored would keep a cleared withheld_at.
		out.row = &model.PosixUser{}
		if err := tx.First(out.row, "id = ?", stored.ID).Error; err != nil {
			return err
		}
		return s.recordAccountEvents(tx, w, out)
	})
	if err != nil {
		return nil, fmt.Errorf("store the directory account %s: %w", account.DN, err)
	}
	s.flush(&out.audit)

	if gecosChanged {
		s.log.InfoContext(ctx, "cleaned the rendered gecos value of a directory account before storing it",
			"dn", account.DN, "gecos", account.Gecos)
	}
	s.raiseCollisionAlerts(ctx, accountTable(), out.observed)
	if out.row != nil && (out.row.PendingUID != nil || out.row.PendingGID != nil) {
		s.raisePendingAlert(ctx, accountTable(), out.row.ID, out.row.Name, out.row.DN,
			pendingDetail(out.row.UID, out.row.PendingUID, out.row.GID, out.row.PendingGID))
	}
	return out, nil
}

// writeAccount is storeAccount's row write: the matched row updated, or a
// new row inserted when nothing holds the entry's uid or name. It returns
// the row (nil when the entry was refused), the collisions that refused it
// or blocked its rename, and whether the gecos sanitiser changed a value
// worth logging.
func (s *NSSDirectoryService) writeAccount(ctx context.Context, tx *gorm.DB, account *nssAccount, disabled bool, now time.Time, out *nssStoredAccount) (*model.PosixUser, []model.NSSCollision, bool, error) {
	match, err := accountTable().match(tx, s.config.LDAP.IDAttribute != "", account.ref())
	if err != nil {
		return nil, nil, false, err
	}
	if match != nil {
		return s.updateAccount(ctx, tx, match.ID, account, disabled, now, &out.change)
	}
	contended, err := s.heldValues(tx, accountTable(), account.ref(), nil, now)
	if err != nil || len(contended) > 0 {
		return nil, contended, false, err
	}
	stored, err := s.insertAccount(ctx, tx, account, disabled, now)
	if err != nil {
		return nil, nil, false, err
	}
	out.created = true
	return stored, nil, account.GecosCleaned, nil
}

// accountGIDUpdate works out a matched account's stored gid and pending
// gid after a read of a. Under nss.auto_private_groups "true" the
// directory's gid is followed as it comes, absent included: sssd ignores it
// for the primary gid and keeps it only as origPrimaryGroupGidNumber, a
// supplementary group refreshed on every save (sdap_save_user), so there is
// no file ownership to protect and nothing for an admin to accept; a value
// left pending from another mode is dropped. In the other modes the stored
// gid is the primary gid and frozen: a differing value waits in
// pending_gid. A row stored without one (under "true") takes the
// directory's first gid as a new row would.
func (s *NSSDirectoryService) accountGIDUpdate(row *model.PosixUser, a *nssAccount) (*uint32, *uint32) {
	if s.config.NSS.AutoPrivateGroups == config.AutoPrivateGroupsOn {
		if !equalGID(row.GID, a.GID) {
			s.log.Info("followed a directory account's gid; nss.auto_private_groups is true, so it is a supplementary group, not the primary one",
				"dn", a.DN, "old_gid", row.GID, "gid", a.GID)
		}
		return a.GID, nil
	}
	if row.GID == nil {
		return a.GID, nil
	}
	if a.GID == nil {
		// not covered: only "true", handled above, parses an account
		// without a gid.
		return row.GID, nil
	}
	return row.GID, nssPending(*row.GID, *a.GID, row.PendingGID)
}

// equalGID reports whether two optional gids are the same, both absent
// included.
func equalGID(a, b *uint32) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// accountRowRef is a stored account's shared view, nil for none.
func accountRowRef(row *model.PosixUser) *nssRowRef {
	if row == nil {
		return nil
	}
	return &nssRowRef{
		ID: row.ID, DirectoryID: row.DirectoryID, DN: row.DN, Name: row.Name, Number: row.UID,
		withheld: row.WithheldAt != nil,
	}
}

// groupRowRef is a stored group's shared view, nil for none.
func groupRowRef(row *model.PosixGroup) *nssRowRef {
	if row == nil {
		return nil
	}
	return &nssRowRef{
		ID: row.ID, DirectoryID: row.DirectoryID, DN: row.DN, Name: row.Name, Number: row.GID,
		withheld: row.WithheldAt != nil,
	}
}

// insertAccount writes a new account row for an entry nothing holds the
// uid or name of, first releasing its DN from any stale row.
func (s *NSSDirectoryService) insertAccount(ctx context.Context, tx *gorm.DB, a *nssAccount, disabled bool, now time.Time) (*model.PosixUser, error) {
	if err := s.releaseDN(ctx, tx, accountTable(), a.DN, "", now); err != nil {
		return nil, err
	}
	row := &model.PosixUser{
		ID:           uuid.NewString(),
		UID:          a.UID,
		Name:         a.Name,
		DirectoryID:  a.DirectoryID,
		DN:           a.DN,
		GID:          a.GID,
		Home:         a.Home,
		Shell:        a.Shell,
		Gecos:        a.Gecos,
		DirDisabled:  disabled,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastSyncedAt: now,
	}
	if err := tx.Create(row).Error; err != nil {
		return nil, fmt.Errorf("insert the account row: %w", err)
	}
	return row, nil
}

// updateAccount brings the matched row id in line with the entry. It
// returns the row as updated, the collision a blocked rename makes (the
// directory renamed the entry to a name another row holds, so the row
// keeps its old name and both are withheld), and whether the gecos
// sanitiser changed a value that differs from the stored one.
func (s *NSSDirectoryService) updateAccount(ctx context.Context, tx *gorm.DB, id string, a *nssAccount, disabled bool, now time.Time, ch *nssRowChange) (*model.PosixUser, []model.NSSCollision, bool, error) {
	var row model.PosixUser
	if err := tx.First(&row, "id = ?", id).Error; err != nil {
		return nil, nil, false, fmt.Errorf("read the matched account: %w", err)
	}
	previousGecos := row.Gecos
	*ch = nssRowChange{oldName: row.Name, oldDN: row.DN, missingSince: row.MissingSince, deletedAt: row.DeletedAt}

	pendingUID := nssPending(row.UID, a.UID, row.PendingUID)
	gid, pendingGID := s.accountGIDUpdate(&row, a)
	updates := map[string]any{
		"gid":            gid,
		"home":           a.Home,
		"shell":          a.Shell,
		"gecos":          a.Gecos,
		"dir_disabled":   disabled,
		"pending_uid":    pendingUID,
		"pending_gid":    pendingGID,
		"pending_since":  nssPendingSince(row.PendingSince, now, [2]*uint32{pendingUID, row.PendingUID}, [2]*uint32{pendingGID, row.PendingGID}),
		"missing_since":  nil,
		"deleted_at":     nil,
		"last_synced_at": now,
		"updated_at":     now,
	}
	if a.DirectoryID != "" {
		updates["directory_id"] = a.DirectoryID
	}

	var blocked []model.NSSCollision
	if a.Name != row.Name {
		holder, err := accountTable().holderOf(tx, "name", a.Name, row.ID)
		if err != nil {
			return nil, nil, false, err
		}
		if holder != nil {
			rowID := row.ID
			blocked = append(blocked, model.NSSCollision{
				Kind: model.NSSCollisionKindUserName, HolderID: holder.ID, OtherDN: a.DN, Value: a.Name,
				OtherID: &rowID, OtherName: a.Name, OtherNumber: a.UID, FirstSeenAt: now, LastSeenAt: now,
			})
		} else {
			updates["name"] = a.Name
			ch.renamed = true
		}
	}
	if a.DN != row.DN {
		if err := s.releaseDN(ctx, tx, accountTable(), a.DN, row.ID, now); err != nil {
			return nil, nil, false, err
		}
		updates["dn"] = a.DN
	}

	if err := tx.Model(&model.PosixUser{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
		return nil, nil, false, fmt.Errorf("update the account row: %w", err)
	}
	var updated model.PosixUser
	if err := tx.First(&updated, "id = ?", row.ID).Error; err != nil {
		return nil, nil, false, fmt.Errorf("re-read the account row: %w", err)
	}
	return &updated, blocked, a.GecosCleaned && a.Gecos != previousGecos, nil
}

// storeGroup writes one group entry as storeAccount writes an account,
// for the gid and the group name. With an index it also replaces the
// group's membership rows with every member that resolves to a stored
// account; without one (a group an account's membership search found) its
// members are left as they are. gone is the read's evidence for
// re-anchoring a renamed or moved group (see renameTarget), nil for none;
// w is the path, for the audit events recorded with the write.
func (s *NSSDirectoryService) storeGroup(ctx context.Context, group *nssGroup, contenders []*nssGroup, complete nssKinds, index *nssMemberIndex, gone *nssGroupGone, w nssWrite) (*nssStoredGroup, error) {
	now := s.now().UTC()
	out := &nssStoredGroup{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return s.storeGroupTx(ctx, tx, group, contenders, complete, index, gone, w, now, out)
	})
	if err != nil {
		return nil, fmt.Errorf("store the directory group %s: %w", group.DN, err)
	}
	s.flush(&out.audit)

	if out.renamed != nil {
		s.log.InfoContext(ctx, "re-anchored an NSS group renamed or moved in the directory",
			"gid", out.row.GID, "old_name", out.renamed.oldName, "name", out.row.Name,
			"old_dn", out.renamed.oldDN, "dn", out.row.DN, "detected_by", out.renamed.via)
	}
	s.raiseCollisionAlerts(ctx, groupTable(), out.observed)
	if out.row != nil && out.row.PendingGID != nil {
		s.raisePendingAlert(ctx, groupTable(), out.row.ID, out.row.Name, out.row.DN, pendingDetail(out.row.GID, out.row.PendingGID, nil, nil))
	}
	return out, nil
}

// storeGroupTx is storeGroup's transaction.
func (s *NSSDirectoryService) storeGroupTx(ctx context.Context, tx *gorm.DB, group *nssGroup, contenders []*nssGroup, complete nssKinds, index *nssMemberIndex, gone *nssGroupGone, w nssWrite, now time.Time, out *nssStoredGroup) error {
	stored, contended, err := s.writeGroup(ctx, tx, group, contenders, gone, now, out)
	if err != nil {
		return err
	}
	refs := make([]nssEntryRef, 0, len(contenders))
	for _, c := range contenders {
		refs = append(refs, c.ref())
	}
	if out.observed, err = s.recordCollisions(tx, groupTable(), group.ref(), groupRowRef(stored), contended, refs, complete, now); err != nil {
		return err
	}
	if stored == nil {
		return nil
	}
	var members []uint32
	if index != nil {
		members = index.resolve(group.Members)
	}
	out.row = &model.PosixGroup{}
	if err := tx.First(out.row, "id = ?", stored.ID).Error; err != nil {
		return err
	}
	if err := s.finishGroupRename(tx, group, w, now, out); err != nil {
		return err
	}
	if err := s.recordGroupEvents(tx, group, w, len(members), out); err != nil {
		return err
	}
	if index == nil {
		return nil
	}
	return s.replaceGroupMembers(tx, out.row, members, w, now, out)
}

// writeGroup is storeGroup's row write, as writeAccount is storeAccount's,
// except that an entry no row anchors may be a stored group renamed or
// moved (renameTarget), whose row it then re-anchors.
func (s *NSSDirectoryService) writeGroup(ctx context.Context, tx *gorm.DB, group *nssGroup, contenders []*nssGroup, gone *nssGroupGone, now time.Time, out *nssStoredGroup) (*model.PosixGroup, []model.NSSCollision, error) {
	match, err := groupTable().match(tx, s.config.LDAP.IDAttribute != "", group.ref())
	if err != nil {
		return nil, nil, err
	}
	if match != nil {
		return s.updateGroup(ctx, tx, match.ID, group, now, &out.change)
	}
	target, err := s.renameTarget(tx, group, contenders, gone)
	if err != nil {
		return nil, nil, err
	}
	if target != nil {
		return s.reanchorGroup(ctx, tx, target, group, gone.via, now, out)
	}
	contended, err := s.heldValues(tx, groupTable(), group.ref(), nil, now)
	if err != nil || len(contended) > 0 {
		return nil, contended, err
	}
	stored, err := s.insertGroup(ctx, tx, group, now)
	if err != nil {
		return nil, nil, err
	}
	out.created = true
	return stored, nil, nil
}

// insertGroup writes a new group row, as insertAccount does.
func (s *NSSDirectoryService) insertGroup(ctx context.Context, tx *gorm.DB, g *nssGroup, now time.Time) (*model.PosixGroup, error) {
	if err := s.releaseDN(ctx, tx, groupTable(), g.DN, "", now); err != nil {
		return nil, err
	}
	row := &model.PosixGroup{
		ID:           uuid.NewString(),
		GID:          g.GID,
		Name:         g.Name,
		DirectoryID:  g.DirectoryID,
		DN:           g.DN,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastSyncedAt: now,
	}
	if err := tx.Create(row).Error; err != nil {
		return nil, fmt.Errorf("insert the group row: %w", err)
	}
	return row, nil
}

// updateGroup brings the matched group row id in line with the entry, as
// updateAccount does, filling ch.
func (s *NSSDirectoryService) updateGroup(ctx context.Context, tx *gorm.DB, id string, g *nssGroup, now time.Time, ch *nssRowChange) (*model.PosixGroup, []model.NSSCollision, error) {
	var row model.PosixGroup
	if err := tx.First(&row, "id = ?", id).Error; err != nil {
		return nil, nil, fmt.Errorf("read the matched group: %w", err)
	}
	*ch = nssRowChange{oldName: row.Name, oldDN: row.DN, missingSince: row.MissingSince, deletedAt: row.DeletedAt}

	pendingGID := nssPending(row.GID, g.GID, row.PendingGID)
	updates := map[string]any{
		"pending_gid":    pendingGID,
		"pending_since":  nssPendingSince(row.PendingSince, now, [2]*uint32{pendingGID, row.PendingGID}),
		"missing_since":  nil,
		"deleted_at":     nil,
		"last_synced_at": now,
		"updated_at":     now,
	}
	if g.DirectoryID != "" {
		updates["directory_id"] = g.DirectoryID
	}

	var blocked []model.NSSCollision
	if g.Name != row.Name {
		holder, err := groupTable().holderOf(tx, "name", g.Name, row.ID)
		if err != nil {
			return nil, nil, err
		}
		if holder != nil {
			rowID := row.ID
			blocked = append(blocked, model.NSSCollision{
				Kind: model.NSSCollisionKindGroupName, HolderID: holder.ID, OtherDN: g.DN, Value: g.Name,
				OtherID: &rowID, OtherName: g.Name, OtherNumber: g.GID, FirstSeenAt: now, LastSeenAt: now,
			})
		} else {
			updates["name"] = g.Name
			ch.renamed = true
		}
	}
	if g.DN != row.DN {
		if err := s.releaseDN(ctx, tx, groupTable(), g.DN, row.ID, now); err != nil {
			return nil, nil, err
		}
		updates["dn"] = g.DN
	}

	if err := tx.Model(&model.PosixGroup{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
		return nil, nil, fmt.Errorf("update the group row: %w", err)
	}
	var updated model.PosixGroup
	if err := tx.First(&updated, "id = ?", row.ID).Error; err != nil {
		return nil, nil, fmt.Errorf("re-read the group row: %w", err)
	}
	return &updated, blocked, nil
}

// gidHolderElsewhere is the stored group a directory group no row anchors
// may be a rename or move of: the row holding its gid, tombstones
// included, at another DN, and with no directory_id that says it is
// another entry (sameEntry). Nil when there is none. A row whose DN was
// released is anchored by its directory_id alone, so it never qualifies.
func (s *NSSDirectoryService) gidHolderElsewhere(tx *gorm.DB, g *nssGroup) (*nssRowRef, error) {
	holder, err := groupTable().holderOf(tx, "gid", g.GID, "")
	if err != nil || holder == nil {
		return nil, err
	}
	if holder.DN == "" || normalizeDN(holder.DN) == normalizeDN(g.DN) || !sameEntry(holder.DirectoryID, g.DirectoryID) {
		return nil, nil //nolint:nilnil // no candidate is an answer, not an error.
	}
	return holder, nil
}

// renameCandidate is gidHolderElsewhere for an entry no row anchors, and
// nil for one a row does: the read-time half of the rename check, which
// decides whether the directory is worth asking about the holder's DN.
func (s *NSSDirectoryService) renameCandidate(tx *gorm.DB, g *nssGroup) (*nssRowRef, error) {
	match, err := groupTable().match(tx, s.config.LDAP.IDAttribute != "", g.ref())
	if err != nil || match != nil {
		return nil, err
	}
	return s.gidHolderElsewhere(tx, g)
}

// renameTarget is the stored group that g, an entry no row anchors, is a
// rename or move of, or nil. It is the gid's holder at another DN
// (gidHolderElsewhere) when the read's evidence says the holder's entry
// has gone and no other entry the read found carries the gid: two entries
// contending for a held gid are a collision, whatever became of the old
// one.
func (s *NSSDirectoryService) renameTarget(tx *gorm.DB, g *nssGroup, contenders []*nssGroup, gone *nssGroupGone) (*nssRowRef, error) {
	if gone == nil {
		return nil, nil //nolint:nilnil // without evidence nothing is a rename.
	}
	for _, c := range contenders {
		if c.GID == g.GID {
			return nil, nil //nolint:nilnil // a second entry with the gid is a collision.
		}
	}
	holder, err := s.gidHolderElsewhere(tx, g)
	if err != nil || holder == nil || !gone.gone(holder.DN) {
		return nil, err
	}
	return holder, nil
}

// reanchorGroup makes the stored group target follow g, its entry renamed
// or moved in the directory with the gid kept. The collisions the move
// resolves are deleted: g contending with target, which is now its own
// row, and every pair the old entry contended in, since it has gone. Then
// the row is updated as a matched one would be: it keeps its id and gid
// (and so its membership rows), takes g's DN, directory_id and name (unless
// another row holds the name, which is the usual blocked rename), and
// loses missing_since and any tombstone. out.renamed records it for the
// audit event.
func (s *NSSDirectoryService) reanchorGroup(ctx context.Context, tx *gorm.DB, target *nssRowRef, g *nssGroup, via string, now time.Time, out *nssStoredGroup) (*model.PosixGroup, []model.NSSCollision, error) {
	var resolved []model.NSSCollision
	if err := tx.Where("kind IN ?", groupTable().kinds()).
		Where(tx.Where("holder_id = ? AND other_dn = ?", target.ID, g.DN).Or("other_dn = ?", target.DN)).
		Find(&resolved).Error; err != nil {
		return nil, nil, fmt.Errorf("read the collisions the rename of %s resolves: %w", target.DN, err)
	}
	if err := deleteCollisions(tx, resolved); err != nil {
		return nil, nil, err
	}
	stored, blocked, err := s.updateGroup(ctx, tx, target.ID, g, now, &out.change)
	if err != nil {
		return nil, nil, err
	}
	out.renamed = &nssGroupRename{
		oldName: out.change.oldName, oldDN: out.change.oldDN, via: via,
		revived: out.change.deletedAt != nil, resolved: resolved,
	}
	return stored, blocked, nil
}

// finishGroupRename is a re-anchoring's last step in its transaction, once
// out.row is the row as stored: the other rows the deleted collisions
// withheld are served again unless another still names them, and the
// nss.group_renamed event is recorded with the write. Nothing when the
// store re-anchored nothing.
func (s *NSSDirectoryService) finishGroupRename(tx *gorm.DB, group *nssGroup, w nssWrite, now time.Time, out *nssStoredGroup) error {
	if out.renamed == nil {
		return nil
	}
	affected := map[string]bool{}
	markAffected(affected, out.renamed.resolved)
	if err := s.recomputeWithheld(tx, groupTable(), affected, now); err != nil {
		return err
	}
	return s.record(tx, &out.audit, groupRenamedEvent(out.renamed, out.row, group, w.via))
}

// nssGroupMatched is the detected_by of nss.group_renamed for a group its
// anchor (directory_id or DN) matched, whose name changed in place.
const nssGroupMatched = "matched"

// groupRenamedEvent is the nss.group_renamed event for one renamed or
// re-anchored group, naming the row before and after. new_name is the
// directory's; name_blocked says another row holds it, so the row kept
// its old name and is withheld until that clears.
func groupRenamedEvent(r *nssGroupRename, row *model.PosixGroup, g *nssGroup, via string) AuditEvent {
	return AuditEvent{
		Action:  AuditNSSGroupRenamed,
		NSSRefs: []NSSAuditRef{groupTable().auditRef(row.ID)},
		Detail: map[string]any{
			"table":        groupTable().table,
			"gid":          row.GID,
			"old_name":     r.oldName,
			"new_name":     g.Name,
			"old_dn":       r.oldDN,
			"new_dn":       row.DN,
			"detected_by":  r.via,
			"name_blocked": row.Name != g.Name,
			"revived":      r.revived,
			"via":          via,
		},
	}
}

// recordGroupEvents records the lifecycle events of one group write:
// nss.group_created (with the members it is stored with, unless the run is
// the first import), or for a matched row nss.group_renamed when its name
// followed the directory in place and nss.group_revived when it was
// missing or a tombstone.
func (s *NSSDirectoryService) recordGroupEvents(tx *gorm.DB, group *nssGroup, w nssWrite, memberCount int, out *nssStoredGroup) error {
	ref := *groupRowRef(out.row)
	if out.created {
		if w.importing {
			return nil
		}
		return s.recordRowEvent(tx, &out.audit, groupTable(), AuditNSSGroupCreated, ref, nil, w.via, map[string]any{"member_count": memberCount})
	}
	if out.renamed == nil && out.change.renamed {
		r := &nssGroupRename{oldName: out.change.oldName, oldDN: out.change.oldDN, via: nssGroupMatched, revived: out.change.deletedAt != nil}
		if err := s.record(tx, &out.audit, groupRenamedEvent(r, out.row, group, w.via)); err != nil {
			return err
		}
	}
	if !out.change.revived() {
		return nil
	}
	return s.recordRowEvent(tx, &out.audit, groupTable(), AuditNSSGroupRevived, ref, nil, w.via, revivedDetail(out.change.missingSince, out.change.deletedAt))
}

// recordAccountEvents is recordGroupEvents for an account: created,
// renamed (nss.account_renamed) and revived.
func (s *NSSDirectoryService) recordAccountEvents(tx *gorm.DB, w nssWrite, out *nssStoredAccount) error {
	row := out.row
	ref := *accountRowRef(row)
	if out.created {
		if w.importing {
			return nil
		}
		return s.recordRowEvent(tx, &out.audit, accountTable(), AuditNSSAccountCreated, ref, row.UserID, w.via, map[string]any{"gid": row.GID})
	}
	if out.change.renamed {
		if err := s.recordRowEvent(tx, &out.audit, accountTable(), AuditNSSAccountRenamed, ref, row.UserID, w.via,
			map[string]any{"old_name": out.change.oldName, "new_name": row.Name, "old_dn": out.change.oldDN}); err != nil {
			return err
		}
	}
	if !out.change.revived() {
		return nil
	}
	return s.recordRowEvent(tx, &out.audit, accountTable(), AuditNSSAccountRevived, ref, row.UserID, w.via, revivedDetail(out.change.missingSince, out.change.deletedAt))
}

// replaceGroupMembers makes row's membership rows exactly uids, recording
// nss.group_members_changed when the set changed. A group created by this
// write has its members in nss.group_created instead. Only the difference
// is written, so a membership that stays keeps its added_at; a new one
// gets memberAddedAt's.
func (s *NSSDirectoryService) replaceGroupMembers(tx *gorm.DB, row *model.PosixGroup, uids []uint32, w nssWrite, now time.Time, out *nssStoredGroup) error {
	after := nssMembers{}
	for _, uid := range uids {
		after.add(row.GID, uid)
	}
	addedAt := memberAddedAt(w, now)
	if out.created {
		// Every membership of a new group is new: a row already under its
		// gid is left over from an earlier holder of the number.
		if err := tx.Where("gid = ?", row.GID).Delete(&model.PosixGroupMember{}).Error; err != nil {
			return fmt.Errorf("clear left-over members of gid %d: %w", row.GID, err)
		}
		return applyMemberDeltas(tx, memberDeltas(nssMembers{}, after), addedAt)
	}
	before, err := readMembers(tx, "gid = ?", row.GID)
	if err != nil {
		return err
	}
	deltas := memberDeltas(before, after)
	if err := applyMemberDeltas(tx, deltas, addedAt); err != nil {
		return err
	}
	return s.recordMemberChanges(tx, &out.audit, deltas, nssMemberChange{
		via: w.via, groupNames: map[uint32]string{row.GID: row.Name}, groupIDs: map[uint32]string{row.GID: row.ID},
	})
}

// heldValues is the collisions a new entry runs into: a row, tombstones
// included, that already holds its number or its name. stored is the
// entry's own row when it has one, so it is never its own holder.
func (s *NSSDirectoryService) heldValues(tx *gorm.DB, t nssTable, e nssEntryRef, stored *nssRowRef, now time.Time) ([]model.NSSCollision, error) {
	except := ""
	var otherID *string
	if stored != nil {
		except = stored.ID
		id := stored.ID
		otherID = &id
	}
	var out []model.NSSCollision
	checks := []struct {
		kind   model.NSSCollisionKind
		column string
		value  any
		text   string
	}{
		{t.numberKind, t.numberColumn, e.Number, strconv.FormatUint(uint64(e.Number), 10)},
		{t.nameKind, "name", e.Name, e.Name},
	}
	for _, c := range checks {
		holder, err := t.holderOf(tx, c.column, c.value, except)
		if err != nil {
			return nil, err
		}
		if holder != nil {
			out = append(out, model.NSSCollision{
				Kind: c.kind, HolderID: holder.ID, OtherDN: e.DN, Value: c.text, OtherID: otherID,
				OtherName: e.Name, OtherNumber: e.Number, FirstSeenAt: now, LastSeenAt: now,
			})
		}
	}
	return out, nil
}

// recordCollisions writes the collisions one read saw for entry e and
// withholds every row they involve. contended are the collisions in which
// e is the other entry because a row holds what it carries; contenders are
// other entries the read found carrying e's number or name, from which
// the collisions with e's own row as holder are worked out (and, for a
// contender stored as the holder, e as the other entry). For the kinds the
// read saw completely, a collision with e as the other entry that the read
// no longer shows is deleted. Returns every collision written, for the
// alerts.
func (s *NSSDirectoryService) recordCollisions(tx *gorm.DB, t nssTable, e nssEntryRef, stored *nssRowRef, contended []model.NSSCollision, contenders []nssEntryRef, complete nssKinds, now time.Time) ([]model.NSSCollision, error) {
	pairs := slices.Clone(contended)
	for _, c := range contenders {
		if strings.EqualFold(c.DN, e.DN) {
			continue
		}
		found, err := s.contenderPairs(tx, t, e, stored, c, now)
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, found...)
	}
	pairs = uniqueCollisions(pairs)

	affected := map[string]bool{}
	if stored != nil {
		if stored.withheld {
			affected[stored.ID] = true
		}
		// A holder whose value changed (the stored name followed a rename)
		// no longer holds what its old collisions contest.
		pruned, err := pruneStaleHolds(tx, t, stored)
		if err != nil {
			return nil, err
		}
		markAffected(affected, pruned)
	}

	seen := map[string]bool{strings.ToLower(e.DN): true}
	for _, c := range contenders {
		seen[strings.ToLower(c.DN)] = true
	}
	stale, err := staleCollisions(tx, t, e, pairs, complete, seen)
	if err != nil {
		return nil, err
	}
	if err := deleteCollisions(tx, stale); err != nil {
		return nil, err
	}
	markAffected(affected, stale)

	if len(pairs) > 0 {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "kind"}, {Name: "holder_id"}, {Name: "other_dn"}},
			DoUpdates: clause.AssignmentColumns([]string{"value", "other_id", "other_name", "other_number", "last_seen_at"}),
		}).Create(&pairs).Error; err != nil {
			return nil, fmt.Errorf("record the collisions of %s: %w", e.DN, err)
		}
	}
	markAffected(affected, pairs)

	if err := s.recomputeWithheld(tx, t, affected, now); err != nil {
		return nil, err
	}
	return pairs, nil
}

// staleCollisions is the stored collisions this read shows to be over,
// for the kinds complete says it saw in full: one with e as the other entry
// that pairs no longer contains, and one over e's own number or name whose
// other entry the read did not find carrying it. seen holds the lower-cased
// DNs of every entry the read found carrying e's values, e's own included.
func staleCollisions(tx *gorm.DB, t nssTable, e nssEntryRef, pairs []model.NSSCollision, complete nssKinds, seen map[string]bool) ([]model.NSSCollision, error) {
	number := strconv.FormatUint(uint64(e.Number), 10)
	var existing []model.NSSCollision
	if err := tx.Where("kind IN ?", t.kinds()).
		Where(tx.Where("other_dn = ?", e.DN).
			Or("kind = ? AND value = ?", t.numberKind, number).
			Or("kind = ? AND value = ?", t.nameKind, e.Name)).
		Find(&existing).Error; err != nil {
		return nil, fmt.Errorf("read the collisions of %s: %w", e.DN, err)
	}
	keep := map[string]bool{}
	for _, p := range pairs {
		keep[collisionKey(p)] = true
	}
	var stale []model.NSSCollision
	for _, c := range existing {
		if keep[collisionKey(c)] || !complete.has(t, c.Kind) {
			continue
		}
		if strings.EqualFold(c.OtherDN, e.DN) || !seen[strings.ToLower(c.OtherDN)] {
			stale = append(stale, c)
		}
	}
	return stale, nil
}

// contenderPairs works out the collisions between entry e and one other
// entry c that carries its number or its name. The row holding the shared
// value decides the pair: when it is e's own row, c is the other entry;
// when it is c's row, e is. A third row holding it (a tombstone, or a row
// whose entry now carries another value) is the new entry's ordinary held
// value collision, found by heldValues, and is not repeated here.
func (s *NSSDirectoryService) contenderPairs(tx *gorm.DB, t nssTable, e nssEntryRef, stored *nssRowRef, c nssEntryRef, now time.Time) ([]model.NSSCollision, error) {
	cRow, err := t.match(tx, s.config.LDAP.IDAttribute != "", c)
	if err != nil {
		return nil, err
	}
	if cRow != nil && stored != nil && cRow.ID == stored.ID {
		return nil, nil
	}

	shared := []struct {
		kind   model.NSSCollisionKind
		column string
		value  any
		text   string
		ok     bool
	}{
		{t.numberKind, t.numberColumn, e.Number, strconv.FormatUint(uint64(e.Number), 10), c.Number == e.Number},
		{t.nameKind, "name", e.Name, e.Name, c.Name == e.Name},
	}
	var out []model.NSSCollision
	for _, sh := range shared {
		if !sh.ok {
			continue
		}
		holder, err := t.holderOf(tx, sh.column, sh.value, "")
		if err != nil {
			return nil, err
		}
		switch {
		case holder == nil:
			continue
		case stored != nil && holder.ID == stored.ID:
			out = append(out, model.NSSCollision{
				Kind: sh.kind, HolderID: holder.ID, OtherDN: c.DN, Value: sh.text, OtherID: refID(cRow),
				OtherName: c.Name, OtherNumber: c.Number, FirstSeenAt: now, LastSeenAt: now,
			})
		case cRow != nil && holder.ID == cRow.ID:
			out = append(out, model.NSSCollision{
				Kind: sh.kind, HolderID: holder.ID, OtherDN: e.DN, Value: sh.text, OtherID: refID(stored),
				OtherName: e.Name, OtherNumber: e.Number, FirstSeenAt: now, LastSeenAt: now,
			})
		}
	}
	return out, nil
}

// refID is the id of r as a nullable column value.
func refID(r *nssRowRef) *string {
	if r == nil {
		return nil
	}
	id := r.ID
	return &id
}

// collisionKey is a collision's primary key as one string.
func collisionKey(c model.NSSCollision) string {
	return string(c.Kind) + "\x00" + c.HolderID + "\x00" + c.OtherDN
}

// uniqueCollisions drops repeats of one primary key, keeping the first.
func uniqueCollisions(pairs []model.NSSCollision) []model.NSSCollision {
	seen := map[string]bool{}
	out := pairs[:0]
	for _, p := range pairs {
		if seen[collisionKey(p)] {
			continue
		}
		seen[collisionKey(p)] = true
		out = append(out, p)
	}
	return out
}

// markAffected adds every row the collisions name to affected.
func markAffected(affected map[string]bool, pairs []model.NSSCollision) {
	for _, p := range pairs {
		affected[p.HolderID] = true
		if p.OtherID != nil {
			affected[*p.OtherID] = true
		}
	}
}

// pruneStaleHolds deletes the collisions row holds for a value it no
// longer has, and returns them.
func pruneStaleHolds(tx *gorm.DB, t nssTable, row *nssRowRef) ([]model.NSSCollision, error) {
	number := strconv.FormatUint(uint64(row.Number), 10)
	var stale []model.NSSCollision
	if err := tx.Where("holder_id = ? AND ((kind = ? AND value <> ?) OR (kind = ? AND value <> ?))",
		row.ID, t.numberKind, number, t.nameKind, row.Name).Find(&stale).Error; err != nil {
		return nil, fmt.Errorf("read the collisions %s holds: %w", row.ID, err)
	}
	return stale, deleteCollisions(tx, stale)
}

// deleteCollisions deletes the given collisions by primary key.
func deleteCollisions(tx *gorm.DB, pairs []model.NSSCollision) error {
	for _, p := range pairs {
		if err := tx.Where("kind = ? AND holder_id = ? AND other_dn = ?", p.Kind, p.HolderID, p.OtherDN).
			Delete(&model.NSSCollision{}).Error; err != nil {
			return fmt.Errorf("delete a resolved collision: %w", err)
		}
	}
	return nil
}

// recomputeWithheld sets or clears withheld_at and withheld_reason on each
// row of t in ids from the collisions that name it now: withheld while one
// does (keeping the time it was first withheld), served again once none
// does.
func (s *NSSDirectoryService) recomputeWithheld(tx *gorm.DB, t nssTable, ids map[string]bool, now time.Time) error {
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		var pairs []model.NSSCollision
		if err := tx.Where("(holder_id = ? OR other_id = ?) AND kind IN ?", id, id, t.kinds()).
			Order("kind, other_dn").Find(&pairs).Error; err != nil {
			return fmt.Errorf("read the collisions of %s: %w", id, err)
		}
		if len(pairs) == 0 {
			if err := tx.Table(t.table).Where("id = ? AND withheld_at IS NOT NULL", id).
				Updates(map[string]any{"withheld_at": nil, "withheld_reason": "", "updated_at": now}).Error; err != nil {
				return fmt.Errorf("release a withheld %s: %w", t.label, err)
			}
			continue
		}
		reason, err := withheldReason(tx, t, id, pairs)
		if err != nil {
			return err
		}
		if err := tx.Table(t.table).Where("id = ?", id).
			Updates(map[string]any{
				"withheld_at":     gorm.Expr("COALESCE(withheld_at, ?)", now),
				"withheld_reason": reason,
			}).Error; err != nil {
			return fmt.Errorf("withhold a %s: %w", t.label, err)
		}
	}
	return nil
}

// withheldReason is the operator's one-sentence account of why row id is
// withheld, from the first of its collisions, with a count of the rest.
func withheldReason(tx *gorm.DB, t nssTable, id string, pairs []model.NSSCollision) (string, error) {
	p := pairs[0]
	what := "name " + p.Value
	if p.Kind == t.numberKind {
		what = t.numberColumn + " " + p.Value
	}
	var reason string
	if p.HolderID == id {
		reason = fmt.Sprintf("%s is also carried by %s", what, p.OtherDN)
	} else {
		holders, err := t.refs(tx, "id = ?", p.HolderID)
		if err != nil {
			return "", err
		}
		holderDN := "another " + t.label
		if len(holders) > 0 && holders[0].DN != "" {
			holderDN = holders[0].DN
		}
		reason = fmt.Sprintf("the directory gives this entry %s, which %s holds", what, holderDN)
	}
	if len(pairs) > 1 {
		reason += fmt.Sprintf(" (and %d more)", len(pairs)-1)
	}
	return reason, nil
}

// nssMemberIndex resolves a group's member values to stored uids: names
// byte for byte, DNs as DNs (normalizeDN).
type nssMemberIndex struct {
	dns     bool
	byName  map[string]uint32
	byDN    map[string]uint32
	skipped int
}

// loadMemberIndex reads every stored account's uid, name and DN, once per
// run or per on-demand group read, so a group's members resolve without a
// query each.
func (s *NSSDirectoryService) loadMemberIndex(ctx context.Context) (*nssMemberIndex, error) {
	type row struct {
		UID  uint32 `gorm:"column:uid"`
		Name string `gorm:"column:name"`
		DN   string `gorm:"column:dn"`
	}
	var rows []row
	if err := s.db.WithContext(ctx).Model(&model.PosixUser{}).Select("uid, name, dn").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read the stored accounts for group membership: %w", err)
	}
	index := &nssMemberIndex{
		dns:    nssMembersAreDNs(s.config.NSS.Attributes.GroupMember),
		byName: make(map[string]uint32, len(rows)),
		byDN:   make(map[string]uint32, len(rows)),
	}
	for _, r := range rows {
		index.byName[r.Name] = r.UID
		if r.DN != "" {
			index.byDN[normalizeDN(r.DN)] = r.UID
		}
	}
	return index, nil
}

// resolve returns the sorted, distinct uids of the members that are stored
// accounts. A member that is not (not a POSIX account, or one the sync has
// not stored) is left out.
func (i *nssMemberIndex) resolve(members []string) []uint32 {
	seen := map[uint32]bool{}
	for _, m := range members {
		var uid uint32
		var ok bool
		if i.dns {
			uid, ok = i.byDN[normalizeDN(memberDN(m))]
		} else {
			uid, ok = i.byName[m]
		}
		if !ok {
			i.skipped++
			continue
		}
		seen[uid] = true
	}
	return slices.Sorted(maps.Keys(seen))
}

// memberAddedAt is the added_at a membership row stored now gets: nil
// during the first full import (the run that records nss.initial_import),
// meaning the membership was present before it, and now after that.
func memberAddedAt(w nssWrite, now time.Time) *time.Time {
	if w.importing {
		return nil
	}
	return &now
}

// applyMemberDeltas writes deltas, the change from the stored membership
// rows to the wanted ones: it deletes each removed row and inserts each
// added one with addedAt. A row that stays is not touched, so its added_at
// survives every write that leaves the membership in place.
func applyMemberDeltas(tx *gorm.DB, deltas map[uint32]nssMemberDelta, addedAt *time.Time) error {
	var rows []model.PosixGroupMember
	for _, gid := range slices.Sorted(maps.Keys(deltas)) {
		d := deltas[gid]
		for chunk := range slices.Chunk(d.removed, nssBatchSize) {
			if err := tx.Where("gid = ? AND uid IN ?", gid, chunk).Delete(&model.PosixGroupMember{}).Error; err != nil {
				return fmt.Errorf("remove members of gid %d: %w", gid, err)
			}
		}
		for _, uid := range d.added {
			rows = append(rows, model.PosixGroupMember{GID: gid, UID: uid, AddedAt: addedAt})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	if err := tx.CreateInBatches(rows, nssBatchSize).Error; err != nil {
		return fmt.Errorf("write new memberships: %w", err)
	}
	return nil
}
