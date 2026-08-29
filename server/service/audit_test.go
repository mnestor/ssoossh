package service

// Test methodology: table-driven unit tests over an in-memory sqlite
// database. The two properties worth pinning are the ones the audit log
// exists for: a mutation and its audit row commit together, and a required
// reason cannot be omitted.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// newTestAuditService builds a recorder over a migrated in-memory database.
func newTestAuditService(t *testing.T) (*AuditService, *gorm.DB) {
	t.Helper()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.AuditEvent{}, &model.User{}); err != nil {
		t.Fatalf("failed to migrate the audit test database: %v", err)
	}
	return NewAuditService(&config.Config{}, db), db
}

// decodePayload reads one stored row back into the event it came from.
func decodePayload(t *testing.T, row model.AuditEvent) AuditEvent {
	t.Helper()

	var event AuditEvent
	if err := json.Unmarshal([]byte(row.Payload), &event); err != nil {
		t.Fatalf("failed to decode the stored payload %q: %v", row.Payload, err)
	}
	return event
}

func TestValidateAuditReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		action  AuditAction
		reason  string
		want    string
		wantErr bool
	}{
		{name: "required action with a reason", action: AuditUserDisabled, reason: "offboarded", want: "offboarded"},
		{name: "required action trims surrounding space", action: AuditUserDisabled, reason: "  offboarded  ", want: "offboarded"},
		{name: "required action with no reason", action: AuditUserDisabled, reason: "", wantErr: true},
		{name: "required action with only whitespace", action: AuditUserDisabled, reason: "   \t\n ", wantErr: true},
		{name: "required action with an overlong reason", action: AuditUserDisabled, reason: strings.Repeat("x", maxAuditReasonLength+1), wantErr: true},
		{name: "required action at exactly the cap", action: AuditUserEnabled, reason: strings.Repeat("x", maxAuditReasonLength), want: strings.Repeat("x", maxAuditReasonLength)},
		{name: "enrollment expiry requires one", action: AuditEnrollmentExpired, reason: "", wantErr: true},
		{name: "an unrequired action accepts nothing", action: AuditAuthLogin, reason: "", want: ""},
		{name: "an unrequired action keeps what it is given", action: AuditCertApproved, reason: "because", want: "because"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ValidateAuditReason(tt.action, tt.reason)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAuditReason() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("ValidateAuditReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

// The row and the state change must commit together. This is the one
// failure mode an audit log exists to prevent, so it is checked from both
// directions: a rolled back transaction leaves no row, and a committed one
// always has it.
func TestRecordTx_ShouldCommitAndRollBackWithTheEnclosingTransaction(t *testing.T) {
	t.Parallel()

	audit, db := newTestAuditService(t)

	event := AuditEvent{
		Action: AuditUserDisabled,
		Target: &AuditSubject{UserID: "u-target", Username: "target"},
		Reason: "offboarded",
	}

	// A transaction that fails after the append must leave nothing behind.
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := audit.RecordTx(tx, event); err != nil {
			return err
		}
		return context.Canceled // any failure after the append
	})
	if err == nil {
		t.Fatal("expected the transaction to fail")
	}

	var count int64
	if err := db.Model(&model.AuditEvent{}).Count(&count).Error; err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if count != 0 {
		t.Errorf("a rolled back transaction left %d audit rows behind", count)
	}

	// And a transaction that commits must have it.
	if err := db.Transaction(func(tx *gorm.DB) error {
		return audit.RecordTx(tx, event)
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var rows []model.AuditEvent
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load audit events: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d audit rows after a commit, want 1", len(rows))
	}
	if rows[0].TargetUserID == nil || *rows[0].TargetUserID != "u-target" {
		t.Errorf("target_user_id = %v, want the grouping key to be set", rows[0].TargetUserID)
	}
}

// A missing required reason must stop the write rather than produce a row
// that says nothing.
func TestRecordTx_ShouldRefuseAnActionMissingItsRequiredReason(t *testing.T) {
	t.Parallel()

	audit, db := newTestAuditService(t)

	err := db.Transaction(func(tx *gorm.DB) error {
		return audit.RecordTx(tx, AuditEvent{
			Action: AuditUserDisabled,
			Target: &AuditSubject{UserID: "u-target"},
		})
	})
	if err == nil {
		t.Fatal("expected a disable with no reason to be refused")
	}

	var count int64
	if err := db.Model(&model.AuditEvent{}).Count(&count).Error; err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if count != 0 {
		t.Errorf("a refused event still wrote %d rows", count)
	}
}

// Every payload carries its schema version from day one, so a future shape
// change never has to guess what it is reading.
func TestRecord_ShouldStampTheSchemaVersionAndTime(t *testing.T) {
	t.Parallel()

	audit, db := newTestAuditService(t)
	audit.Record(context.Background(), AuditEvent{Action: AuditAuthLogin, Actor: &AuditSubject{UserID: "u-alice"}})

	var rows []model.AuditEvent
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load audit events: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}

	event := decodePayload(t, rows[0])
	if event.V != auditPayloadVersion {
		t.Errorf("payload version = %d, want %d", event.V, auditPayloadVersion)
	}
	if event.OccurredAt.IsZero() {
		t.Error("occurred_at was left zero")
	}
	if rows[0].CreatedAt.IsZero() {
		t.Error("created_at was left zero")
	}
}

// cert.issued is emitted to the shipped log only: the UI already has
// certificate history from the certificates table, so a table copy would be
// pure duplication.
func TestRecord_ShouldKeepCertIssuedOutOfTheTable(t *testing.T) {
	t.Parallel()

	audit, db := newTestAuditService(t)
	audit.Record(context.Background(), AuditEvent{
		Action: AuditCertIssued,
		Target: &AuditSubject{UserID: "u-alice"},
		Detail: map[string]any{"serial": uint64(42)},
	})
	audit.Record(context.Background(), AuditEvent{Action: AuditCertApproved, Actor: &AuditSubject{UserID: "u-alice"}})

	var rows []model.AuditEvent
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load audit events: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want only the cert.approved one", len(rows))
	}
	if decodePayload(t, rows[0]).Action != AuditCertApproved {
		t.Errorf("stored action = %q, want cert.approved", decodePayload(t, rows[0]).Action)
	}
}

