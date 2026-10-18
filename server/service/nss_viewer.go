package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/utils/paging"
)

// NSSDirectoryViewer is what the read-only directory viewer's handlers
// (GET /api/admin/nss/users and /groups) drive: the stored accounts and
// groups as stored, and beside each what a host's lookup is served now.
// Implemented by *NSSLookupService, so "served" is built by the lookup
// code itself; an interface so the handlers' authorization and wire
// mapping can be tested on their own.
//
// Nothing here calls the directory refresher, writes a row or reads LDAP:
// where a host's lookup would ask the directory on a miss, the viewer
// reports the miss the database gives.
type NSSDirectoryViewer interface {
	Accounts(ctx context.Context, filter NSSViewerFilter) (*NSSAccountPage, error)
	Account(ctx context.Context, uid uint32) (*NSSAccountView, error)
	Groups(ctx context.Context, filter NSSViewerFilter) (*NSSGroupPage, error)
	Group(ctx context.Context, gid uint32) (*NSSGroupView, error)
	GroupMembers(ctx context.Context, gid uint32, params paging.Params) (*NSSGroupMemberPage, error)
}

// NSSViewerFilter is one list request: the page window and search term,
// and the state to keep, empty for every state.
type NSSViewerFilter struct {
	Page  paging.Params
	State model.NSSEntryState
}

// NSSAccountPage is one page of stored accounts, by name then id.
type NSSAccountPage struct {
	Accounts []NSSAccountEntry
	// Total counts every account matching the filter, across all pages.
	Total int64
	// NextCursor is the after for the following page, nil on the last.
	NextCursor *string
}

// NSSAccountEntry is one stored account with its derived state.
type NSSAccountEntry struct {
	Account model.PosixUser
	State   model.NSSEntryState
}

// NSSAccountView is one stored account in full: the row, the linked
// person, every stored membership, and what a lookup by its uid is served.
type NSSAccountView struct {
	Account model.PosixUser
	State   model.NSSEntryState
	// Person is the linked users row, nil when there is none.
	Person *NSSLinkedPerson
	// Groups is every membership row for the uid, by gid.
	Groups []NSSStoredMembership
	// Served is the lookup's record; nil exactly when NotServed is set.
	Served    *apitypes.NSSUserResponse
	NotServed model.NSSNotServedReason
}

// NSSLinkedPerson is the users row an account is linked to.
type NSSLinkedPerson struct {
	ID          string     `gorm:"column:id"`
	Username    string     `gorm:"column:username"`
	DisplayName string     `gorm:"column:display_name"`
	DisabledAt  *time.Time `gorm:"column:disabled_at"`
}

// NSSStoredMembership is one membership row of an account. Name and State
// are the stored group's, set only when Stored.
type NSSStoredMembership struct {
	GID    uint32
	Stored bool
	Name   string
	State  model.NSSEntryState
}

// NSSGroupPage is one page of stored groups, by name then id.
type NSSGroupPage struct {
	Groups     []NSSGroupEntry
	Total      int64
	NextCursor *string
}

// NSSGroupEntry is one stored group with its derived state and its count
// of membership rows.
type NSSGroupEntry struct {
	Group       model.PosixGroup
	State       model.NSSEntryState
	MemberCount int64
}

// NSSGroupView is one stored group: the row, its membership count, and
// what a lookup by its gid is served.
type NSSGroupView struct {
	Group       model.PosixGroup
	State       model.NSSEntryState
	MemberCount int64
	// Served is the lookup's record; nil exactly when NotServed is set.
	Served    *apitypes.NSSGroupResponse
	NotServed model.NSSNotServedReason
}

// NSSGroupMemberPage is one page of a group's membership rows, by uid.
type NSSGroupMemberPage struct {
	Members []NSSGroupMember
	// Total counts every membership row of the group.
	Total int64
	// NextCursor is the after for the following page (the last uid, in
	// decimal), nil on the last.
	NextCursor *string
}

// NSSGroupMember is one membership row of a group. Name and State are the
// stored account's, set only when Stored.
type NSSGroupMember struct {
	UID    uint32
	Stored bool
	Name   string
	State  model.NSSEntryState
}

// nssAccountState is the one state the viewer shows for an account, from
// its marks and the linked person's disable, first match winning. It must
// agree with nssAccountStateSQL, which the state filter uses.
func nssAccountState(row *model.PosixUser, personDisabledAt *time.Time) model.NSSEntryState {
	switch {
	case row.WithheldAt != nil:
		return model.NSSEntryStateWithheld
	case row.DeletedAt != nil:
		return model.NSSEntryStateTombstone
	case row.DirDisabled || personDisabledAt != nil:
		return model.NSSEntryStateDisabled
	case row.MissingSince != nil:
		return model.NSSEntryStateMissing
	case row.PendingUID != nil || row.PendingGID != nil:
		return model.NSSEntryStatePending
	default:
		return model.NSSEntryStateActive
	}
}

