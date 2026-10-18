package webtypes

import "time"

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
	// UID and GID are the stored values, still served.
	UID uint32 `json:"uid" validate:"required"`
	GID uint32 `json:"gid" validate:"required"`
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
	GID  uint32 `json:"gid" validate:"required"`
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
	Kind string `json:"kind" validate:"required"`
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
