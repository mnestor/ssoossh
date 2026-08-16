package model

import "time"

// User represents an identity that has authenticated via OIDC (optionally
// enriched with LDAP attributes, see config.LDAPConfig). It exists mainly
// to key certificate history and enrollments to a stable identifier across
// logins, since group membership and other claims can change between
// sessions — group membership itself is never persisted here or placed in
// a certificate (see root CLAUDE.md Hard Constraints).
type User struct {
	ID       string `gorm:"column:id;primaryKey"`
	Subject  string `gorm:"column:subject"` // OIDC "sub" claim, unique per provider
	Username string `gorm:"column:username"`
	Email    string `gorm:"column:email"`

	// OtherAccounts and ServiceAccounts are JSON-encoded []string, same
	// convention as model.Certificate.Extensions. See
	// config.OAuthFields.OtherAccounts / .ServiceAccounts.
	OtherAccounts   string `gorm:"column:other_accounts"`
	ServiceAccounts string `gorm:"column:service_accounts"`

	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`

	// TODO: LDAP-sourced fields once identity enrichment is implemented.
}

// TableName overrides GORM's default pluralization to match the migration.
func (User) TableName() string { return "users" }
