package model

import "time"

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

	// KeyID is the free-form audit-trail string sshd logs on every
	// authentication, produced by executing the applicable
	// config.CertOptions*.KeyIDTemplate (see
	// docs/certificate-keyid-template.md).
	KeyID string `gorm:"column:key_id"`

	// Principals is a comma-separated list. TODO: move to a join table if
	// querying by individual principal becomes necessary.
	Principals string `gorm:"column:principals"`

	// CriticalOptions is a JSON-encoded map[string]string of granted SSH
	// certificate critical options (force-command, source-address — see
	// docs/what-ssoossh-is.md "Certificate terms"). Unlike Extensions,
	// sshd rejects the certificate outright if it doesn't understand one
	// of these, so an empty map here is meaningfully different from an
	// empty Extensions list.
	CriticalOptions string `gorm:"column:critical_options"`

	// Extensions is a JSON-encoded []string of granted SSH certificate
	// extensions (see config.CertOptionsUser.Extensions /
	// CertOptionsService.Extensions).
	Extensions string `gorm:"column:extensions"`

	IssuedAt  time.Time `gorm:"column:issued_at"`
	ExpiresAt time.Time `gorm:"column:expires_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (Certificate) TableName() string { return "certificates" }
