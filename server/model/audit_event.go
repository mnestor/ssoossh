package model

import "time"

// AuditEvent is one entry in the append-only administrative audit stream:
// who did what, to whom, and when. Rows are inserted and never updated or
// deleted except by the retention sweep.
//
// An audit entry must read the same in five years as it did the day it was
// written, so it references nothing that can change. There are deliberately
// no foreign keys here: a rename would change what an entry appears to say
// and a deleted row would make it say nothing. Identity is instead copied
// into Payload as a literal snapshot taken at event time — the same posture
// CertificateRequestDecision takes, and consistent with how this codebase
// already treats identity, since group membership is never persisted
// precisely because claims drift between logins (see
// docs/internals/invariants.md).
//
// This table is a bounded cache, not the archive. Real retention and search
// happen in whatever external log system the deployment ships the
// type=audit slog destination to; the rows here exist only to serve the
// UI's recent-history views and are pruned on a schedule. That is why the
// log emit is unconditional for every event while the table copy is
// skipped for some.
type AuditEvent struct {
	ID string `gorm:"column:id;primaryKey"`

	CreatedAt time.Time `gorm:"column:created_at;index:idx_audit_events_created_at"`

	// ActorUserID and TargetUserID exist solely so the UI can ask
	// "everything this account did" and "everything done to this account"
	// without a JSON scan across both sqlite and postgres. They are
	// grouping keys, never references and never authoritative — the payload
	// is. If a user row is later deleted or renamed the timeline still
	// reads correctly from the payloads; the column merely stops matching.
	//
	// ActorUserID is NULL for system and anonymous actions; TargetUserID is
	// NULL when an action is not about a particular user.
	ActorUserID  *string `gorm:"column:actor_user_id;index:idx_audit_events_actor,priority:1"`
	TargetUserID *string `gorm:"column:target_user_id;index:idx_audit_events_target,priority:1"`

	// Payload is the actual record: a JSON document carrying the payload
	// schema version, the action, the actor and target snapshots, the
	// reason where one is required, and per-action detail. Never an
	// enrollment code or any other secret — the never-log-sensitive-data
	// rule applies to payloads and log lines alike.
	Payload string `gorm:"column:payload"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (AuditEvent) TableName() string { return "audit_events" }
