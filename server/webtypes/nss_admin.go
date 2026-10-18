package webtypes

import (
	"time"

	"github.com/mnestor/ssoossh/server/model"
)

// The NSS directory's admin views: POST /api/admin/ldap/sync's NSS half,
// and /api/admin/nss/*. A group's members on the wire are only the
// accounts the server has stored, which with the full sync is every
// account nss.user_filter selects; a member that is not such an account
// is never served.

// NSSSyncRunResponse is what one NSS directory sync run concluded.
type NSSSyncRunResponse struct {
	// Full is true for a full run, which is the only kind that marks
	// entries missing; the admin trigger always runs one.
	Full      bool      `json:"full" validate:"required"`
	StartedAt time.Time `json:"started_at" validate:"required"`
	// FinishedAt is absent when the run stopped on an error.
	FinishedAt *time.Time            `json:"finished_at,omitempty"`
	Accounts   NSSSyncCountsResponse `json:"accounts" validate:"required"`
	Groups     NSSSyncCountsResponse `json:"groups" validate:"required"`
	// Error is why the run stopped short: the directory could not be
	// reached or searched, or another run was in progress on this
	// instance. Nothing is marked missing by a run that stopped short.
	Error string `json:"error,omitempty"`
}

// NSSSyncCountsResponse is what one run did to the accounts or the groups.
type NSSSyncCountsResponse struct {
	// Seen is the entries the directory returned.
	Seen int `json:"seen" validate:"required"`
	// Skipped is the entries rejected because a value did not parse (a
	// name the POSIX rules refuse, a uid that is not a number, a relative
	// home); each is logged with its DN.
	Skipped int `json:"skipped" validate:"required"`
	Created int `json:"created" validate:"required"`
	Updated int `json:"updated" validate:"required"`
	// Refused is the new entries not stored because another row, a
	// tombstone included, holds their number or name.
	Refused int `json:"refused" validate:"required"`
	// Failed is the entries that could not be checked or written.
	Failed int `json:"failed" validate:"required"`
	// Missing is the rows marked missing for the first time; Tombstoned
	// the rows missing for nss.tombstone_after and now deleted.
	Missing    int `json:"missing" validate:"required"`
	Tombstoned int `json:"tombstoned" validate:"required"`
}

// NSSPendingIDChangesResponse is GET /api/admin/nss/pending-id-changes:
// every account and group whose directory entry now gives a stored uid or
// gid a different value. The stored value is what hosts are served until
// an admin accepts the change or the directory reverts.
type NSSPendingIDChangesResponse struct {
	Users  []NSSPendingUserIDChange  `json:"users" validate:"required"`
	Groups []NSSPendingGroupIDChange `json:"groups" validate:"required"`
}

// NSSPendingUserIDChange is one account with a pending uid, gid or both.
type NSSPendingUserIDChange struct {
	Name string `json:"name" validate:"required"`
	DN   string `json:"dn" validate:"required"`
	// UID and GID are the stored values, still served. GID is absent when
	// the entry has no gid attribute (accepted only under
	// nss.auto_private_groups "true").
	UID uint32  `json:"uid" validate:"required"`
	GID *uint32 `json:"gid,omitempty"`
	// PendingUID and PendingGID are the directory's values, absent when
	// that number has not changed.
	PendingUID *uint32 `json:"pending_uid,omitempty"`
	PendingGID *uint32 `json:"pending_gid,omitempty"`
	// PendingSince is the first sync that saw the pending value.
	PendingSince time.Time `json:"pending_since" validate:"required"`
}

// NSSPendingGroupIDChange is one group with a pending gid.
type NSSPendingGroupIDChange struct {
	Name         string    `json:"name" validate:"required"`
	DN           string    `json:"dn" validate:"required"`
	GID          uint32    `json:"gid" validate:"required"`
	PendingGID   uint32    `json:"pending_gid" validate:"required"`
	PendingSince time.Time `json:"pending_since" validate:"required"`
}

// NSSAcceptedUserIDChangeResponse is the account after
// POST /api/admin/nss/users/{uid}/accept-id-change.
type NSSAcceptedUserIDChangeResponse struct {
	Name string `json:"name" validate:"required"`
	DN   string `json:"dn" validate:"required"`
	UID  uint32 `json:"uid" validate:"required"`
	// GID is absent when the entry has no gid attribute.
	GID *uint32 `json:"gid,omitempty"`
}

