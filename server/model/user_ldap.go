package model

import "time"

// GroupSource says where a persisted group membership came from. The two
// sources never collide and either can be queried alone.
type GroupSource string

const (
	// GroupSourceOIDC is a group from the ID token's groups claim,
	// captured at every login.
	GroupSourceOIDC GroupSource = "oidc"
	// GroupSourceLDAP is a group read from the directory, captured at
	// login and refreshed by the sync.
	GroupSourceLDAP GroupSource = "ldap"
)

// DisabledSource says what disabled a user, which is what makes
// auto-re-enable safe: the sync only ever clears disables it caused.
// Complements User.DisabledByUserID, which is a users.id and so cannot
// represent the system actor.
type DisabledSource string

const (
	DisabledSourceAdmin DisabledSource = "admin"
	DisabledSourceSOC   DisabledSource = "soc"
	// DisabledSourceLDAPSync marks a disable the directory sync performed
	// because the entry stopped resolving. Only these may be cleared
	// automatically.
	DisabledSourceLDAPSync DisabledSource = "ldap_sync"
)

// UserLDAP is the sync bookkeeping for one user's directory entry: where it
// is, what was last read from it, and how many consecutive successful
// searches have failed to find it.
//
// Separate from users because the lifecycles differ: this row exists only
// while a user participates in directory sync, and it is written by a
// background job rather than by login.
type UserLDAP struct {
	UserID string `gorm:"column:user_id;primaryKey"`

	// DN is the entry's distinguished name from the login-time search, and
	// the load-bearing column here. Login searches by filter once; the sync
	// re-reads by DN, which is cheaper and distinguishes "entry deleted"
	// from "filter no longer matches". A DN read that fails falls back to
	// one filter search before counting a miss, so a moved entry
	// re-anchors instead of being disabled.
	DN string `gorm:"column:dn"`

	// Attributes is the JSON-encoded map of fetched field values, including
	// extras and resolved account lists. It doubles as a last-known-good
	// cache: when the login-time lookup fails (LDAP fails open), enrichment
	// falls back to these, so a directory outage degrades to slightly stale
	// data rather than a thinner certificate.
	Attributes string `gorm:"column:attributes"`

	// LastSeenAt is the last successful read of the entry.
	LastSeenAt *time.Time `gorm:"column:last_seen_at"`

	// LastSyncedAt is the last sync attempt that reached the directory at
	// all, successful or not. It differs from LastSeenAt precisely when the
	// entry is missing, which is the state the miss counter tracks.
	LastSyncedAt *time.Time `gorm:"column:last_synced_at"`

	// ConsecutiveMisses counts *successful* searches that found no entry.
	// A directory outage never increments it: only a search that succeeds
	// and finds nothing is a miss, so an outage can never disable anyone.
	ConsecutiveMisses int `gorm:"column:consecutive_misses"`

	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (UserLDAP) TableName() string { return "user_ldap" }

// UserGroup is one persisted group membership, for notification fan-out and
// display.
//
// Rows rather than JSON so "everyone in soc" is one indexed query.
//
// **Never an authorization input.** Authorization — admin, SOC, auditor,
// and certificate policy gates — is evaluated from the session identity
// only, and this table answers "who should this reach", never "may this
// caller do this". See docs/internals/invariants.md.
//
// Only group names the configuration references are persisted; membership
// outside that allowlist is discarded at capture time. The server does not
// mirror the directory's group graph, it records the roles it acts on.
type UserGroup struct {
	ID     string `gorm:"column:id;primaryKey"`
	UserID string `gorm:"column:user_id;index:idx_user_groups_user"`

	// GroupName is the reduced, comparable name — a DN from memberOf is
	// reduced to its CN before it reaches here.
	GroupName string `gorm:"column:group_name;index:idx_user_groups_name"`

	// Source distinguishes the two capture paths so they never collide and
	// either can be queried alone.
	Source GroupSource `gorm:"column:source"`

	FirstSeenAt time.Time `gorm:"column:first_seen_at"`
	LastSeenAt  time.Time `gorm:"column:last_seen_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (UserGroup) TableName() string { return "user_groups" }
