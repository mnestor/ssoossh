package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/logging"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/utils/paging"
)

// auditPayloadVersion is the payload schema version, carried on every
// event from day one so a future shape change never has to guess what it
// is reading.
const auditPayloadVersion = 1

// maxAuditReasonLength caps a required reason. Long enough for a sentence
// and a ticket reference, short enough that the column cannot be used as
// free storage.
const maxAuditReasonLength = 1000

// auditActions is every action in the taxonomy, in declaration order. It is
// what the feed's action filter checks a caller's value against. It lives
// here rather than in audit_actions.go because tygo generates the frontend's
// copy of that file and would emit this too.
// TestAuditActions_ShouldListEveryTaxonomyAction parses audit_actions.go and
// fails when the two differ, so a new action cannot be left unfilterable.
var auditActions = []AuditAction{
	AuditAuthLogin,
	AuditAuthLoginDenied,
	AuditCertRequested,
	AuditCertApproved,
	AuditCertDenied,
	AuditCertCodeResolved,
	AuditCertIssued,
	AuditCertClaimed,
	AuditCertExpired,
	AuditCertSignFailed,
	AuditCertRequestRefusedNetwork,
	AuditEnrollmentCodeCreated,
	AuditEnrollmentRedeemed,
	AuditEnrollmentExpired,
	AuditEnrollmentNotificationEmailSet,
	AuditEnrollmentReassigned,
	AuditUserDisabled,
	AuditUserEnabled,
	AuditUserAutoDisabled,
	AuditUserDisableRefused,
	AuditAdminUserViewed,
	AuditAdminEnrollmentViewed,
	AuditAdminConfigViewed,
	AuditAdminAuditViewed,
	AuditLDAPSyncTriggered,
	AuditLDAPProbed,
	AuditAdminDiagnosticsRun,
	AuditNSSUIDCollision,
	AuditNSSGIDCollision,
	AuditNSSNameCollision,
	AuditNSSIDChangePending,
	AuditNSSNameMismatch,
	AuditNSSIDChangeAccepted,
	AuditNSSTombstonePurged,
	AuditNSSGroupRenamed,
	AuditNSSLookupForbidden,
	AuditNSSKeyRevokedAttempt,
	AuditNSSKeyExpiredAttempt,
	AuditNSSKeySourceAlert,
	AuditNSSMissRateAlert,
	AuditNSSPrivilegedNameLookup,
	AuditNSSKeyCreated,
	AuditNSSKeyUpdated,
	AuditNSSKeyRotated,
	AuditNSSKeyPreviousCleared,
	AuditNSSKeyRevoked,
	AuditNSSShellChanged,
}

// IsAuditAction reports whether action is in the taxonomy, including the
// actions that are no longer emitted (older rows still carry them) and the
// shipped-log-only ones (a filter on those is valid and finds no rows).
func IsAuditAction(action string) bool {
	return slices.Contains(auditActions, AuditAction(action))
}