// One row serves both timelines: when an analyst disables alice, the
// analyst's history shows the disable and alice's page shows who did it.
func TestListForUser_ShouldReturnEventsAsActorAndAsTarget(t *testing.T) {
	t.Parallel()

	audit, _ := newTestAuditService(t)
	ctx := context.Background()

	audit.Record(ctx, AuditEvent{
		Action: AuditUserDisabled,
		Actor:  &AuditSubject{UserID: "u-analyst", Username: "soc-bob"},
		Target: &AuditSubject{UserID: "u-alice", Username: "alice"},
		Reason: "incident 42",
	})
	audit.Record(ctx, AuditEvent{Action: AuditAuthLogin, Actor: &AuditSubject{UserID: "u-carol"}})

	for _, userID := range []string{"u-analyst", "u-alice"} {
		page, err := audit.ListForUser(ctx, userID, 25, 0)
		if err != nil {
			t.Fatalf("ListForUser(%q) error = %v", userID, err)
		}
		if len(page.Events) != 1 {
			t.Fatalf("ListForUser(%q) returned %d events, want the one disable", userID, len(page.Events))
		}
		if page.Events[0].Event.Reason != "incident 42" {
			t.Errorf("ListForUser(%q) lost the reason: %+v", userID, page.Events[0].Event)
		}
	}

	// And an unrelated user's timeline stays empty.
	page, err := audit.ListForUser(ctx, "u-nobody", 25, 0)
	if err != nil {
		t.Fatalf("ListForUser() error = %v", err)
	}
	if len(page.Events) != 0 {
		t.Errorf("an uninvolved user's timeline returned %d events", len(page.Events))
	}
}

