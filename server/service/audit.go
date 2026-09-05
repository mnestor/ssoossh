package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/logging"
	"github.com/mnestor/ssoossh/server/model"
)

// AuditAction is a namespaced action name in the audit taxonomy. They are
// strings inside the payload rather than a database enum, so the set grows
// without a migration.
type AuditAction string

// The taxonomy. Namespaced by subject area: authentication, certificates,
// enrollments, user containment, and privileged views.
const (
	// AuditAuthLogin records a successful login, snapshotting the groups
	// the identity carried at that moment. Since group membership is never
	// persisted (see docs/internals/invariants.md), this is in fact the only
	// durable record of what access an identity held on a given day.
	AuditAuthLogin AuditAction = "auth.login"
	// AuditAuthLoginDenied records a disabled account attempting to log in.
	// There is deliberately no logout event: sessions mostly end by expiry,
	// so an explicit logout carries too little signal to keep.
	AuditAuthLoginDenied AuditAction = "auth.login_denied"

	AuditCertRequested AuditAction = "cert.requested"
	AuditCertApproved  AuditAction = "cert.approved"
	AuditCertDenied    AuditAction = "cert.denied"
	// AuditCertCodeResolved records a console login code being typed into
	// the web UI and resolving. It is the moment a request created by an
	// unauthenticated machine acquires a named human, which is the step the
	// consent-phishing case in docs/proposals/console-login-pam.md turns
	// on: an approval that follows a phone call looks exactly like one that
	// does not, except that this event names who was talked into it and
	// which machine's console they were told about.
	AuditCertCodeResolved AuditAction = "cert.code_resolved"
	// AuditCertIssued goes to the shipped log only, never the table — see
	// tableSkipped. The UI already has certificate history from the
	// certificates table, so a table copy would be pure duplication; the
	// archive line, which an incident reviewer joins against target-host
	// sshd logs, is the valuable part.
	AuditCertIssued AuditAction = "cert.issued"
	// AuditCertClaimed records the first browser opening an approval page
	// (see CertRequestService.ClaimApprovalPage). It is the moment a link
	// that was sent somewhere turns into a page in front of someone, and
	// the event a reviewer reads to learn whether a link was opened at all
	// and from what user agent, before any decision was made.
	AuditCertClaimed AuditAction = "cert.claimed"
	// AuditCertExpired records a request nobody answered within its budget.
	// System event: there is no actor, which is the point of recording it.
	AuditCertExpired AuditAction = "cert.expired"
	// AuditCertSignFailed records an approval that produced no certificate:
	// the signer refused the job, or the stranded-request sweep found the
	// row stuck in signing. Without it an approval that never became a
	// certificate reads exactly like one that did.
	AuditCertSignFailed AuditAction = "cert.sign_failed"

	AuditEnrollmentCodeCreated AuditAction = "enrollment.code_created"
	AuditEnrollmentRedeemed    AuditAction = "enrollment.redeemed"
	AuditEnrollmentExpired     AuditAction = "enrollment.expired"

	// AuditEnrollmentNotificationEmailSet records a change to an
	// enrollment's notification address. Audited because it redirects every
	// subsequent notification about a credential to an address of the
	// setter's choosing — an unremarkable convenience, and also the quiet
	// way to stop an account's holders hearing about their own code.
	//
	// The address itself is in the detail: it is operator-entered
	// configuration in an admin-only log, not a user secret, and an event
	// that recorded only "the address changed" would not answer the
	// question anyone reads it for.
	AuditEnrollmentNotificationEmailSet AuditAction = "enrollment.notification_email_set"

	// AuditEnrollmentReassigned is no longer emitted: group ownership
	// removed reassignment (see
	// docs/proposals/enrollment-group-ownership.md). The constant stays so
	// events recorded before that still resolve to a name rather than a
	// raw string in every reader of the log.
	AuditEnrollmentReassigned AuditAction = "enrollment.reassigned"

	AuditUserDisabled AuditAction = "user.disabled"
	AuditUserEnabled  AuditAction = "user.enabled"
	// AuditUserAutoDisabled is a system action, so it carries no actor.
	AuditUserAutoDisabled AuditAction = "user.auto_disabled"

	// Privileged detail views. List views are deliberately not audited:
	// a row per page of the user directory says nothing. If a list query
	// ever needs auditing, record it as one event carrying the search
	// parameters.
	AuditAdminUserViewed       AuditAction = "admin.user_viewed"
	AuditAdminEnrollmentViewed AuditAction = "admin.enrollment_viewed"
	// AuditAdminConfigViewed is no longer emitted. The effective-config
	// screen is read-only and gets reloaded constantly while an operator
	// works, so the event arrived several times a minute and buried the
	// decisions this log exists to record. The constant stays so events
	// recorded before that still resolve to a name rather than a raw
	// string in every reader of the log.
	AuditAdminConfigViewed AuditAction = "admin.config_viewed"
	// AuditAdminAuditViewed is one event per visit to the audit feed, not
	// one per event displayed — which settles the recursion question.
	AuditAdminAuditViewed AuditAction = "admin.audit_viewed"
)

