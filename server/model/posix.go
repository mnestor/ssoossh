package model

import "time"

// PosixUser is one POSIX account the NSS directory serves to hosts: a
// directory entry that carries the mapped uid attribute. People, shared
// accounts and service accounts alike, which is why it is anchored on the
// directory entry rather than on users, and links to a person only through
// the optional UserID. See
// docs/proposals/nss-directory/1-server-api.md, "Data model".
//
// There is no status field. Whether the account is active, disabled or
// deleted is derived when it is read (PosixStatus), from DeletedAt,
// DirDisabled and the linked users.disabled_at.
type PosixUser struct {
	// ID is a random id, the same convention as User.ID, so an accepted uid
	// change is a column update rather than a delete and an insert.
	ID string `gorm:"column:id;primaryKey"`

	// UID is the mapped uid attribute. Frozen after the first write: hosts
	// own files by number, so a directory that changes it is held in
	// PendingUID until an admin accepts the change. Unique, tombstones
	// included.
	UID uint32 `gorm:"column:uid;uniqueIndex:idx_posix_users_uid"`

	// Name is the mapped name attribute, verbatim. It follows the directory
	// and is unique, tombstones included.
	Name string `gorm:"column:name;uniqueIndex:idx_posix_users_name"`

	// DirectoryID is the ldap.id_attribute value, in the same form as
	// UserLDAP.DirectoryID, and the first anchor the sync matches on. Empty
	// when ldap.id_attribute is unset.
	DirectoryID string `gorm:"column:directory_id;index:idx_posix_users_directory_id"`

	// DN is the entry's distinguished name from the last successful read:
	// the sync's second anchor, and what a group's member DNs resolve
	// against. Empty once an entry with another DirectoryID has turned up
	// at the same DN, which makes this row's copy stale; unique otherwise.
	DN string `gorm:"column:dn;uniqueIndex:idx_posix_users_dn"`

	// UserID links the person, set at login when the LDAP enrichment
	// resolves this entry. Nil for shared and service accounts and for
	// anyone who has not logged in.
	UserID *string `gorm:"column:user_id;uniqueIndex:idx_posix_users_user_id"`

	// GID is the mapped primary gid, frozen like UID. auto_private_groups
	// is applied when the row is read, never stored here. Nil when the
	// entry has no gid attribute, which only auto_private_groups "true"
	// accepts; under "true" the directory's value is followed rather than
	// frozen, because it is then only a supplementary group (sssd's
	// origPrimaryGroupGidNumber).
	GID *uint32 `gorm:"column:gid"`

	// PendingUID and PendingGID hold a differing directory value the sync
	// refused to apply, and PendingSince the first sync that saw one. The
	// directory reverting clears them; an admin accepting applies them.
	PendingUID   *uint32    `gorm:"column:pending_uid"`
	PendingGID   *uint32    `gorm:"column:pending_gid"`
	PendingSince *time.Time `gorm:"column:pending_since"`

	// Home and Shell are the mapped attributes, following the directory.
	// Nil when the entry has none.
	Home  *string `gorm:"column:home"`
	Shell *string `gorm:"column:shell"`

	// ShellPref is the person's own choice from nss.allowed_shells. Only
	// meaningful on a row with a UserID.
	ShellPref *string `gorm:"column:shell_pref"`

	// Gecos is the rendered and sanitised nss.gecos_template.
	Gecos string `gorm:"column:gecos"`

	// DirDisabled is the directory's own disabled flag, from
	// nss.disabled_filter.
	DirDisabled bool `gorm:"column:dir_disabled"`

	// MissingSince is the first full sync that did not see the entry; a
	// directory outage or a failed search is never a miss. Nil while the
	// entry resolves.
	MissingSince *time.Time `gorm:"column:missing_since"`

	// DeletedAt is set once MissingSince is older than nss.tombstone_after
	// and cleared if the entry reappears. A *time.Time on purpose, not
	// gorm.DeletedAt: GORM would then hide every tombstone from every
	// query, and a tombstone must stay resolvable, holding its uid and
	// name, until nss.tombstone_retention purges it.
	DeletedAt *time.Time `gorm:"column:deleted_at"`

	// WithheldAt is set while the row is part of a uid or name collision
	// and cleared when the collision resolves; lookups treat a withheld row
	// as not found. Row state, so every instance withholds the same rows.
	// The collisions themselves are NSSCollision rows; WithheldReason sums
	// them up in a short sentence for the operator, and is empty while the
	// row is not withheld.
	WithheldAt     *time.Time `gorm:"column:withheld_at"`
	WithheldReason string     `gorm:"column:withheld_reason"`

	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`

	// LastSyncedAt is the last sync or on-demand read that reached the
	// directory for this entry. Every row is born from one.
	LastSyncedAt time.Time `gorm:"column:last_synced_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (PosixUser) TableName() string { return "posix_users" }

// PosixGroup is one POSIX group the NSS directory serves: a directory group
// with the mapped gid attribute. Private groups (nss.auto_private_groups)
// are worked out when an account is read and are never stored here.
//
// Groups are tombstoned the way accounts are. A group missing from the
// directory for nss.tombstone_after gets DeletedAt; it still resolves by
// name and gid, so ls -l keeps showing a name, but is served with no
// members (the sync deletes its membership rows), so it grants nothing; it
// is purged after nss.tombstone_retention with the accounts.
type PosixGroup struct {
	ID string `gorm:"column:id;primaryKey"`

	// GID is frozen after the first write, as PosixUser.UID, and unique.
	GID uint32 `gorm:"column:gid;uniqueIndex:idx_posix_groups_gid"`

	// PendingGID holds a differing directory gid the sync refused to apply,
	// and PendingSince the first sync that saw it.
	PendingGID   *uint32    `gorm:"column:pending_gid"`
	PendingSince *time.Time `gorm:"column:pending_since"`

	// Name is the mapped group name attribute. A separate namespace from
	// PosixUser.Name: a private group shares its user's name.
	Name string `gorm:"column:name;uniqueIndex:idx_posix_groups_name"`

	// DirectoryID and DN are the sync's two anchors, as on PosixUser,
	// including the empty DN of a released one.
	DirectoryID string `gorm:"column:directory_id;index:idx_posix_groups_directory_id"`
	DN          string `gorm:"column:dn;uniqueIndex:idx_posix_groups_dn"`

	// MissingSince is the first full sync that did not see the entry, nil
	// while it resolves. DeletedAt is the tombstone, set once MissingSince
	// is older than nss.tombstone_after and cleared if the entry reappears;
	// a *time.Time rather than gorm.DeletedAt for the reason PosixUser's
	// is.
	MissingSince *time.Time `gorm:"column:missing_since"`
	DeletedAt    *time.Time `gorm:"column:deleted_at"`

	// WithheldAt and WithheldReason mark a gid or name collision, as on
	// PosixUser.
	WithheldAt     *time.Time `gorm:"column:withheld_at"`
	WithheldReason string     `gorm:"column:withheld_reason"`

	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`

	// LastSyncedAt is the last sync or on-demand read that reached the
	// directory for this entry. Every row is born from one.
	LastSyncedAt time.Time `gorm:"column:last_synced_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (PosixGroup) TableName() string { return "posix_groups" }

// PosixGroupMember is one membership, by number: GID's group contains UID's
// account. Keyed by uid rather than users.id because members include
// accounts that never log in. There are no foreign keys, so an accepted uid
// or gid change re-keys these rows itself, in the same transaction.
type PosixGroupMember struct {
	GID uint32 `gorm:"column:gid;primaryKey"`
	UID uint32 `gorm:"column:uid;primaryKey;index:idx_posix_group_members_uid"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (PosixGroupMember) TableName() string { return "posix_group_members" }

// ForGroups reports whether the kind is about groups rather than accounts.
// NSSCollisionKind itself is declared in enums.go, so tygo generates it for
// the web UI.
func (k NSSCollisionKind) ForGroups() bool {
	return k == NSSCollisionKindGID || k == NSSCollisionKindGroupName
}

// NSSCollision is one directory entry contending for a value a stored row
// holds. While it exists the holder is withheld, and so is the other entry
// when it is stored too (OtherID set); when the sync refused to store the
// other entry, OtherID is nil and only its DN, name and number are known.
// The sync writes one each time it sees the pair and deletes it once the
// pair is gone.
type NSSCollision struct {
	// Kind names the contested value and, through ForGroups, the table
	// HolderID and OtherID belong to.
	Kind NSSCollisionKind `gorm:"column:kind;primaryKey;check:chk_nss_collisions_kind,kind IN ('uid','user_name','gid','group_name')"`

	// HolderID is the row holding the contested value: a PosixUser.ID or a
	// PosixGroup.ID by Kind.
	HolderID string `gorm:"column:holder_id;primaryKey;index:idx_nss_collisions_holder_id"`

	// OtherDN is the contending entry's DN as the directory returned it.
	OtherDN string `gorm:"column:other_dn;primaryKey;index:idx_nss_collisions_other_dn"`

	// Value is the contested uid, gid or name, as text.
	Value string `gorm:"column:value"`

	// OtherID is the contending entry's row when it is stored too, nil
	// when the sync refused to store it.
	OtherID *string `gorm:"column:other_id;index:idx_nss_collisions_other_id"`

	// OtherName and OtherNumber are what the contending entry carried when
	// last seen: its mapped name, and its uid or gid.
	OtherName   string `gorm:"column:other_name"`
	OtherNumber uint32 `gorm:"column:other_number"`

	FirstSeenAt time.Time `gorm:"column:first_seen_at"`
	LastSeenAt  time.Time `gorm:"column:last_seen_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (NSSCollision) TableName() string { return "nss_collisions" }