func TestListRecent_ShouldOrderNewestFirstAndPage(t *testing.T) {
	t.Parallel()

	audit, _ := newTestAuditService(t)
	ctx := context.Background()

	base := time.Now().Add(-time.Hour)
	for i := range 5 {
		audit.Record(ctx, AuditEvent{
			Action:     AuditAuthLogin,
			Actor:      &AuditSubject{UserID: "u-alice"},
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
			Detail:     map[string]any{"n": i},
		})
	}

	page, err := audit.ListRecent(ctx, 2, 0)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if page.Total != 5 {
		t.Errorf("total = %d, want 5", page.Total)
	}
	if len(page.Events) != 2 {
		t.Fatalf("got %d events, want the page size of 2", len(page.Events))
	}
	if page.NextOffset != 2 {
		t.Errorf("next offset = %d, want 2", page.NextOffset)
	}
	// Newest first: the last one recorded carries n=4.
	if got := page.Events[0].Event.Detail["n"]; got != float64(4) {
		t.Errorf("first event n = %v, want 4 (newest first)", got)
	}

	last, err := audit.ListRecent(ctx, 2, 4)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if last.NextOffset != 0 {
		t.Errorf("next offset on the last page = %d, want 0", last.NextOffset)
	}
}

func TestSweepAuditEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		ages      []time.Duration
		retention time.Duration
		maxRows   int64
		wantLeft  int
	}{
		{
			name:      "prunes past the retention window",
			ages:      []time.Duration{time.Hour, 48 * time.Hour, 72 * time.Hour},
			retention: 24 * time.Hour,
			wantLeft:  1,
		},
		{
			name:      "zero retention disables age pruning",
			ages:      []time.Duration{time.Hour, 48 * time.Hour, 72 * time.Hour},
			retention: 0,
			wantLeft:  3,
		},
		{
			name:     "row cap deletes oldest first",
			ages:     []time.Duration{time.Hour, 2 * time.Hour, 3 * time.Hour, 4 * time.Hour},
			maxRows:  2,
			wantLeft: 2,
		},
		{
			name:     "under the cap deletes nothing",
			ages:     []time.Duration{time.Hour, 2 * time.Hour},
			maxRows:  10,
			wantLeft: 2,
		},
		{
			name:      "both bounds apply together",
			ages:      []time.Duration{time.Hour, 2 * time.Hour, 48 * time.Hour},
			retention: 24 * time.Hour,
			maxRows:   1,
			wantLeft:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			audit, db := newTestAuditService(t)
			now := time.Now()
			for i, age := range tt.ages {
				audit.Record(context.Background(), AuditEvent{
					Action:     AuditAuthLogin,
					Actor:      &AuditSubject{UserID: "u-alice"},
					OccurredAt: now.Add(-age),
					Detail:     map[string]any{"n": i},
				})
			}

			if err := SweepAuditEvents(context.Background(), db, tt.retention, tt.maxRows); err != nil {
				t.Fatalf("SweepAuditEvents() error = %v", err)
			}

			var count int64
			if err := db.Model(&model.AuditEvent{}).Count(&count).Error; err != nil {
				t.Fatalf("count audit events: %v", err)
			}
			if int(count) != tt.wantLeft {
				t.Errorf("got %d rows after the sweep, want %d", count, tt.wantLeft)
			}
		})
	}
}

// The cap must delete the oldest rows, not an arbitrary set: the table is
// "most recent events", so pruning the newest would defeat it entirely.
func TestSweepAuditEvents_ShouldKeepTheNewestPastTheCap(t *testing.T) {
	t.Parallel()

	audit, db := newTestAuditService(t)
	now := time.Now()
	for i := range 4 {
		audit.Record(context.Background(), AuditEvent{
			Action:     AuditAuthLogin,
			Actor:      &AuditSubject{UserID: "u-alice"},
			OccurredAt: now.Add(-time.Duration(4-i) * time.Hour),
			Detail:     map[string]any{"n": i},
		})
	}

	if err := SweepAuditEvents(context.Background(), db, 0, 2); err != nil {
		t.Fatalf("SweepAuditEvents() error = %v", err)
	}

	var rows []model.AuditEvent
	if err := db.Order("created_at ASC").Find(&rows).Error; err != nil {
		t.Fatalf("load audit events: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	// n=2 and n=3 are the two newest.
	for i, row := range rows {
		if got := decodePayload(t, row).Detail["n"]; got != float64(i+2) {
			t.Errorf("row %d has n=%v, want the newest events to survive", i, got)
		}
	}
}
