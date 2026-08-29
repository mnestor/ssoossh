package model

import "time"

// User represents an identity that has authenticated via OIDC (optionally
// enriched with LDAP attributes, see config.LDAPConfig). It exists mainly
// to key certificate history and enrollments to a stable identifier across
// logins, since group membership and other claims can change between
// sessions — group membership itself is never persisted here or placed in
// a certificate (see docs/internals/invariants.md).
type User struct {
	ID       string `gorm:"column:id;primaryKey"`
	Subject  string `gorm:"column:subject;uniqueIndex:idx_users_subject"` // OIDC "sub" claim, unique per provider
	Username string `gorm:"column:username"`
	Email    string `gorm:"column:email"`

	// OtherAccounts and ServiceAccounts are JSON-encoded []string, same
	// convention as model.Certificate.Extensions. See
	// config.OAuthFields.OtherAccounts / .ServiceAccounts.
	OtherAccounts   string `gorm:"column:other_accounts"`
	ServiceAccounts string `gorm:"column:service_accounts"`

	// ExtraFields is a JSON-encoded map of the operator-configured extra
	// claim fields captured at login (config.OAuthFields.Extra): template
	// name -> string or array of strings. Consumed by key ID templates at
	// approval time via service.Identity.Extra.
	ExtraFields string `gorm:"column:extra_fields"`

	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`

	// DisabledAt records when the user was disabled by an admin. Nil means
	// not disabled. A disabled user cannot authenticate and is excluded
	// from notification fan-out; their service enrollments are untouched,
	// belonging to the accounts rather than to them (see
	// docs/proposals/enrollment-group-ownership.md).
	DisabledAt *time.Time `gorm:"column:disabled_at"`

	// DisabledByUserID records which admin user disabled this user (foreign
	// key to users.id). Nil when DisabledAt is nil.
	DisabledByUserID *string `gorm:"column:disabled_by_user_id"`

	// DisabledSource says what performed the disable: admin, soc, or
	// ldap_sync. It complements DisabledByUserID, which is a users.id and
	// so cannot represent the system actor (it stays NULL for ldap_sync).
	// The directory sync clears only disables whose source is exactly
	// ldap_sync, which is what keeps an operator's disable from being
	// undone automatically. NULL on rows predating the column.
	DisabledSource *DisabledSource `gorm:"column:disabled_source"`

	// DisabledReason is why the user was disabled, required at the API and
	// recorded here as denormalized current state so the person deciding
	// whether to re-enable sees it without reading the audit trail. The
	// audit trail is the history; this is the current state, and it
	// survives audit pruning. Empty when not disabled.
	DisabledReason string `gorm:"column:disabled_reason"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (User) TableName() string { return "users" }
