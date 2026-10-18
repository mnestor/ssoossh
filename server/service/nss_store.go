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
		numberKind: model.NSSCollisionUID, nameKind: model.NSSCollisionUserName,
		label: "account",
	}
}

// groupTable describes posix_groups.
func groupTable() nssTable {
	return nssTable{
		table: "posix_groups", numberColumn: "gid",
		numberKind: model.NSSCollisionGID, nameKind: model.NSSCollisionGroupName,
		label: "group",
	}
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
}

// nssStoredGroup is what storing one group entry did.
type nssStoredGroup struct {
	row      *model.PosixGroup
	created  bool
	observed []model.NSSCollision
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
// saw every such entry. The alerts are raised after the write commits.
func (s *NSSDirectoryService) storeAccount(ctx context.Context, account *nssAccount, disabled bool, contenders []*nssAccount, complete nssKinds) (*nssStoredAccount, error) {
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
		return tx.First(out.row, "id = ?", stored.ID).Error
	})
	if err != nil {
		return nil, fmt.Errorf("store the directory account %s: %w", account.DN, err)
	}

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
		return s.updateAccount(ctx, tx, match.ID, account, disabled, now)
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
func (s *NSSDirectoryService) updateAccount(ctx context.Context, tx *gorm.DB, id string, a *nssAccount, disabled bool, now time.Time) (*model.PosixUser, []model.NSSCollision, bool, error) {
	var row model.PosixUser
	if err := tx.First(&row, "id = ?", id).Error; err != nil {
		return nil, nil, false, fmt.Errorf("read the matched account: %w", err)
	}
	previousGecos := row.Gecos

	pendingUID := nssPending(row.UID, a.UID, row.PendingUID)
	pendingGID := nssPending(row.GID, a.GID, row.PendingGID)
	updates := map[string]any{
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
				Kind: model.NSSCollisionUserName, HolderID: holder.ID, OtherDN: a.DN, Value: a.Name,
				OtherID: &rowID, OtherName: a.Name, OtherNumber: a.UID, FirstSeenAt: now, LastSeenAt: now,
			})
		} else {
			updates["name"] = a.Name
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
// members are left as they are.
func (s *NSSDirectoryService) storeGroup(ctx context.Context, group *nssGroup, contenders []*nssGroup, complete nssKinds, index *nssMemberIndex) (*nssStoredGroup, error) {
	now := s.now().UTC()
	out := &nssStoredGroup{}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		stored, contended, err := s.writeGroup(ctx, tx, group, now, out)
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
		if index != nil {
			if err := replaceMembersOfGroup(tx, stored.GID, index.resolve(group.Members)); err != nil {
				return err
			}
		}
		out.row = &model.PosixGroup{}
		return tx.First(out.row, "id = ?", stored.ID).Error
	})
	if err != nil {
		return nil, fmt.Errorf("store the directory group %s: %w", group.DN, err)
	}

	s.raiseCollisionAlerts(ctx, groupTable(), out.observed)
	if out.row != nil && out.row.PendingGID != nil {
		s.raisePendingAlert(ctx, groupTable(), out.row.ID, out.row.Name, out.row.DN, pendingDetail(out.row.GID, out.row.PendingGID, 0, nil))
	}
	return out, nil
}

// writeGroup is storeGroup's row write, as writeAccount is storeAccount's.
func (s *NSSDirectoryService) writeGroup(ctx context.Context, tx *gorm.DB, group *nssGroup, now time.Time, out *nssStoredGroup) (*model.PosixGroup, []model.NSSCollision, error) {
	match, err := groupTable().match(tx, s.config.LDAP.IDAttribute != "", group.ref())
	if err != nil {
		return nil, nil, err
	}
	if match != nil {
		return s.updateGroup(ctx, tx, match.ID, group, now)
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
// updateAccount does.
func (s *NSSDirectoryService) updateGroup(ctx context.Context, tx *gorm.DB, id string, g *nssGroup, now time.Time) (*model.PosixGroup, []model.NSSCollision, error) {
	var row model.PosixGroup
	if err := tx.First(&row, "id = ?", id).Error; err != nil {
		return nil, nil, fmt.Errorf("read the matched group: %w", err)
	}

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
				Kind: model.NSSCollisionGroupName, HolderID: holder.ID, OtherDN: g.DN, Value: g.Name,
				OtherID: &rowID, OtherName: g.Name, OtherNumber: g.GID, FirstSeenAt: now, LastSeenAt: now,
			})
		} else {
			updates["name"] = g.Name
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

// replaceMembersOfGroup makes gid's membership rows exactly uids.
func replaceMembersOfGroup(tx *gorm.DB, gid uint32, uids []uint32) error {
	if err := tx.Where("gid = ?", gid).Delete(&model.PosixGroupMember{}).Error; err != nil {
		return fmt.Errorf("clear the members of gid %d: %w", gid, err)
	}
	if len(uids) == 0 {
		return nil
	}
	rows := make([]model.PosixGroupMember, 0, len(uids))
	for _, uid := range uids {
		rows = append(rows, model.PosixGroupMember{GID: gid, UID: uid})
	}
	if err := tx.CreateInBatches(rows, nssBatchSize).Error; err != nil {
		return fmt.Errorf("write the members of gid %d: %w", gid, err)
	}
	return nil
}

// replaceMembershipsOfUser makes uid's membership rows exactly gids.
func replaceMembershipsOfUser(tx *gorm.DB, uid uint32, gids []uint32) error {
	if err := tx.Where("uid = ?", uid).Delete(&model.PosixGroupMember{}).Error; err != nil {
		return fmt.Errorf("clear the memberships of uid %d: %w", uid, err)
	}
	slices.Sort(gids)
	gids = slices.Compact(gids)
	if len(gids) == 0 {
		return nil
	}
	rows := make([]model.PosixGroupMember, 0, len(gids))
	for _, gid := range gids {
		rows = append(rows, model.PosixGroupMember{GID: gid, UID: uid})
	}
	if err := tx.CreateInBatches(rows, nssBatchSize).Error; err != nil {
		return fmt.Errorf("write the memberships of uid %d: %w", uid, err)
	}
	return nil
}
