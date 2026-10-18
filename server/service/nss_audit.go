package service

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// How a write to the NSS tables was reached, as the "via" of the audit
// events it records (decided 2026-10-18 by the owner).
const (
	nssViaFullSync         = "full_sync"
	nssViaIncrementalSync  = "incremental_sync"
	nssViaOnDemand         = "on_demand"
	nssViaLogin            = "login"
	nssViaTombstoned       = "tombstoned"
	nssViaIDChangeAccepted = "id_change_accepted"
	nssViaTombstonePurged  = "tombstone_purged"
)

// nssMemberListCap bounds the added and removed lists of one
// nss.group_members_changed event; the counts are always the totals.
const nssMemberListCap = 100

// nssWrite is how one store call was reached, for the audit events it
// records.
type nssWrite struct {
	via string
	// importing is a sync run into a table that was empty when it began:
	// the run records one nss.initial_import instead of an event per
	// created row.
	importing bool
}

// nssAuditBatch collects the events one transaction recorded, so their
// shipped-log lines are emitted only once it has committed.
type nssAuditBatch struct {
	events []AuditEvent
}

// record writes event in tx and keeps it for flush. The server's own
// events (no actor) are System.
func (s *NSSDirectoryService) record(tx *gorm.DB, b *nssAuditBatch, event AuditEvent) error {
	if event.Actor == nil {
		event.System = true
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.now()
	}
	if err := s.recordTx(tx, event); err != nil {
		return err
	}
	b.events = append(b.events, event)
	return nil
}

// flush emits the shipped-log line of every event b recorded, after its
// transaction committed.
func (s *NSSDirectoryService) flush(b *nssAuditBatch) {
	for _, e := range b.events {
		s.logOnly(e)
	}
	b.events = nil
}

// accountTarget is the person linked to an account, for an event's
// target, or nil when the account has none (or the read fails: the event
// still names the account in its detail).
func accountTarget(tx *gorm.DB, userID *string) *AuditSubject {
	if userID == nil {
		return nil
	}
	var user model.User
	if err := tx.First(&user, "id = ?", *userID).Error; err != nil {
		return nil
	}
	return AuditSubjectFromUser(&user)
}

// nssMembers is membership rows as gid to the set of its uids.
type nssMembers map[uint32]map[uint32]bool

// add puts one membership in m.
func (m nssMembers) add(gid, uid uint32) {
	if m[gid] == nil {
		m[gid] = map[uint32]bool{}
	}
	m[gid][uid] = true
}

// readMembers reads the membership rows matching where.
func readMembers(tx *gorm.DB, where string, args ...any) (nssMembers, error) {
	var rows []model.PosixGroupMember
	if err := tx.Where(where, args...).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read the memberships: %w", err)
	}
	out := nssMembers{}
	for _, r := range rows {
		out.add(r.GID, r.UID)
	}
	return out, nil
}

// nssMemberDelta is one group's membership change, each list sorted.
type nssMemberDelta struct {
	added   []uint32
	removed []uint32
}

// memberDeltas is the change from before to after, per gid, for the gids
// whose set changed.
func memberDeltas(before, after nssMembers) map[uint32]nssMemberDelta {
	out := map[uint32]nssMemberDelta{}
	gids := maps.Clone(before)
	maps.Copy(gids, after)
	for gid := range gids {
		var d nssMemberDelta
		for uid := range after[gid] {
			if !before[gid][uid] {
				d.added = append(d.added, uid)
			}
		}
		for uid := range before[gid] {
			if !after[gid][uid] {
				d.removed = append(d.removed, uid)
			}
		}
		if len(d.added) == 0 && len(d.removed) == 0 {
			continue
		}
		slices.Sort(d.added)
		slices.Sort(d.removed)
		out[gid] = d
	}
	return out
}

// nssMemberRef is one member as nss.group_members_changed lists it: the
// uid, and the stored account's name when there is one.
type nssMemberRef struct {
	UID  uint32 `json:"uid"`
	Name string `json:"name,omitempty"`
}

