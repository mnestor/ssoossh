package model

import "time"

// CertificateType identifies which of the three certificate types
// (docs/ssoossh-context.md — "Certificate types") a row represents.
type CertificateType string

const (
	CertificateTypeUser    CertificateType = "user"
	CertificateTypeHost    CertificateType = "host"
	CertificateTypeService CertificateType = "service"
	CertificateTypePAM     CertificateType = "pam"
)

// Certificate records an issued SSH certificate for audit / per-user
// history purposes. The private key is never generated or stored by the
// server (see root CLAUDE.md Hard Constraints) — only public key
// fingerprint and certificate metadata.
type Certificate struct {
	ID     string          `gorm:"column:id;primaryKey"`
	Type   CertificateType `gorm:"column:type"`
	UserID *string         `gorm:"column:user_id"` // nil for host certs, which identify a machine, not a user

	// Hostname is set only for CertificateTypeHost.
	Hostname string `gorm:"column:hostname"`

	PublicKeyFingerprint string `gorm:"column:public_key_fingerprint"`
	SerialNumber         uint64 `gorm:"column:serial_number"`

	// Principals is a comma-separated list. TODO: move to a join table if
	// querying by individual principal becomes necessary.
	Principals string `gorm:"column:principals"`

	// Extensions is a JSON-encoded []string of granted SSH certificate
	// extensions (see config.CertOptionsUser.Extensions /
	// CertOptionsService.Extensions).
	Extensions string `gorm:"column:extensions"`

	IssuedAt  time.Time `gorm:"column:issued_at"`
	ExpiresAt time.Time `gorm:"column:expires_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (Certificate) TableName() string { return "certificates" }
