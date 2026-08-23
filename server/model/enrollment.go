package model

import "time"

// Enrollment binds an approved service-certificate option set to a public
// key. Created when a CertificateTypeService CertificateRequest is
// approved. After that, `service retrieve` calls present only Code — the
// server never re-accepts a public key for an existing enrollment, so a
// stolen code can't be paired with an attacker's keypair (see
// docs/dev/ssoossh-context.md, "Service enrollment").
type Enrollment struct {
	ID        string `gorm:"column:id;primaryKey"`
	Code      string `gorm:"column:code"`
	PublicKey string `gorm:"column:public_key"`

	// OptionSet is JSON-encoded, fixed at approval time.
	OptionSet string `gorm:"column:option_set"`

	UserID    string    `gorm:"column:user_id"` // who approved this enrollment
	CreatedAt time.Time `gorm:"column:created_at"`
	ExpiresAt time.Time `gorm:"column:expires_at"`

	// RedeemedAt is set on first successful `service retrieve`.
	// TODO: decide (see docs/dev/ssoossh-context.md open questions) whether
	// enrollments are single-use or reusable until ExpiresAt, and whether
	// proof-of-possession is required at retrieve time.
	RedeemedAt *time.Time `gorm:"column:redeemed_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (Enrollment) TableName() string { return "enrollments" }
