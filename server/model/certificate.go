package model

import "time"

// Certificate records an issued SSH certificate for audit / per-user
// history purposes. The private key is never generated or stored by the
// server (see https://mnestor.github.io/ssoossh/internals/invariants/) — only public key
// fingerprint and certificate metadata.
type Certificate struct {
	ID string `gorm:"column:id;primaryKey"`

	// Type carries a CHECK constraint mirroring the migration's, so the
	// AutoMigrate-backed unit tests build the same constraint the real
	// schema has (same reasoning as CertificateRequestDecision's `unique`
	// tag). Adding a CertificateType means updating this tag and both
	// migrations alongside enums.go.
	Type CertificateType `gorm:"column:type;check:chk_certificates_type,type IN ('user','service','pam','console')"`
	// Nil only when the owner could not be resolved at issuance -- the
	// request was never bound to a user, or the lookup failed. See
	// SignedReplyHandler.recordCertificate, which logs both cases and
	// still records CertificateRequestID so the row stays reattachable.
	UserID *string `gorm:"column:user_id"`

	// CertificateRequestID is the request whose approval authorized this
	// certificate, closing the audit chain certificate_request -> decision
	// -> certificate. Without it a certificate row whose owner could not be
	// resolved (see SignedReplyHandler, which treats that as non-fatal
	// because the certificate is already signed and delivered) would be
	// permanently orphaned, with nothing to reattach it by.
	//
	// A pointer, and nullable in the schema, deliberately: the signer's
	// reply always carries the request ID so this is always populated in
	// practice, but a NOT NULL column would add a second way for the
	// audit-record write to fail and lose the record entirely.
	CertificateRequestID *string `gorm:"column:certificate_request_id"`

	PublicKeyFingerprint string `gorm:"column:public_key_fingerprint"`
	// SerialNumber is pre-allocated at approval time (before signing is queued),
	// ensuring it's available to persist at request resolution without waiting
	// for the signer. The UNIQUE constraint converts collisions into failed
	// inserts rather than silently revoking unrelated certificates.
	SerialNumber uint64 `gorm:"column:serial_number;uniqueIndex:idx_certificates_serial_number"`

	// KeyID is the free-form audit-trail string sshd logs on every
	// authentication, produced by executing the applicable
	// config.CertOptions*.KeyIDTemplate (see
	// https://mnestor.github.io/ssoossh/concepts/ (key ID templating)).
	KeyID string `gorm:"column:key_id"`

	// Principals is a comma-separated list. TODO: move to a join table if
	// querying by individual principal becomes necessary.
	Principals string `gorm:"column:principals"`

	// CriticalOptions is a JSON-encoded map[string]string of granted SSH
	// certificate critical options (force-command, source-address — see
	// https://mnestor.github.io/ssoossh/concepts/, issuance). Unlike Extensions,
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