// nssGroupState is nssAccountState for a group, which has no disable. It
// must agree with nssGroupStateSQL.
func nssGroupState(row *model.PosixGroup) model.NSSEntryState {
	switch {
	case row.WithheldAt != nil:
		return model.NSSEntryStateWithheld
	case row.DeletedAt != nil:
		return model.NSSEntryStateTombstone
	case row.MissingSince != nil:
		return model.NSSEntryStateMissing
	case row.PendingGID != nil:
		return model.NSSEntryStatePending
	default:
		return model.NSSEntryStateActive
	}
}

// nssAccountStateSQL is nssAccountState as SQL over posix_users LEFT JOIN
// users, for the state filter. dir_disabled is a bare condition because it
// is BOOLEAN on Postgres and a 0/1 INTEGER on SQLite, which both take.
const nssAccountStateSQL = "(CASE" +
	" WHEN posix_users.withheld_at IS NOT NULL THEN '" + string(model.NSSEntryStateWithheld) + "'" +
	" WHEN posix_users.deleted_at IS NOT NULL THEN '" + string(model.NSSEntryStateTombstone) + "'" +
	" WHEN posix_users.dir_disabled OR users.disabled_at IS NOT NULL THEN '" + string(model.NSSEntryStateDisabled) + "'" +
	" WHEN posix_users.missing_since IS NOT NULL THEN '" + string(model.NSSEntryStateMissing) + "'" +
	" WHEN posix_users.pending_uid IS NOT NULL OR posix_users.pending_gid IS NOT NULL THEN '" + string(model.NSSEntryStatePending) + "'" +
	" ELSE '" + string(model.NSSEntryStateActive) + "' END)"

// nssGroupStateSQL is nssGroupState as SQL over posix_groups.
const nssGroupStateSQL = "(CASE" +
	" WHEN posix_groups.withheld_at IS NOT NULL THEN '" + string(model.NSSEntryStateWithheld) + "'" +
	" WHEN posix_groups.deleted_at IS NOT NULL THEN '" + string(model.NSSEntryStateTombstone) + "'" +
	" WHEN posix_groups.missing_since IS NOT NULL THEN '" + string(model.NSSEntryStateMissing) + "'" +
	" WHEN posix_groups.pending_gid IS NOT NULL THEN '" + string(model.NSSEntryStatePending) + "'" +
	" ELSE '" + string(model.NSSEntryStateActive) + "' END)"

// notServedReason says why a stored row that servable refuses is not
// served: it is withheld, or else it is a tombstone past
// nss.tombstone_retention.
func notServedReason(withheldAt *time.Time) model.NSSNotServedReason {
	if withheldAt != nil {
		return model.NSSNotServedWithheld
	}
	return model.NSSNotServedPastRetention
}

// nssViewerSearch applies the list search to q: a case-insensitive
// substring of the name or DN, or, when the term is a uint32, the exact
// number in numberColumn. Columns are literals from this file.
func nssViewerSearch(q *gorm.DB, query, nameColumn, dnColumn, numberColumn string) *gorm.DB {
	where, args := paging.Filter(query, nameColumn, dnColumn)
	if where == "" {
		return q
	}
	if n, err := strconv.ParseUint(query, 10, 32); err == nil {
		where = "(" + where + " OR " + numberColumn + " = ?)"
		args = append(args, uint32(n))
	}
	return q.Where(where, args...)
}

// nssViewerSeek resumes a list ordered by (name, id) after the row of
// table with id after. Only the cursor's place is read, so it need not
// match the filter; an id naming no row is paging.UnknownCursor.
func nssViewerSeek(db *gorm.DB, table, after string) (string, []any, error) {
	var cursor struct {
		ID   string `gorm:"column:id"`
		Name string `gorm:"column:name"`
	}
	err := db.Table(table).Select("id, name").Where("id = ?", after).Take(&cursor).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil, paging.UnknownCursor()
	}
	if err != nil {
		return "", nil, fmt.Errorf("look up the NSS %s cursor: %w", table, err)
	}
	return fmt.Sprintf("(%[1]s.name > ? OR (%[1]s.name = ? AND %[1]s.id > ?))", table),
		[]any{cursor.Name, cursor.Name, cursor.ID}, nil
}