// NSSAcceptedGroupIDChangeResponse is the group after
// POST /api/admin/nss/groups/{gid}/accept-id-change.
type NSSAcceptedGroupIDChangeResponse struct {
	Name string `json:"name" validate:"required"`
	DN   string `json:"dn" validate:"required"`
	GID  uint32 `json:"gid" validate:"required"`
}

// NSSCollisionsResponse is GET /api/admin/nss/collisions: every pair of
// directory entries contending for one uid, gid or name, oldest first.
// Neither entry of a pair is served while it lasts.
type NSSCollisionsResponse struct {
	Collisions []NSSCollisionResponse `json:"collisions" validate:"required"`
}

// NSSCollisionResponse is one collision.
type NSSCollisionResponse struct {
	// Kind is the contested value: uid or user_name between accounts, gid
	// or group_name between groups.
	Kind model.NSSCollisionKind `json:"kind" validate:"required"`
	// Value is the contested uid, gid or name, as text.
	Value string `json:"value" validate:"required"`
	// Holder is the stored row holding the value; Other the entry
	// contending for it.
	Holder      NSSCollisionEntryResponse `json:"holder" validate:"required"`
	Other       NSSCollisionEntryResponse `json:"other" validate:"required"`
	FirstSeenAt time.Time                 `json:"first_seen_at" validate:"required"`
	LastSeenAt  time.Time                 `json:"last_seen_at" validate:"required"`
}