// nssMemberChange is what one recordMemberChanges call is about: the path,
// the admin behind it (nil for the server), and the names and row ids of
// uids and gids whose rows the write itself removed or re-keyed, so a
// lookup by the number no longer finds them.
type nssMemberChange struct {
	via        string
	actor      *AuditSubject
	userNames  map[uint32]string
	groupNames map[uint32]string
	userIDs    map[uint32]string
	groupIDs   map[uint32]string
}

// recordMemberChanges records one nss.group_members_changed per gid in
// deltas, sorted by gid, with the first nssMemberListCap added and removed
// members of each and the full counts. Each event refers to the group and
// to every added and removed account that is stored, past the cap too, so
// a large change still shows on each member's history.
func (s *NSSDirectoryService) recordMemberChanges(tx *gorm.DB, b *nssAuditBatch, deltas map[uint32]nssMemberDelta, c nssMemberChange) error {
	if len(deltas) == 0 {
		return nil
	}
	var uids []uint32
	for _, d := range deltas {
		uids = append(uids, d.added...)
		uids = append(uids, d.removed...)
	}
	userIDs, err := nssRowIDs(tx, accountTable(), uids, c.userIDs)
	if err != nil {
		return err
	}
	groupIDs, err := nssRowIDs(tx, groupTable(), slices.Collect(maps.Keys(deltas)), c.groupIDs)
	if err != nil {
		return err
	}
	for _, gid := range slices.Sorted(maps.Keys(deltas)) {
		d := deltas[gid]
		added, removed := capUIDs(d.added), capUIDs(d.removed)
		names, err := memberNames(tx, append(slices.Clone(added), removed...), c.userNames)
		if err != nil {
			return err
		}
		name, err := groupName(tx, gid, c.groupNames)
		if err != nil {
			return err
		}
		if err := s.record(tx, b, AuditEvent{
			Action:  AuditNSSGroupMembersChanged,
			Actor:   c.actor,
			NSSRefs: memberChangeRefs(gid, d, groupIDs, userIDs),
			Detail: map[string]any{
				"table":         groupTable().table,
				"name":          name,
				"gid":           gid,
				"via":           c.via,
				"added":         memberRefs(added, names),
				"removed":       memberRefs(removed, names),
				"added_count":   len(d.added),
				"removed_count": len(d.removed),
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

// groupName is the name of gid, the hint first, else the stored group's,
// else "".
func groupName(tx *gorm.DB, gid uint32, hints map[uint32]string) (string, error) {
	if name := hints[gid]; name != "" {
		return name, nil
	}
	var rows []string
	if err := tx.Model(&model.PosixGroup{}).Where("gid = ?", gid).Limit(1).Pluck("name", &rows).Error; err != nil {
		return "", fmt.Errorf("read the name of gid %d: %w", gid, err)
	}
	if len(rows) == 0 {
		return "", nil
	}
	return rows[0], nil
}

// memberChangeRefs is what one group's nss.group_members_changed refers
// to: the group and every added and removed account, each when stored.
func memberChangeRefs(gid uint32, d nssMemberDelta, groupIDs, userIDs map[uint32]string) []NSSAuditRef {
	refs := make([]NSSAuditRef, 0, 1+len(d.added)+len(d.removed))
	if id, ok := groupIDs[gid]; ok {
		refs = append(refs, groupTable().auditRef(id))
	}
	for _, uid := range slices.Concat(d.added, d.removed) {
		if id, ok := userIDs[uid]; ok {
			refs = append(refs, accountTable().auditRef(id))
		}
	}
	return refs
}

// nssRowIDs is the stored row id of each of numbers in t that has a row,
// hints first, read in batches of nssBatchSize.
func nssRowIDs(tx *gorm.DB, t nssTable, numbers []uint32, hints map[uint32]string) (map[uint32]string, error) {
	out := maps.Clone(hints)
	if out == nil {
		out = map[uint32]string{}
	}
	var missing []uint32
	for _, n := range numbers {
		if _, ok := out[n]; !ok {
			missing = append(missing, n)
		}
	}
	slices.Sort(missing)
	missing = slices.Compact(missing)
	for chunk := range slices.Chunk(missing, nssBatchSize) {
		var rows []struct {
			ID     string `gorm:"column:id"`
			Number uint32 `gorm:"column:number"`
		}
		if err := tx.Table(t.table).Select("id, "+t.numberColumn+" AS number").
			Where(t.numberColumn+" IN ?", chunk).Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("read the %s row ids: %w", t.label, err)
		}
		for _, r := range rows {
			out[r.Number] = r.ID
		}
	}
	return out, nil
}

// capUIDs is the first nssMemberListCap of uids.
func capUIDs(uids []uint32) []uint32 {
	if len(uids) > nssMemberListCap {
		return uids[:nssMemberListCap]
	}
	return uids
}

// memberNames is the stored account name of each uid that has one, hints
// first: a write that removed or re-keyed an account knows its name.
func memberNames(tx *gorm.DB, uids []uint32, hints map[uint32]string) (map[uint32]string, error) {
	out := maps.Clone(hints)
	if out == nil {
		out = map[uint32]string{}
	}
	var missing []uint32
	for _, uid := range uids {
		if _, ok := out[uid]; !ok {
			missing = append(missing, uid)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	var rows []struct {
		UID  uint32 `gorm:"column:uid"`
		Name string `gorm:"column:name"`
	}
	if err := tx.Model(&model.PosixUser{}).Select("uid, name").Where("uid IN ?", missing).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("read member names: %w", err)
	}
	for _, r := range rows {
		out[r.UID] = r.Name
	}
	return out, nil
}

// memberRefs pairs each uid with its name, never nil so the detail always
// carries a list.
func memberRefs(uids []uint32, names map[uint32]string) []nssMemberRef {
	out := make([]nssMemberRef, 0, len(uids))
	for _, uid := range uids {
		out = append(out, nssMemberRef{UID: uid, Name: names[uid]})
	}
	return out
}

// recordRowEvent records one lifecycle event for an account or group row:
// action with the row's table, name, number and DN, the path, and extra.
// An account's linked person is the target.
func (s *NSSDirectoryService) recordRowEvent(tx *gorm.DB, b *nssAuditBatch, t nssTable, action AuditAction, row nssRowRef, userID *string, via string, extra map[string]any) error {
	detail := map[string]any{
		"table":        t.table,
		"name":         row.Name,
		t.numberColumn: row.Number,
		"dn":           row.DN,
		"via":          via,
	}
	maps.Copy(detail, extra)
	event := AuditEvent{Action: action, Detail: detail, NSSRefs: []NSSAuditRef{t.auditRef(row.ID)}}
	if t.numberColumn == "uid" {
		event.Target = accountTarget(tx, userID)
	}
	return s.record(tx, b, event)
}

// revivedDetail is what a revived event adds: since when the row was
// missing and whether it was a tombstone.
func revivedDetail(missingSince, deletedAt *time.Time) map[string]any {
	detail := map[string]any{"was_tombstoned": deletedAt != nil}
	if missingSince != nil {
		detail["missing_since"] = *missingSince
	}
	return detail
}

// tableEmpty reports whether t has no rows at all, tombstones included:
// a sync run into it is the first import.
func (s *NSSDirectoryService) tableEmpty(ctx context.Context, t nssTable) (bool, error) {
	var n int64
	if err := s.db.WithContext(ctx).Table(t.table).Count(&n).Error; err != nil {
		return false, fmt.Errorf("count the %s rows: %w", t.label, err)
	}
	return n == 0, nil
}

// recordInitialImport records the one nss.initial_import of a run that
// imported into an empty table, in place of an event per created row.
// Nothing when the run created nothing.
func (s *NSSDirectoryService) recordInitialImport(ctx context.Context, t nssTable, via string, created int) {
	if created == 0 || s.auditor == nil {
		return
	}
	detail := map[string]any{"table": t.table, "via": via, "created": created}
	if t.numberColumn == "gid" {
		var n int64
		if err := s.db.WithContext(ctx).Model(&model.PosixGroupMember{}).Count(&n).Error; err != nil {
			s.log.WarnContext(ctx, "could not count the imported memberships", "error", err)
		} else {
			detail["memberships"] = n
		}
	}
	s.auditor.Record(ctx, AuditEvent{Action: AuditNSSInitialImport, System: true, Detail: detail, OccurredAt: s.now()})
}