// Accounts lists one page of stored accounts matching filter, by name then
// id, each with its state.
func (s *NSSLookupService) Accounts(ctx context.Context, filter NSSViewerFilter) (*NSSAccountPage, error) {
	db := s.db.WithContext(ctx)
	q := db.Table("posix_users").Joins("LEFT JOIN users ON users.id = posix_users.user_id")
	q = nssViewerSearch(q, filter.Page.Query, "posix_users.name", "posix_users.dn", "posix_users.uid")
	if filter.State != "" {
		q = q.Where(nssAccountStateSQL+" = ?", string(filter.State))
	}
	total, err := paging.Count(q.Session(&gorm.Session{}))
	if err != nil {
		return nil, fmt.Errorf("count NSS accounts: %w", err)
	}
	if filter.Page.After != "" {
		where, args, err := nssViewerSeek(db, "posix_users", filter.Page.After)
		if err != nil {
			return nil, err
		}
		q = q.Where(where, args...)
	}

	var rows []posixUserRow
	if err := q.Select("posix_users.*, users.disabled_at AS user_disabled_at").
		Order("posix_users.name, posix_users.id").Limit(filter.Page.Limit + 1).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list NSS accounts: %w", err)
	}
	rows, next := paging.Trim(rows, filter.Page.Limit, func(r posixUserRow) string { return r.ID })

	page := &NSSAccountPage{Accounts: make([]NSSAccountEntry, 0, len(rows)), Total: total, NextCursor: next}
	for i := range rows {
		page.Accounts = append(page.Accounts, NSSAccountEntry{
			Account: rows[i].PosixUser, State: nssAccountState(&rows[i].PosixUser, rows[i].UserDisabledAt),
		})
	}
	return page, nil
}

// Account reads the stored account with uid in full. A NotFoundError when
// no row has the uid. Served is built by the code a lookup by uid runs,
// minus the directory refresh a miss would trigger.
func (s *NSSLookupService) Account(ctx context.Context, uid uint32) (*NSSAccountView, error) {
	row, err := s.readUserRow(ctx, func(db *gorm.DB) *gorm.DB { return db.Where("posix_users.uid = ?", uid) })
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, &errorresponses.NotFoundError{Resource: fmt.Sprintf("the NSS account with uid %d", uid)}
	}
	view := &NSSAccountView{Account: row.PosixUser, State: nssAccountState(&row.PosixUser, row.UserDisabledAt)}

	if view.Person, err = s.linkedPerson(ctx, row.UserID); err != nil {
		return nil, err
	}
	if view.Groups, err = s.storedMemberships(ctx, uid); err != nil {
		return nil, err
	}
	if view.Served, view.NotServed, err = s.serveUser(ctx, row); err != nil {
		return nil, err
	}
	return view, nil
}

// serveUser is what a lookup that found row answers: nothing when the row
// is not servable or its private group clashes, else buildUser's record.
func (s *NSSLookupService) serveUser(ctx context.Context, row *posixUserRow) (*apitypes.NSSUserResponse, model.NSSNotServedReason, error) {
	if !s.servable(row.WithheldAt, row.DeletedAt) {
		return nil, notServedReason(row.WithheldAt), nil
	}
	served, err := s.buildUser(ctx, row)
	if errors.Is(err, errNSSPrivateGroupClash) {
		return nil, model.NSSNotServedPrivateGroupClash, nil
	}
	if errors.Is(err, errNSSNoGID) {
		return nil, model.NSSNotServedNoGID, nil
	}
	if err != nil {
		return nil, "", err
	}
	return served, "", nil
}

// linkedPerson reads the users row userID names, nil when it is unset or
// the row has gone.
func (s *NSSLookupService) linkedPerson(ctx context.Context, userID *string) (*NSSLinkedPerson, error) {
	if userID == nil {
		return nil, nil
	}
	var person NSSLinkedPerson
	err := s.db.WithContext(ctx).Table("users").
		Select("id, username, display_name, disabled_at").
		Where("id = ?", *userID).Take(&person).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the person linked to an NSS account: %w", err)
	}
	return &person, nil
}