// tableSkipped reports whether an action is emitted to the shipped log
// only. Keeping this a function of the action (rather than a decision at
// each call site) is what stops the two sinks drifting apart.
//
// The two privileged-view actions are here for the reason
// admin.config_viewed stopped being emitted at all: an auditor working
// through the directory generates one per user opened and one per page of
// the feed, and within a session they outnumber the decisions the log
// exists to record. The difference is that these still matter to whoever
// reads the archive — "who looked at this account" is a real question — so
// they keep their shipped-log line and lose only the table copy the UI
// renders. admin.audit_viewed also wrote a new row on every read of the
// feed, which shifted the window under "load more" while the feed paged by
// offset.
func tableSkipped(action AuditAction) bool {
	switch action {
	case AuditCertIssued, AuditAdminAuditViewed, AuditAdminUserViewed:
		return true
	default:
		return false
	}
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
	//
	// The key "source_ip" is reserved for an address the server observed
	// (gin's ClientIP, which honours forwarded headers only from
	// http.trusted_proxies), because the recorder copies it into the
	// indexed source_ip column the admin feed filters on; see
	// auditSourceIP. Never put a client-supplied address under it, and
	// never a filter value: admin.audit_viewed records its filters as
	// filter_source_ip.
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
		// Action and SourceIP are encoded into payload just above from
		// this same event, which is what keeps the indexed copies equal to
		// the record.
		Action:   string(event.Action),
		SourceIP: auditSourceIP(event.Detail),
		Payload:  string(payload),
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

// auditSourceIP is the value of the indexed source_ip column for an event:
// its detail.source_ip in canonical form, or "" when it has none.
//
// The rule is one key for every action, so the column means the same thing
// on every row: the address the server observed for the client the event is
// about. Per action that is:
//   - cert.requested, cert.claimed, cert.approved, cert.denied,
//     cert.expired, cert.sign_failed and enrollment.code_created: the
//     address the certificate request came from (req.SourceIP, stored from
//     ClientIP when the request was created). Following one address
//     through these reads one client's requests end to end.
//   - cert.request_refused_network: the refused caller.
//   - enrollment.redeemed: the client that retrieved the certificate.
//   - user.disable_refused: the caller's browser.
//   - nss.lookup_forbidden, nss.key_revoked_attempt and
//     nss.key_expired_attempt: the host that made the lookup, as ClientIP
//     sees it through http.trusted_proxies.
//   - nss.shell_changed: the caller's browser.
//   - every other action: none, so "".
//
// approver_ip on cert.approved and cert.denied is deliberately not used: it
// is the approving browser, a different party, and mixing the two in one
// column would make a filter on an address answer two questions at once.
// remote_host (the host context) is what the client reported, never an
// observation, so it is never used either.
func auditSourceIP(detail map[string]any) string {
	raw, ok := detail["source_ip"].(string)
	if !ok {
		return ""
	}
	canonical, ok := canonicalSourceIP(raw)
	if !ok {
		return ""
	}
	return canonical
}

// canonicalSourceIP parses an address and returns the one spelling the
// column stores and the filter compares: netip's form, with an IPv4-mapped
// IPv6 address unmapped to plain IPv4, since both name the same client. ok is
// false for anything that is not an address, including "".
func canonicalSourceIP(raw string) (string, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	return addr.Unmap().String(), true
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
	// NextCursor is the after to pass for the following page: the id of
	// this page's last event, or nil when this is the last page.
	NextCursor *string
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
//
// after is the id of the previous page's last event, or "" for the first
// page; it must be an event on this user's timeline.
func (s *AuditService) ListForUser(ctx context.Context, userID, after string, limit int) (*AuditPage, error) {
	q := s.db.WithContext(ctx).Model(&model.AuditEvent{}).
		Where("(actor_user_id = ? OR target_user_id = ?)", userID, userID)
	return s.page(ctx, q, q, after, limit)
}

// AuditFilter narrows the recent-activity feed. Each field is an equality
// match on an indexed column, and the zero value matches everything.
type AuditFilter struct {
	// Action keeps only the rows of this action.
	Action AuditAction
	// SourceIP keeps only the rows whose observed source is this address,
	// in canonical form (see canonicalSourceIP).
	SourceIP string
}

// ParseAuditFilter validates the feed's action and source_ip parameters as
// a caller sent them. An empty value is no filter. An action outside the
// taxonomy, or a source that is not an IP address, is an
// InvalidRequestError (400): a filter the server silently dropped would show
// the whole feed under a heading that says it is narrowed. The address is
// canonicalized, so "::ffff:192.0.2.1" finds the rows stored as 192.0.2.1.
func ParseAuditFilter(action, sourceIP string) (AuditFilter, error) {
	var filter AuditFilter
	if action = strings.TrimSpace(action); action != "" {
		if !IsAuditAction(action) {
			return AuditFilter{}, &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("action %q is not an audit action", action)}
		}
		filter.Action = AuditAction(action)
	}
	if sourceIP = strings.TrimSpace(sourceIP); sourceIP != "" {
		canonical, ok := canonicalSourceIP(sourceIP)
		if !ok {
			return AuditFilter{}, &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("source_ip %q is not an IP address", sourceIP)}
		}
		filter.SourceIP = canonical
	}
	return filter, nil
}

