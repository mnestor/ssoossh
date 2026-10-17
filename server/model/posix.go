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
	// against.
	DN string `gorm:"column:dn;uniqueIndex:idx_posix_users_dn"`

	// UserID links the person, set at login when the LDAP enrichment
	// resolves this entry. Nil for shared and service accounts and for
	// anyone who has not logged in.
	UserID *string `gorm:"column:user_id;uniqueIndex:idx_posix_users_user_id"`

	// GID is the mapped primary gid, frozen like UID. auto_private_groups
	// is applied when the row is read, never stored here.
	GID uint32 `gorm:"column:gid"`

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

	// MissingSince is the first sync that could not find the entry by id,
	// DN or filter. Nil while the entry resolves.
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
	// WithheldReason names the other entry in a short sentence for the
	// operator, and is empty while the row is not withheld.
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
// with the mapped gid attribute, or a synthetic private group created by
// nss.auto_private_groups.
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

	// DirectoryID and DN are the sync's two anchors, as on PosixUser. Both
	// are empty on a synthetic group, which has no entry.
	DirectoryID string `gorm:"column:directory_id;index:idx_posix_groups_directory_id"`
	DN          string `gorm:"column:dn;index:idx_posix_groups_dn"`

	// Synthetic is true for a private group auto_private_groups created.
	Synthetic bool `gorm:"column:synthetic"`

	// WithheldAt and WithheldReason mark a gid or name collision, as on
	// PosixUser.
	WithheldAt     *time.Time `gorm:"column:withheld_at"`
	WithheldReason string     `gorm:"column:withheld_reason"`

	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
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