// storedMemberships lists every membership row of uid, by gid, with the
// stored group of that gid when there is one.
func (s *NSSLookupService) storedMemberships(ctx context.Context, uid uint32) ([]NSSStoredMembership, error) {
	var rows []struct {
		GID          uint32     `gorm:"column:gid"`
		GroupID      *string    `gorm:"column:group_id"`
		Name         *string    `gorm:"column:name"`
		WithheldAt   *time.Time `gorm:"column:withheld_at"`
		DeletedAt    *time.Time `gorm:"column:deleted_at"`
		MissingSince *time.Time `gorm:"column:missing_since"`
		PendingGID   *uint32    `gorm:"column:pending_gid"`
	}
	err := s.db.WithContext(ctx).Table("posix_group_members").
		Select("posix_group_members.gid AS gid, posix_groups.id AS group_id, posix_groups.name AS name, "+
			"posix_groups.withheld_at AS withheld_at, posix_groups.deleted_at AS deleted_at, "+
			"posix_groups.missing_since AS missing_since, posix_groups.pending_gid AS pending_gid").
		Joins("LEFT JOIN posix_groups ON posix_groups.gid = posix_group_members.gid").
		Where("posix_group_members.uid = ?", uid).
		Order("posix_group_members.gid").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read an NSS account's membership rows: %w", err)
	}
	out := make([]NSSStoredMembership, 0, len(rows))
	for _, r := range rows {
		m := NSSStoredMembership{GID: r.GID}
		if r.GroupID != nil {
			m.Stored, m.Name = true, stringValue(r.Name)
			m.State = nssGroupState(&model.PosixGroup{
				WithheldAt: r.WithheldAt, DeletedAt: r.DeletedAt, MissingSince: r.MissingSince, PendingGID: r.PendingGID,
			})
		}
		out = append(out, m)
	}
	return out, nil
}

// Groups lists one page of stored groups matching filter, by name then id,
// each with its state and membership count.
func (s *NSSLookupService) Groups(ctx context.Context, filter NSSViewerFilter) (*NSSGroupPage, error) {
	db := s.db.WithContext(ctx)
	q := db.Table("posix_groups")
	q = nssViewerSearch(q, filter.Page.Query, "posix_groups.name", "posix_groups.dn", "posix_groups.gid")
	if filter.State != "" {
		q = q.Where(nssGroupStateSQL+" = ?", string(filter.State))
	}
	total, err := paging.Count(q.Session(&gorm.Session{}))
	if err != nil {
		return nil, fmt.Errorf("count NSS groups: %w", err)
	}
	if filter.Page.After != "" {
		where, args, err := nssViewerSeek(db, "posix_groups", filter.Page.After)
		if err != nil {
			return nil, err
		}
		q = q.Where(where, args...)
	}

	var rows []model.PosixGroup
	if err := q.Order("posix_groups.name, posix_groups.id").Limit(filter.Page.Limit + 1).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list NSS groups: %w", err)
	}
	rows, next := paging.Trim(rows, filter.Page.Limit, func(r model.PosixGroup) string { return r.ID })

	gids := make([]uint32, 0, len(rows))
	for _, r := range rows {
		gids = append(gids, r.GID)
	}
	counts, err := s.memberCounts(ctx, gids)
	if err != nil {
		return nil, err
	}
	page := &NSSGroupPage{Groups: make([]NSSGroupEntry, 0, len(rows)), Total: total, NextCursor: next}
	for i := range rows {
		page.Groups = append(page.Groups, NSSGroupEntry{
			Group: rows[i], State: nssGroupState(&rows[i]), MemberCount: counts[rows[i].GID],
		})
	}
	return page, nil
}

// memberCounts counts the membership rows of each gid in one query; a gid
// with none is absent from the map.
func (s *NSSLookupService) memberCounts(ctx context.Context, gids []uint32) (map[uint32]int64, error) {
	counts := make(map[uint32]int64, len(gids))
	if len(gids) == 0 {
		return counts, nil
	}
	var rows []struct {
		GID   uint32 `gorm:"column:gid"`
		Count int64  `gorm:"column:n"`
	}
	if err := s.db.WithContext(ctx).Table("posix_group_members").
		Select("gid, COUNT(*) AS n").Where("gid IN ?", gids).Group("gid").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("count NSS group members: %w", err)
	}
	for _, r := range rows {
		counts[r.GID] = r.Count
	}
	return counts, nil
}

// Group reads the stored group with gid. A NotFoundError when no row has
// the gid. Served is built by the code a lookup by gid runs on a stored
// group, minus the directory refresh a miss would trigger; a stored group
// is what a lookup by its gid finds, ahead of any private group.
func (s *NSSLookupService) Group(ctx context.Context, gid uint32) (*NSSGroupView, error) {
	group, err := s.storedGroup(ctx, gid)
	if err != nil {
		return nil, err
	}
	counts, err := s.memberCounts(ctx, []uint32{gid})
	if err != nil {
		return nil, err
	}
	view := &NSSGroupView{Group: *group, State: nssGroupState(group), MemberCount: counts[gid]}
	if !s.servable(group.WithheldAt, group.DeletedAt) {
		view.NotServed = notServedReason(group.WithheldAt)
		return view, nil
	}
	if view.Served, err = s.buildGroup(ctx, group); err != nil {
		return nil, err
	}
	return view, nil
}

