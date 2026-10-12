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
// https://mnestor.github.io/ssoossh/internals/invariants/).
//
// This table is a bounded cache, not the archive. Real retention and search
// happen in whatever external log system the deployment ships the
// type=audit slog destination to; the rows here exist only to serve the
// UI's recent-history views and are pruned on a schedule. That is why the
// log emit is unconditional for every event while the table copy is
// skipped for some.
type AuditEvent struct {
	ID string `gorm:"column:id;primaryKey;index:idx_audit_events_action,priority:3;index:idx_audit_events_source_ip,priority:3;index:idx_audit_events_action_source_ip,priority:4"`

	CreatedAt time.Time `gorm:"column:created_at;index:idx_audit_events_created_at;index:idx_audit_events_action,priority:2;index:idx_audit_events_action_actor,priority:3;index:idx_audit_events_source_ip,priority:2;index:idx_audit_events_action_source_ip,priority:3"`

	// ActorUserID and TargetUserID exist solely so the UI can ask
	// "everything this account did" and "everything done to this account"
	// without a JSON scan across both sqlite and postgres. They are
	// grouping keys, never references and never authoritative — the payload
	// is. If a user row is later deleted or renamed the timeline still
	// reads correctly from the payloads; the column merely stops matching.
	//
	// ActorUserID is NULL for system and anonymous actions; TargetUserID is
	// NULL when an action is not about a particular user.
	ActorUserID  *string `gorm:"column:actor_user_id;index:idx_audit_events_actor,priority:1;index:idx_audit_events_action_actor,priority:2"`
	TargetUserID *string `gorm:"column:target_user_id;index:idx_audit_events_target,priority:1"`

	// Action copies the payload's action so a filter on one action reads an
	// index rather than matching JSON. The same rule as the two user
	// columns applies: a grouping key, never authoritative. The recorder
	// sets it from the same event it encodes into Payload (see
	// service.AuditService.prepare), so the two cannot disagree. A row
	// whose payload could not be read when the column was backfilled holds
	// "", which matches no action filter.
	//
	// Two indexes serve the two query shapes: (action, created_at, id) for
	// "the rows with action X, oldest or newest first", and
	// (action, actor_user_id, created_at) for "each user's latest row with
	// action X".
	Action string `gorm:"column:action;not null;default:'';index:idx_audit_events_action,priority:1;index:idx_audit_events_action_actor,priority:1;index:idx_audit_events_action_source_ip,priority:1"`

	// SourceIP copies the payload's detail.source_ip, in canonical form, so
	// the admin feed can filter to one address from an index. Same rule
	// again: a grouping key, never authoritative, set by the recorder from
	// the event it encodes (service.AuditService.prepare). It is the address
	// the server observed for the client the event is about, never one the
	// client supplied; see service.auditSourceIP for which address that is
	// per action. "" when the event has no observed source, or its
	// source_ip is not an address.
	//
	// Two indexes: (source_ip, created_at, id) for "the rows from address
	// X, newest first", and (action, source_ip, created_at, id) for both
	// filters at once.
	SourceIP string `gorm:"column:source_ip;not null;default:'';index:idx_audit_events_source_ip,priority:1;index:idx_audit_events_action_source_ip,priority:2"`

	// Payload is the actual record: a JSON document carrying the payload
	// schema version, the action, the actor and target snapshots, the
	// reason where one is required, and per-action detail. Never an
	// enrollment code or any other secret — the never-log-sensitive-data
	// rule applies to payloads and log lines alike.
	Payload string `gorm:"column:payload"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (AuditEvent) TableName() string { return "audit_events" }
