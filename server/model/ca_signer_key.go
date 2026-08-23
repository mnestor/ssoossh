package model

import "time"

// CASignerKey represents a CA public key announced by a signer. Multiple
// signers may announce the same key (HA setup); deduplication is by
// SHA256 fingerprint computed server-side at announce time.
type CASignerKey struct {
	// Fingerprint is the SSH SHA256 fingerprint of the key, computed and
	// normalized server-side (ssh.FingerprintSHA256). Primary key and dedup
	// mechanism, guaranteeing exactly one row per unique CA key.
	Fingerprint string `gorm:"primaryKey"`

	// PublicKey is the canonical authorized_keys format public key (normalized
	// after parsing and re-marshaling).
	PublicKey string

	// ExpiresAt is the expiry time, refreshed to now + ttl on every announce.
	ExpiresAt time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
}

// TableName overrides GORM's default pluralization to match the migration.
func (CASignerKey) TableName() string { return "ca_signer_keys" }