// NSSCollisionEntryResponse is one side of a collision.
type NSSCollisionEntryResponse struct {
	Name string `json:"name" validate:"required"`
	DN   string `json:"dn" validate:"required"`
	// Number is the uid (accounts) or gid (groups) the entry carries.
	Number uint32 `json:"number" validate:"required"`
	// Stored is false for an entry the sync refused to store because the
	// holder has its number or name; it is known only by what it carried.
	Stored bool `json:"stored" validate:"required"`
	// Tombstone is true for a stored row the directory no longer has and
	// that is kept, holding its number and name, until
	// nss.tombstone_retention or an admin purges it.
	Tombstone bool `json:"tombstone" validate:"required"`
	// MissingSince and DeletedAt are the stored row's, absent while it
	// resolves.
	MissingSince *time.Time `json:"missing_since,omitempty"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}

// NSSPurgedTombstoneResponse is the row
// DELETE /api/admin/nss/users/{uid}/tombstone or
// /api/admin/nss/groups/{gid}/tombstone removed. Its number and name are
// free from then on.
type NSSPurgedTombstoneResponse struct {
	Name string `json:"name" validate:"required"`
	DN   string `json:"dn" validate:"required"`
	// Number is the uid or gid the row held.
	Number uint32 `json:"number" validate:"required"`
}

// The read-only directory viewer: GET /api/admin/nss/users,
// /api/admin/nss/users/{uid}, /api/admin/nss/groups,
// /api/admin/nss/groups/{gid} and /api/admin/nss/groups/{gid}/members.
// Each shows a stored row as stored, and beside it what a host's lookup
// would be served now, built by the lookup code itself. Reading here never
// asks the directory and never writes.

// NSSAccountsResponse is one page of the stored accounts, ordered by name.
type NSSAccountsResponse struct {
	Accounts []NSSAccountSummaryResponse `json:"accounts" validate:"required"`
	// Total is how many stored accounts match the search, across all pages.
	Total int64 `json:"total" validate:"required"`
	// NextCursor is the after for the following page: the id of this
	// page's last row, absent on the last page.
	NextCursor *string `json:"next_cursor,omitempty"`
}

// NSSAccountSummaryResponse is one stored account in the list.
type NSSAccountSummaryResponse struct {
	// ID is the row id, the list's cursor. The account's own pages are
	// addressed by UID.
	ID   string `json:"id" validate:"required"`
	Name string `json:"name" validate:"required"`
	UID  uint32 `json:"uid" validate:"required"`
	// GID is the stored gid attribute, before nss.auto_private_groups;
	// absent when the entry has none (accepted only under "true").
	GID   *uint32             `json:"gid,omitempty"`
	Gecos string              `json:"gecos,omitempty"`
	DN    string              `json:"dn" validate:"required"`
	State model.NSSEntryState `json:"state" validate:"required"`
	// LastSyncedAt is the last sync or on-demand read that reached the
	// directory for this entry.
	LastSyncedAt time.Time `json:"last_synced_at" validate:"required"`
}

// NSSAccountDetailResponse is one stored account: the row, its stored
// group memberships, and what a host is served for it.
type NSSAccountDetailResponse struct {
	ID   string `json:"id" validate:"required"`
	Name string `json:"name" validate:"required"`
	UID  uint32 `json:"uid" validate:"required"`
	// GID is the stored gid attribute, before nss.auto_private_groups;
	// absent when the entry has none (accepted only under "true", where
	// it is followed rather than frozen).
	GID *uint32 `json:"gid,omitempty"`
	// DirectoryID is the ldap.id_attribute value, absent when that is
	// unset.
	DirectoryID string `json:"directory_id,omitempty"`
	// DN is the entry's DN from the last read; empty once another entry
	// took the DN (the row is then matched by DirectoryID alone).
	DN string `json:"dn"`
	// Home and Shell are the directory's attributes as stored, before the
	// nss.default_home and nss.default_shell fallbacks.
	Home  *string `json:"home,omitempty"`
	Shell *string `json:"shell,omitempty"`
	Gecos string  `json:"gecos,omitempty"`
	// DirDisabled is the directory's own disabled flag
	// (nss.disabled_filter).
	DirDisabled bool `json:"dir_disabled" validate:"required"`
	// Person is the ssoossh user linked to this entry at login. Absent for
	// shared and service accounts and for anyone who has not logged in.
	Person       *NSSLinkedPersonResponse `json:"person,omitempty"`
	PendingUID   *uint32                  `json:"pending_uid,omitempty"`
	PendingGID   *uint32                  `json:"pending_gid,omitempty"`
	PendingSince *time.Time               `json:"pending_since,omitempty"`
	MissingSince *time.Time               `json:"missing_since,omitempty"`
	DeletedAt    *time.Time               `json:"deleted_at,omitempty"`
	WithheldAt   *time.Time               `json:"withheld_at,omitempty"`
	// WithheldReason sums up the collisions that withhold the row.
	WithheldReason string              `json:"withheld_reason,omitempty"`
	State          model.NSSEntryState `json:"state" validate:"required"`
	CreatedAt      time.Time           `json:"created_at" validate:"required"`
	UpdatedAt      time.Time           `json:"updated_at" validate:"required"`
	LastSyncedAt   time.Time           `json:"last_synced_at" validate:"required"`
	// Groups is every stored membership row for the uid, by gid, whether or
	// not the group is served. The primary group is not among them unless
	// a membership row names it.
	Groups []NSSStoredMembershipResponse `json:"groups" validate:"required"`
	// Served is what a host's lookup by this uid gets now. Absent when the
	// lookup finds nothing; NotServed then says why.
	Served    *NSSServedAccountResponse `json:"served,omitempty"`
	NotServed model.NSSNotServedReason  `json:"not_served,omitempty"`
}

// NSSLinkedPersonResponse is the ssoossh user an account is linked to.
type NSSLinkedPersonResponse struct {
	// UserID addresses the person's admin page, /admin/users/{id}.
	UserID      string     `json:"user_id" validate:"required"`
	Username    string     `json:"username" validate:"required"`
	DisplayName string     `json:"display_name,omitempty"`
	DisabledAt  *time.Time `json:"disabled_at,omitempty"`
}

// NSSStoredMembershipResponse is one membership row of a stored account.
type NSSStoredMembershipResponse struct {
	GID uint32 `json:"gid" validate:"required"`
	// Name and State describe the stored group with that gid, and are
	// absent when no stored group has it.
	Name  string              `json:"name,omitempty"`
	State model.NSSEntryState `json:"state,omitempty"`
}

// NSSServedAccountResponse is the record a host's user lookup receives,
// field for field the /api/nss/user/* response.
type NSSServedAccountResponse struct {
	Name string `json:"name" validate:"required"`
	UID  uint32 `json:"uid" validate:"required"`
	// GID is the primary gid after nss.auto_private_groups.
	GID uint32 `json:"gid" validate:"required"`
	// Groups is every membership served, primary first.
	Groups []NSSServedMembershipResponse `json:"groups" validate:"required"`
	Home   *string                       `json:"home,omitempty"`
	Shell  *string                       `json:"shell,omitempty"`
	Gecos  string                        `json:"gecos,omitempty"`
	Status model.PosixStatus             `json:"status" validate:"required"`
}

// NSSServedMembershipResponse is one group in a served account's groups.
type NSSServedMembershipResponse struct {
	GID  uint32 `json:"gid" validate:"required"`
	Name string `json:"name" validate:"required"`
}

// NSSGroupsResponse is one page of the stored groups, ordered by name.
// Private groups (nss.auto_private_groups) are never stored, so they are
// not here; each appears on its account.
type NSSGroupsResponse struct {
	Groups []NSSGroupSummaryResponse `json:"groups" validate:"required"`
	// Total is how many stored groups match the search, across all pages.
	Total int64 `json:"total" validate:"required"`
	// NextCursor is the after for the following page: the id of this
	// page's last row, absent on the last page.
	NextCursor *string `json:"next_cursor,omitempty"`
}

// NSSGroupSummaryResponse is one stored group in the list.
type NSSGroupSummaryResponse struct {
	// ID is the row id, the list's cursor. The group's own pages are
	// addressed by GID.
	ID    string              `json:"id" validate:"required"`
	Name  string              `json:"name" validate:"required"`
	GID   uint32              `json:"gid" validate:"required"`
	DN    string              `json:"dn" validate:"required"`
	State model.NSSEntryState `json:"state" validate:"required"`
	// MemberCount is the stored membership rows, served or not.
	MemberCount  int64     `json:"member_count" validate:"required"`
	LastSyncedAt time.Time `json:"last_synced_at" validate:"required"`
}

// NSSGroupDetailResponse is one stored group: the row and what a host is
// served for it. Its members are paged separately, from
// /api/admin/nss/groups/{gid}/members.
type NSSGroupDetailResponse struct {
	ID           string     `json:"id" validate:"required"`
	Name         string     `json:"name" validate:"required"`
	GID          uint32     `json:"gid" validate:"required"`
	DirectoryID  string     `json:"directory_id,omitempty"`
	DN           string     `json:"dn"`
	PendingGID   *uint32    `json:"pending_gid,omitempty"`
	PendingSince *time.Time `json:"pending_since,omitempty"`
	MissingSince *time.Time `json:"missing_since,omitempty"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
	WithheldAt   *time.Time `json:"withheld_at,omitempty"`
	// WithheldReason sums up the collisions that withhold the row.
	WithheldReason string              `json:"withheld_reason,omitempty"`
	State          model.NSSEntryState `json:"state" validate:"required"`
	// MemberCount is the stored membership rows, served or not.
	MemberCount  int64     `json:"member_count" validate:"required"`
	CreatedAt    time.Time `json:"created_at" validate:"required"`
	UpdatedAt    time.Time `json:"updated_at" validate:"required"`
	LastSyncedAt time.Time `json:"last_synced_at" validate:"required"`
	// Served is what a host's group lookup by this gid gets now. Absent
	// when the lookup finds nothing; NotServed then says why.
	Served    *NSSServedGroupResponse  `json:"served,omitempty"`
	NotServed model.NSSNotServedReason `json:"not_served,omitempty"`
}