// ListRecent returns the stream in created_at order, newest first: the
// recent-activity feed, optionally narrowed by filter.
//
// The feed filters by action and by observed source address, and nothing
// else. Both are equality matches on indexed columns, ordered as the
// indexes are (created_at, id), so a filtered page reads
// idx_audit_events_action, idx_audit_events_source_ip or
// idx_audit_events_action_source_ip rather than scanning payloads. Anything
// else (a free-text search, a time range, a field inside the detail) is
// still the shipped log's job: the table is a bounded cache, not the
// archive.
//
// after is the id of the previous page's last event, or "" for the first
// page. It must be an event, but need not match the filter: only its
// position is read.
func (s *AuditService) ListRecent(ctx context.Context, filter AuditFilter, after string, limit int) (*AuditPage, error) {
	scope := s.db.WithContext(ctx).Model(&model.AuditEvent{})
	q := scope.Session(&gorm.Session{})
	if filter.Action != "" {
		q = q.Where("action = ?", string(filter.Action))
	}
	if filter.SourceIP != "" {
		q = q.Where("source_ip = ?", filter.SourceIP)
	}
	return s.page(ctx, scope, q, after, limit)
}

// page runs one keyset-paged query, newest first, and decodes the payloads.
//
// The cursor is looked up within scope, the list before any filter, so an
// id from outside it (another user's timeline, say) is refused rather than
// used as a position. A cursor whose row the retention sweep has since
// removed is refused the same way: there is no position left to resume
// from.
func (s *AuditService) page(ctx context.Context, scope, q *gorm.DB, after string, limit int) (*AuditPage, error) {
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, fmt.Errorf("failed to count audit events: %w", err)
	}

	window := q.Session(&gorm.Session{})
	if after != "" {
		var cursor model.AuditEvent
		err := scope.Session(&gorm.Session{}).Select("id", "created_at").Where("id = ?", after).Take(&cursor).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, paging.UnknownCursor()
		}
		if err != nil {
			return nil, fmt.Errorf("failed to look up the audit cursor: %w", err)
		}
		where, args := paging.SeekBefore("created_at", "id", cursor.CreatedAt, cursor.ID)
		window = window.Where(where, args...)
	}

	var rows []model.AuditEvent
	if err := window.
		Order("created_at DESC, id DESC").
		Limit(limit + 1).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list audit events: %w", err)
	}
	rows, next := paging.Trim(rows, limit, func(r model.AuditEvent) string { return r.ID })

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
	page.NextCursor = next
	return page, nil
}

// auditSweepBatch is how many rows one delete in the row-cap sweep names.
// The ids travel as bind parameters, and both backends cap those (sqlite at
// 32766, postgres at 65535), so a large excess, such as a burst of network
// refusals, is deleted in batches rather than as one statement that fails.
const auditSweepBatch = 1000

// SweepAuditEvents prunes the database copy: rows older than the retention
// window, then the oldest rows past the row cap. The shipped log is the
// archive, so this loses nothing a deployment should be relying on.
//
// Past the cap it is oldest first whatever the action, network refusals
// included, so the table always holds one unbroken window of time rather
// than a history with one kind of event cut out early. Anyone on a refused
// network can raise cert.request_refused_network without signing in, so a
// burst of them can push older events out of the UI's history. That is left
// to the deployment: a firewall keeps those networks out, the rate limits
// slow what gets through, and the shipped log keeps every line regardless.
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
	excess := int(total - maxRows)

	deleted, err := deleteOldestAuditEvents(ctx, db, excess)
	if err != nil {
		return fmt.Errorf("failed to prune audit events past the row cap: %w", err)
	}
	slog.Info("pruned audit events past the row cap", "deleted", deleted, "max_rows", maxRows)
	return nil
}

// deleteOldestAuditEvents deletes up to limit of the oldest rows, in batches
// of auditSweepBatch, and returns how many it deleted. It stops early when
// the table runs out of rows.
//
// Each batch selects ids and deletes by id rather than putting a LIMIT on
// the delete itself, which sqlite and postgres do not share syntax for.
func deleteOldestAuditEvents(ctx context.Context, db *gorm.DB, limit int) (int, error) {
	deleted := 0
	for deleted < limit {
		var ids []string
		if err := db.WithContext(ctx).Model(&model.AuditEvent{}).
			Order("created_at ASC, id ASC").
			Limit(min(limit-deleted, auditSweepBatch)).
			Pluck("id", &ids).Error; err != nil {
			return deleted, fmt.Errorf("failed to select the oldest audit events: %w", err)
		}
		if len(ids) == 0 {
			return deleted, nil
		}
		if err := db.WithContext(ctx).Where("id IN ?", ids).Delete(&model.AuditEvent{}).Error; err != nil {
			return deleted, fmt.Errorf("failed to delete audit events: %w", err)
		}
		deleted += len(ids)
	}
	return deleted, nil
}
