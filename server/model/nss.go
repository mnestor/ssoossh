package model

import "time"

// NSSKey is one fleet API key: the credential a fleet of hosts presents to
// the /api/nss/* lookup endpoints. See
// docs/proposals/nss-directory/1-server-api.md, "Fleet key authentication".
//
// The secret itself is never stored, only its SHA-256. A rotation moves the
// current hash to PreviousSecretHash, which stays accepted until
// PreviousExpiresAt so hosts can switch over.
type NSSKey struct {
	// ID is "nss_" and 8 base32 characters, fixed for life. Unlike the
	// secret it may be logged.
	ID string `gorm:"column:id;primaryKey"`

	// CurrentGeneration is 1 at creation and grows by one per rotation.
	CurrentGeneration int `gorm:"column:current_generation"`

	// SecretHash is the SHA-256 of the current secret. The secret is 256
	// random bits, so no key derivation is needed.
	SecretHash string `gorm:"column:secret_hash"`

	// PreviousSecretHash is generation CurrentGeneration-1, accepted until
	// PreviousExpiresAt. Both nil when there is no previous secret.
	PreviousSecretHash *string    `gorm:"column:previous_secret_hash"`
	PreviousExpiresAt  *time.Time `gorm:"column:previous_expires_at"`

	Name        string `gorm:"column:name"`
	Description string `gorm:"column:description"`

	// CreatedBy is the users.id of whoever created the key.
	CreatedBy string `gorm:"column:created_by"`

	// GroupName is the optional group whose members manage this key; empty
	// for none. The column is group_name because GROUP is a reserved word
	// in both dialects.
	GroupName string `gorm:"column:group_name"`

	// ExpiresAt is when the key stops being accepted, at most
	// nss.key_max_lifetime after creation and by default exactly that.
	// Nil when that setting is 0 and the creator chose no date: the key
	// never expires. Fixed once the key exists.
	ExpiresAt *time.Time `gorm:"column:expires_at"`

	// NotificationEmail is where the key's mail goes; it defaults to the
	// creator's address.
	NotificationEmail string `gorm:"column:notification_email"`

	// RevokedAt and RevokedBy (a users.id) record a revocation. Nil while
	// the key is live.
	RevokedAt *time.Time `gorm:"column:revoked_at"`
	RevokedBy *string    `gorm:"column:revoked_by"`

	// RotatedAt is the last rotation, where mail.rotation_quiet_period
	// starts. Nil for a key never rotated.
	RotatedAt *time.Time `gorm:"column:rotated_at"`

	// ExpiryReminderSentAt, LastExpiredAttemptNotifiedAt and
	// RotationCleanupSentAt are notification claims, each taken with a
	// guarded UPDATE so every instance can run every job without any of
	// them sending twice: the expiring reminder, the expired and revoked
	// attempt mails, and the daily rotation cleanup digest. They play the
	// roles Enrollment.ExpiryReminderSentAt and
	// Enrollment.LastExpiredAttemptNotifiedAt play for service codes. Nil
	// means never sent.
	ExpiryReminderSentAt         *time.Time `gorm:"column:expiry_reminder_sent_at"`
	LastExpiredAttemptNotifiedAt *time.Time `gorm:"column:last_expired_attempt_notified_at"`
	RotationCleanupSentAt        *time.Time `gorm:"column:rotation_cleanup_sent_at"`

	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (NSSKey) TableName() string { return "nss_keys" }

// NSSKeyUsage is one key's lookups from one source address with one
// generation of its secret on one day, for the usage and rotation reports.
// Written by a flusher that aggregates in memory, never once per lookup, and
// pruned at nss.usage_retention.
type NSSKeyUsage struct {
	KeyID string `gorm:"column:key_id;primaryKey;index:idx_nss_key_usage_key_day,priority:1"`

	// SourceIP is the connection's address as the server observed it.
	SourceIP string `gorm:"column:source_ip;primaryKey"`

	// Generation is the generation the host presented: current or
	// current-1.
	Generation int `gorm:"column:generation;primaryKey"`

	// Day is midnight UTC of the day the counts belong to.
	Day time.Time `gorm:"column:day;primaryKey;index:idx_nss_key_usage_key_day,priority:2;index:idx_nss_key_usage_day"`

	Lookups int64 `gorm:"column:lookups"`
	Misses  int64 `gorm:"column:misses"`

	FirstSeen time.Time `gorm:"column:first_seen"`
	LastSeen  time.Time `gorm:"column:last_seen"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (NSSKeyUsage) TableName() string { return "nss_key_usage" }

// NSSAlert records when one NSS alert was last raised, so an alert meant to
// fire at most once a day per subject does so across every instance: the
// raiser claims the row with a guarded UPDATE (or the insert) and writes
// the audit event only when it won. A claim marker, not an audit record; a
// row may be deleted freely, which only lets the next alert of its kind
// through early.
type NSSAlert struct {
	// Kind names the alert: uid_collision, name_collision,
	// id_change_pending, name_mismatch, privileged_name, key_source and the
	// like.
	Kind string `gorm:"column:kind;primaryKey"`

	// Subject is what the alert is about: a uid, a name, a row id or a key
	// id.
	Subject string `gorm:"column:subject;primaryKey"`

	LastRaisedAt time.Time `gorm:"column:last_raised_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (NSSAlert) TableName() string { return "nss_alerts" }

// NSS audit reference kinds: what NSSAuditRef.RowID names.
const (
	// NSSAuditRefAccount is a posix_users.id.
	NSSAuditRefAccount = "account"
	// NSSAuditRefGroup is a posix_groups.id.
	NSSAuditRefGroup = "group"
)

// NSSAuditRef ties one audit event to one stored NSS account or group it
// concerns, so the viewer's history of that row reads an index rather than
// matching JSON (decided 2026-10-18 by the owner). Like the indexed columns
// of AuditEvent it is a grouping key, never authoritative: the payload is.
//
// RowID is the row's random id, which survives a rename and an accepted
// uid or gid change where the name and the number do not. It has no
// foreign key, so a purged row's history stays until its events age out.
// EventID does: the ref is deleted with its event by the audit sweep.
// CreatedAt copies the event's, so a page orders and seeks on this table
// alone.
type NSSAuditRef struct {
	EventID   string    `gorm:"column:event_id;primaryKey"`
	Kind      string    `gorm:"column:kind;primaryKey;index:idx_nss_audit_refs_row,priority:1"`
	RowID     string    `gorm:"column:row_id;primaryKey;index:idx_nss_audit_refs_row,priority:2"`
	CreatedAt time.Time `gorm:"column:created_at;index:idx_nss_audit_refs_row,priority:3"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (NSSAuditRef) TableName() string { return "nss_audit_refs" }