// storedGroup reads the posix_groups row with gid, or a NotFoundError.
func (s *NSSLookupService) storedGroup(ctx context.Context, gid uint32) (*model.PosixGroup, error) {
	var groups []model.PosixGroup
	if err := s.db.WithContext(ctx).Where("gid = ?", gid).Limit(1).Find(&groups).Error; err != nil {
		return nil, fmt.Errorf("read posix group: %w", err)
	}
	if len(groups) == 0 {
		return nil, &errorresponses.NotFoundError{Resource: fmt.Sprintf("the NSS group with gid %d", gid)}
	}
	return &groups[0], nil
}

// GroupMembers lists one page of the membership rows of the stored group
// with gid, by uid, each with the stored account of that uid when there is
// one. A NotFoundError when no group has the gid; the cursor is the last
// uid, and anything that is not a uid is paging.UnknownCursor. The search
// term is not used.
func (s *NSSLookupService) GroupMembers(ctx context.Context, gid uint32, params paging.Params) (*NSSGroupMemberPage, error) {
	if _, err := s.storedGroup(ctx, gid); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	q := db.Table("posix_group_members").Where("posix_group_members.gid = ?", gid)
	total, err := paging.Count(q.Session(&gorm.Session{}))
	if err != nil {
		return nil, fmt.Errorf("count NSS group members: %w", err)
	}
	if params.After != "" {
		after, err := strconv.ParseUint(params.After, 10, 32)
		if err != nil {
			return nil, paging.UnknownCursor()
		}
		q = q.Where("posix_group_members.uid > ?", uint32(after))
	}

	var rows []nssMemberRow
	if err := q.Select("posix_group_members.uid AS uid, posix_users.id AS account_id, posix_users.name AS name, " +
		"posix_users.withheld_at AS withheld_at, posix_users.deleted_at AS deleted_at, " +
		"posix_users.dir_disabled AS dir_disabled, posix_users.missing_since AS missing_since, " +
		"posix_users.pending_uid AS pending_uid, posix_users.pending_gid AS pending_gid, " +
		"users.disabled_at AS user_disabled_at").
		Joins("LEFT JOIN posix_users ON posix_users.uid = posix_group_members.uid").
		Joins("LEFT JOIN users ON users.id = posix_users.user_id").
		Order("posix_group_members.uid").Limit(params.Limit + 1).
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list NSS group members: %w", err)
	}
	rows, next := paging.Trim(rows, params.Limit, func(r nssMemberRow) string { return strconv.FormatUint(uint64(r.UID), 10) })

	page := &NSSGroupMemberPage{Members: make([]NSSGroupMember, 0, len(rows)), Total: total, NextCursor: next}
	for _, r := range rows {
		page.Members = append(page.Members, r.member())
	}
	return page, nil
}

// nssMemberRow is one membership row joined to the stored account of its
// uid; every account column is nil when there is none.
type nssMemberRow struct {
	UID            uint32     `gorm:"column:uid"`
	AccountID      *string    `gorm:"column:account_id"`
	Name           *string    `gorm:"column:name"`
	WithheldAt     *time.Time `gorm:"column:withheld_at"`
	DeletedAt      *time.Time `gorm:"column:deleted_at"`
	DirDisabled    *bool      `gorm:"column:dir_disabled"`
	MissingSince   *time.Time `gorm:"column:missing_since"`
	PendingUID     *uint32    `gorm:"column:pending_uid"`
	PendingGID     *uint32    `gorm:"column:pending_gid"`
	UserDisabledAt *time.Time `gorm:"column:user_disabled_at"`
}

// member describes the row for the members page, with the account's state
// when it is stored.
func (r nssMemberRow) member() NSSGroupMember {
	m := NSSGroupMember{UID: r.UID}
	if r.AccountID == nil {
		return m
	}
	m.Stored, m.Name = true, stringValue(r.Name)
	m.State = nssAccountState(&model.PosixUser{
		WithheldAt: r.WithheldAt, DeletedAt: r.DeletedAt, DirDisabled: r.DirDisabled != nil && *r.DirDisabled,
		MissingSince: r.MissingSince, PendingUID: r.PendingUID, PendingGID: r.PendingGID,
	}, r.UserDisabledAt)
	return m
}

// stringValue is *p, or "" for nil: a joined column that is NULL only
// when the row it belongs to is absent.
func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