// auditPayloadVersion is the payload schema version, carried on every
// event from day one so a future shape change never has to guess what it
// is reading.
const auditPayloadVersion = 1

// maxAuditReasonLength caps a required reason. Long enough for a sentence
// and a ticket reference, short enough that the column cannot be used as
// free storage.
const maxAuditReasonLength = 1000

// tableSkipped reports whether an action is emitted to the shipped log
// only. Keeping this a function of the action (rather than a decision at
// each call site) is what stops the two sinks drifting apart.
func tableSkipped(action AuditAction) bool {
	return action == AuditCertIssued
}

// AuditSubject is the snapshot of one identity as it stood at event time:
// literal copied values, never a reference. See model.AuditEvent.
type AuditSubject struct {
	UserID   string   `json:"user_id,omitempty"`
	Subject  string   `json:"subject,omitempty"`
	Username string   `json:"username,omitempty"`
	Email    string   `json:"email,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

// AuditEvent is one event on its way to the two sinks. Both are fed from
// this one struct so they cannot drift.
type AuditEvent struct {
	Action AuditAction `json:"action"`

	// Actor is who performed the action, or nil for a system or anonymous
	// one. ActorUserID mirrors Actor.UserID into the indexed column.
	Actor *AuditSubject `json:"actor,omitempty"`
	// System marks an action taken by the server itself rather than a
	// person, so a reader can tell "nobody" from "not recorded".
	System bool `json:"system,omitempty"`

	// Target is who the action was done to, or nil when it is about nobody
	// in particular.
	Target *AuditSubject `json:"target,omitempty"`

	// Reason is required on the containment and restorative actions; see
	// ValidateAuditReason.
	Reason string `json:"reason,omitempty"`

	// Detail carries the per-action specifics: certificate serial, key ID,
	// principals, enrollment ID, source IP, search parameters. Never an
	// enrollment code or any other secret.
	Detail map[string]any `json:"detail,omitempty"`

	// OccurredAt is when the action happened. Zero means "now", filled in
	// by the recorder.
	OccurredAt time.Time `json:"occurred_at"`

	// V is the payload schema version, set by the recorder.
	V int `json:"v"`
}

// reasonRequired lists the actions whose reason is required and
// server-validated. These are the containment and restorative ones: the
// next person deciding whether to re-enable an account, or looking at an
// expired enrollment, is the reader this exists for. Optional reason fields
// do not get filled; required ones cost seconds at action time and are the
// whole point later.
//
// AuditEnrollmentReassigned is absent because nothing emits it any more.
var reasonRequired = map[AuditAction]bool{
	AuditUserDisabled:      true,
	AuditUserEnabled:       true,
	AuditEnrollmentExpired: true,
}

// ValidateAuditReason checks a caller-supplied reason for an action that
// requires one. Returns the trimmed reason. Actions that require no reason
// accept anything, including empty.
func ValidateAuditReason(action AuditAction, reason string) (string, error) {
	trimmed := strings.TrimSpace(reason)
	if !reasonRequired[action] {
		return trimmed, nil
	}
	if trimmed == "" {
		return "", fmt.Errorf("a reason is required for %s", action)
	}
	if len(trimmed) > maxAuditReasonLength {
		return "", fmt.Errorf("the reason for %s is too long (%d characters, limit %d)", action, len(trimmed), maxAuditReasonLength)
	}
	return trimmed, nil
}

// AuditService records administrative audit events to the two sinks.
//
// The event bus is deliberately not the write path here: the in-process
// gochannel is non-persistent, so a crash between "user disabled" and
// "event consumed" would lose the audit row, which is the one failure mode
// an audit log exists to prevent. Mutations instead append their event in
// the same transaction as the state change they describe, making a disable
// without its audit row unrepresentable.
type AuditService struct {
	db     *gorm.DB
	config *config.Config
	// log is the type=audit tagged logger, the durable export. It is
	// written for every event, unconditionally, because the table copy is
	// disposable.
	log *slog.Logger
}

// NewAuditService builds the recorder. The tagged logger is resolved once
// here rather than per event.
func NewAuditService(c *config.Config, db *gorm.DB) *AuditService {
	return &AuditService{db: db, config: c, log: logging.Tagged(logging.TagAudit)}
}

// RecordTx appends event inside tx, so the audit row commits with the state
// change it describes or not at all. A failure here must fail the
// transaction — that is the entire point.
func (s *AuditService) RecordTx(tx *gorm.DB, event AuditEvent) error {
	row, prepared, err := s.prepare(event)
	if err != nil {
		return err
	}
	// The log line is emitted after the caller's transaction commits (see
	// LogOnly) for mutations; here only the row is written, so a rolled
	// back transaction leaves no misleading log line behind.
	if tableSkipped(prepared.Action) {
		return nil
	}
	if err := tx.Create(row).Error; err != nil {
		return fmt.Errorf("failed to record the %s audit event: %w", prepared.Action, err)
	}
	return nil
}

// Record appends event outside any transaction and emits the log line. Used
// for events with no state change to ride along with: view events, login,
// and anything recorded after its own commit.
//
// A failed insert must not fail the operation it describes — a read that
// errors because its audit row could not be written is worse than a missing
// row — so the error is logged loudly and swallowed. The log line is still
// emitted, and the log is the archive.
func (s *AuditService) Record(ctx context.Context, event AuditEvent) {
	row, prepared, err := s.prepare(event)
	if err != nil {
		slog.Error("failed to prepare an audit event", "action", event.Action, "error", err)
		return
	}
	s.emit(prepared)

	if tableSkipped(prepared.Action) {
		return
	}
	if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
		slog.Error("failed to record an audit event to the database; it is still in the shipped audit log",
			"action", prepared.Action, "error", err)
	}
}

// LogOnly emits the shipped-log line for an event whose row was already
// written by RecordTx. Called after that transaction commits, best-effort:
// the row is the durable part of a mutation, the line is the archive copy.
func (s *AuditService) LogOnly(event AuditEvent) {
	_, prepared, err := s.prepare(event)
	if err != nil {
		slog.Error("failed to prepare an audit event for the shipped log", "action", event.Action, "error", err)
		return
	}
	s.emit(prepared)
}

// prepare fills in the recorder-owned fields, validates the reason, and
// encodes the payload, returning both the row and the completed event.
func (s *AuditService) prepare(event AuditEvent) (*model.AuditEvent, AuditEvent, error) {
	if event.Action == "" {
		return nil, event, fmt.Errorf("an audit event needs an action")
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now()
	}
	event.V = auditPayloadVersion

	reason, err := ValidateAuditReason(event.Action, event.Reason)
	if err != nil {
		return nil, event, err
	}
	event.Reason = reason

	payload, err := json.Marshal(event)
	if err != nil {
		// not covered: AuditEvent is plain strings, slices and a map of
		// values this package controls, so json.Marshal cannot fail on it.
		return nil, event, fmt.Errorf("failed to encode the audit payload: %w", err)
	}

	row := &model.AuditEvent{
		ID:           uuid.NewString(),
		CreatedAt:    event.OccurredAt,
		ActorUserID:  subjectUserID(event.Actor),
		TargetUserID: subjectUserID(event.Target),
		Payload:      string(payload),
	}
	return row, event, nil
}

// emit writes the one JSON line per event that an external log system
// ships and archives.
func (s *AuditService) emit(event AuditEvent) {
	attrs := []any{
		slog.String("action", string(event.Action)),
		slog.Int("v", event.V),
		slog.Time("occurred_at", event.OccurredAt),
	}
	if event.System {
		attrs = append(attrs, slog.Bool("system", true))
	}
	if event.Actor != nil {
		attrs = append(attrs, slog.Any("actor", event.Actor))
	}
	if event.Target != nil {
		attrs = append(attrs, slog.Any("target", event.Target))
	}
	if event.Reason != "" {
		attrs = append(attrs, slog.String("reason", event.Reason))
	}
	for k, v := range event.Detail {
		attrs = append(attrs, slog.Any(k, v))
	}
	s.log.Info("audit", attrs...)
}

// subjectUserID pulls the indexed grouping key out of a snapshot, or nil
// when there is no subject or it has no row.
func subjectUserID(subject *AuditSubject) *string {
	if subject == nil || subject.UserID == "" {
		return nil
	}
	id := subject.UserID
	return &id
}

// AuditSubjectFromIdentity snapshots a session identity as the actor or
// target of an event. userID is the users-row id, which may be empty when
// the row has not been resolved.
func AuditSubjectFromIdentity(identity *Identity, userID string) *AuditSubject {
	if identity == nil {
		return nil
	}
	return &AuditSubject{
		UserID:   userID,
		Subject:  identity.Subject,
		Username: identity.Username,
		Email:    identity.Email,
		Groups:   identity.Groups,
	}
}

// AuditSubjectFromUser snapshots a users row as the target of an event,
// which is the shape most administrative actions have.
func AuditSubjectFromUser(user *model.User) *AuditSubject {
	if user == nil {
		return nil
	}
	return &AuditSubject{
		UserID:   user.ID,
		Subject:  user.Subject,
		Username: user.Username,
		Email:    user.Email,
	}
}

// AuditPage is one page of audit events, decoded for the UI.
type AuditPage struct {
	Events []AuditEventRecord
	// NextOffset is the offset to pass for the following page, or 0 when
	// this is the last one.
	NextOffset int
	Total      int64
}

// AuditEventRecord is a stored event decoded back into its payload, with
// the row's own identifiers alongside.
type AuditEventRecord struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	Event     AuditEvent `json:"event"`
}

// ListForUser returns the events where userID is the actor or the target,
// newest first — the per-user timeline. One row serves both sides: when a
// SOC analyst disables alice, the analyst's history shows the disable and
// alice's page shows who did it and why, from the same event.
func (s *AuditService) ListForUser(ctx context.Context, userID string, limit, offset int) (*AuditPage, error) {
	q := s.db.WithContext(ctx).Model(&model.AuditEvent{}).
		Where("actor_user_id = ? OR target_user_id = ?", userID, userID)
	return s.page(ctx, q, limit, offset)
}

// ListRecent returns the whole stream in created_at order, newest first —
// the recent-activity feed. Deliberately unsearchable: the external log
// system is where searching happens.
func (s *AuditService) ListRecent(ctx context.Context, limit, offset int) (*AuditPage, error) {
	return s.page(ctx, s.db.WithContext(ctx).Model(&model.AuditEvent{}), limit, offset)
}

// page runs one paged query and decodes the payloads.
func (s *AuditService) page(ctx context.Context, q *gorm.DB, limit, offset int) (*AuditPage, error) {
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("failed to count audit events: %w", err)
	}

	var rows []model.AuditEvent
	if err := q.Session(&gorm.Session{}).
		Order("created_at DESC, id DESC").
		Limit(limit).Offset(offset).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list audit events: %w", err)
	}

	page := &AuditPage{Events: make([]AuditEventRecord, 0, len(rows)), Total: total}
	for _, row := range rows {
		record := AuditEventRecord{ID: row.ID, CreatedAt: row.CreatedAt}
		if err := json.Unmarshal([]byte(row.Payload), &record.Event); err != nil {
			// A corrupt payload must not blank the whole page: the row's
			// existence and timestamp are themselves audit evidence, so it
			// is surfaced with an action naming the problem.
			slog.Error("failed to decode a stored audit payload", "audit_event_id", row.ID, "error", err)
			record.Event = AuditEvent{Action: "audit.unreadable", OccurredAt: row.CreatedAt}
		}
		page.Events = append(page.Events, record)
	}
	if offset+len(rows) < int(total) {
		page.NextOffset = offset + len(rows)
	}
	return page, nil
}

// SweepAuditEvents prunes the database copy: rows older than the retention
// window, then anything past the row cap, oldest first. The shipped log is
// the archive, so this loses nothing a deployment should be relying on.
//
// A package-level function rather than a method, so the scheduler can
// register it without reaching through a service.
func SweepAuditEvents(ctx context.Context, db *gorm.DB, retention time.Duration, maxRows int64) error {
	if retention > 0 {
		cutoff := time.Now().Add(-retention)
		result := db.WithContext(ctx).Where("created_at < ?", cutoff).Delete(&model.AuditEvent{})
		if result.Error != nil {
			return fmt.Errorf("failed to prune audit events past the retention window: %w", result.Error)
		}
		if result.RowsAffected > 0 {
			slog.Info("pruned audit events past the retention window",
				"deleted", result.RowsAffected, "retention", retention)
		}
	}

	if maxRows <= 0 {
		return nil
	}

	var total int64
	if err := db.WithContext(ctx).Model(&model.AuditEvent{}).Count(&total).Error; err != nil {
		return fmt.Errorf("failed to count audit events: %w", err)
	}
	if total <= maxRows {
		return nil
	}

	// Delete by id from a bounded subquery rather than with a LIMIT on the
	// delete itself, which sqlite and postgres do not share syntax for.
	excess := int(total - maxRows)
	var ids []string
	if err := db.WithContext(ctx).Model(&model.AuditEvent{}).
		Order("created_at ASC, id ASC").
		Limit(excess).
		Pluck("id", &ids).Error; err != nil {
		return fmt.Errorf("failed to select the oldest audit events past the cap: %w", err)
	}
	if len(ids) == 0 {
		// not covered: total > maxRows >= 0 means at least one row exists
		// to select, so this cannot be reached from the count above.
		return nil
	}
	if err := db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.AuditEvent{}).Error; err != nil {
		return fmt.Errorf("failed to prune audit events past the row cap: %w", err)
	}
	slog.Info("pruned audit events past the row cap", "deleted", len(ids), "max_rows", maxRows)
	return nil
}
