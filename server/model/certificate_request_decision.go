package model

import "time"

// CertificateRequestDecision is the immutable audit record of a single
// Approve/Deny decision on a CertificateRequest: who decided it, their full
// identity as it stood at that moment, and the connection they decided it
// from. Exactly one row is ever inserted per request — never updated, never
// deleted — enforced by CertRequestService only ever calling INSERT here,
// and backstopped by the UNIQUE constraint on CertificateRequestID at the
// database level.
//
// This is deliberately its own table rather than columns on
// CertificateRequest: that struct backs the busy, read/write issuance
// pipeline (status transitions, the sweep), while this is an append-only
// log entry about one event in that pipeline's history. Keeping it separate
// means a decider's identity is frozen at decision time and can never be
// rewritten by a later change to the users table — there is deliberately no
// UserID foreign key here, only plain copied values.
type CertificateRequestDecision struct {
	ID string `gorm:"column:id;primaryKey"`

	// CertificateRequestID is the decided request. UNIQUE at the database
	// level: at most one decision per request, matching the guarded
	// UPDATE ... WHERE status = 'pending' in Approve/Deny that already
	// ensures only one caller ever wins the race to resolve a request. The
	// `unique` tag is included (unlike most unique columns in this
	// codebase, which rely on the migration alone) so AutoMigrate-backed
	// unit tests can exercise this invariant directly, not only the
	// slower migration-parity tests.
	CertificateRequestID string `gorm:"column:certificate_request_id;uniqueIndex"`

	// The UNIQUE constraint enforces "at most one decision per request" at
	// the database level, as defense in depth: the guarded UPDATE ...
	// WHERE status = 'pending' in CertRequestService.Approve/Deny already
	// ensures only one caller ever wins the race to resolve a given request.
	// This column is a plain copied ID (not a foreign key) for retention
	// consistency — the decisions table is permanent and append-only, while
	// certificate_requests may be pruned someday. An FK would block pruning
	// or silently delete audit records via CASCADE, both unacceptable. By
	// keeping a copied ID (like decider identity), the audit record outlives
	// the request, and retention can be applied per-table.

	// Outcome is "approved" or "denied" — see
	// model.CertificateRequestDecisionOutcome* in enums.go. The CHECK
	// constraint mirrors the migration's, for the same reason the `unique`
	// tag above is present: so AutoMigrate-backed tests build the real
	// constraint.
	Outcome CertificateRequestDecisionOutcome `gorm:"column:outcome;check:chk_certificate_request_decisions_outcome,outcome IN ('approved','denied')"`

	// Subject, Username, Email, Groups, OtherAccounts, and ServiceAccounts
	// are a full snapshot of service.Identity as it stood at decision
	// time — copied values, not a reference to the users table, so a later
	// change to that table (a rename, a re-provisioned account) can never
	// alter what a historical decision record shows. Groups, OtherAccounts,
	// and ServiceAccounts are JSON-encoded []string, matching this
	// project's existing JSON-in-TEXT convention for RequestedOptions.
	Subject         string `gorm:"column:subject"`
	Username        string `gorm:"column:username"`
	Email           string `gorm:"column:email"`
	Groups          string `gorm:"column:groups"`
	OtherAccounts   string `gorm:"column:other_accounts"`
	ServiceAccounts string `gorm:"column:service_accounts"`

	// SourceIP is the server-observed connecting address at decision time
	// (g.ClientIP(), trustworthy: SetTrustedProxies is always called — see
	// server/bootstrap/router.go).
	SourceIP string `gorm:"column:source_ip"`

	// UserAgent, AcceptLanguage, and ForwardedFor are a deliberate
	// allowlist of request headers, not "every header minus a denylist":
	// Cookie carries the live session token, and neither it nor
	// Authorization is ever captured here, per docs/internals/invariants.md's "never
	// log sensitive data" rule. ForwardedFor is the raw header, kept
	// alongside SourceIP because g.ClientIP() already collapses it to one
	// trusted value — the raw chain preserves forensic detail that
	// resolution throws away.
	UserAgent      string `gorm:"column:user_agent"`
	AcceptLanguage string `gorm:"column:accept_language"`
	ForwardedFor   string `gorm:"column:forwarded_for"`

	DecidedAt time.Time `gorm:"column:decided_at"`
}

// TableName overrides GORM's default pluralization to match the migration.
func (CertificateRequestDecision) TableName() string { return "certificate_request_decisions" }