// NSSServedGroupResponse is the record a host's group lookup receives,
// with the member names counted rather than listed (the members endpoint
// lists them).
type NSSServedGroupResponse struct {
	Name string `json:"name" validate:"required"`
	GID  uint32 `json:"gid" validate:"required"`
	// MemberCount is how many member names the host receives, after
	// nss.max_group_members.
	MemberCount int `json:"member_count" validate:"required"`
	// MembersTruncated is true when nss.max_group_members cut the list.
	MembersTruncated bool `json:"members_truncated" validate:"required"`
}

// NSSGroupMembersResponse is one page of a stored group's membership rows,
// ordered by uid.
type NSSGroupMembersResponse struct {
	Members []NSSGroupMemberResponse `json:"members" validate:"required"`
	// Total is the group's membership rows, across all pages.
	Total int64 `json:"total" validate:"required"`
	// NextCursor is the after for the following page: this page's last
	// uid, absent on the last page.
	NextCursor *string `json:"next_cursor,omitempty"`
}

// NSSGroupMemberResponse is one membership row of a stored group.
type NSSGroupMemberResponse struct {
	UID uint32 `json:"uid" validate:"required"`
	// Name and State describe the stored account with that uid, and are
	// absent when no stored account has it (such a member is never
	// served).
	Name  string              `json:"name,omitempty"`
	State model.NSSEntryState `json:"state,omitempty"`
}
