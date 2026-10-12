package service

// Test methodology: table-driven unit tests over an in-memory sqlite
// database. The two properties worth pinning are the ones the audit log
// exists for: a mutation and its audit row commit together, and a required
// reason cannot be omitted.

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service/auditns"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
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

// The log-only actions are emitted to the shipped log and skipped in the
// table copy the UI renders.
//
// cert.issued because the UI already has certificate history from the
// certificates table, so a table copy would be pure duplication. The two
// privileged-view actions because an auditor working through the directory
// generates one per user opened and one per page of the feed, and within a
// session they bury the decisions the log exists to record — admin.audit_viewed
// also wrote a new row on every read of the feed, which shifted the offset
// window under the UI's "load more".
func TestRecord_ShouldKeepTheLogOnlyActionsOutOfTheTable(t *testing.T) {
	t.Parallel()

	logOnly := []AuditAction{AuditCertIssued, AuditAdminAuditViewed, AuditAdminUserViewed}
	for _, action := range logOnly {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			audit, db := newTestAuditService(t)
			audit.Record(context.Background(), AuditEvent{
				Action: action,
				Actor:  &AuditSubject{UserID: "u-auditor"},
				Target: &AuditSubject{UserID: "u-alice"},
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
		})
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
		page, err := audit.ListForUser(ctx, userID, "", 25)
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
	page, err := audit.ListForUser(ctx, "u-nobody", "", 25)
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

	page, err := audit.ListRecent(ctx, AuditFilter{}, "", 2)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if page.Total != 5 {
		t.Errorf("total = %d, want 5", page.Total)
	}
	if len(page.Events) != 2 {
		t.Fatalf("got %d events, want the page size of 2", len(page.Events))
	}
	if page.NextCursor == nil || *page.NextCursor != page.Events[1].ID {
		t.Errorf("next cursor = %v, want the last served event %q", page.NextCursor, page.Events[1].ID)
	}
	// Newest first: the last one recorded carries n=4.
	if got := page.Events[0].Event.Detail["n"]; got != float64(4) {
		t.Errorf("first event n = %v, want 4 (newest first)", got)
	}

	second, err := audit.ListRecent(ctx, AuditFilter{}, *page.NextCursor, 2)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	third, err := audit.ListRecent(ctx, AuditFilter{}, *second.NextCursor, 2)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if third.NextCursor != nil {
		t.Errorf("next cursor on the last page = %q, want nil", *third.NextCursor)
	}
	got := slices.Concat(eventNumbers(page), eventNumbers(second), eventNumbers(third))
	if !slices.Equal(got, []int{4, 3, 2, 1, 0}) {
		t.Errorf("paged events = %v, want every event once, newest first", got)
	}
}

// A row written between two page requests lands above the cursor, so the
// next page neither repeats the cursor's neighbour (offset paging's failure)
// nor skips a row.
func TestListRecent_ShouldNeitherRepeatNorSkipWhenARowArrivesBetweenPages(t *testing.T) {
	t.Parallel()

	audit, _ := newTestAuditService(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	for i := range 4 {
		audit.Record(ctx, AuditEvent{Action: AuditAuthLogin, OccurredAt: base.Add(time.Duration(i) * time.Minute), Detail: map[string]any{"n": i}})
	}

	first, err := audit.ListRecent(ctx, AuditFilter{}, "", 2)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	audit.Record(ctx, AuditEvent{Action: AuditAuthLogin, OccurredAt: base.Add(10 * time.Minute), Detail: map[string]any{"n": 99}})
	second, err := audit.ListRecent(ctx, AuditFilter{}, *first.NextCursor, 2)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}

	got := slices.Concat(eventNumbers(first), eventNumbers(second))
	if !slices.Equal(got, []int{3, 2, 1, 0}) {
		t.Errorf("paged events = %v, want [3 2 1 0]", got)
	}
}

// A cursor must name a row of the list being paged; anything else is a 400
// rather than a silent first page.
func TestAuditList_ShouldRefuseACursorOutsideTheList(t *testing.T) {
	t.Parallel()

	audit, _ := newTestAuditService(t)
	ctx := context.Background()
	audit.Record(ctx, AuditEvent{Action: AuditAuthLogin, Actor: &AuditSubject{UserID: "u-alice"}})
	audit.Record(ctx, AuditEvent{Action: AuditAuthLogin, Actor: &AuditSubject{UserID: "u-bob"}})
	bobs, err := audit.ListForUser(ctx, "u-bob", "", 25)
	if err != nil || len(bobs.Events) != 1 {
		t.Fatalf("ListForUser(u-bob) = %v, %v; want one event", bobs, err)
	}

	tests := []struct {
		name string
		list func() error
	}{
		{name: "should refuse an unknown id on the feed", list: func() error {
			_, err := audit.ListRecent(ctx, AuditFilter{}, "no-such-event", 25)
			return err
		}},
		{name: "should refuse an unknown id on a timeline", list: func() error {
			_, err := audit.ListForUser(ctx, "u-alice", "no-such-event", 25)
			return err
		}},
		{name: "should refuse another user's event on a timeline", list: func() error {
			_, err := audit.ListForUser(ctx, "u-alice", bobs.Events[0].ID, 25)
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var invalid *errorresponses.InvalidRequestError
			if err := tt.list(); !errors.As(err, &invalid) {
				t.Errorf("error = %v, want *errorresponses.InvalidRequestError", err)
			}
		})
	}
}

// One user's timeline pages by cursor across both sides of the OR, so an
// event where the user is the actor and one where they are the target page
// in one order.
func TestListForUser_ShouldPageByCursor(t *testing.T) {
	t.Parallel()

	audit, _ := newTestAuditService(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	for i := range 3 {
		event := AuditEvent{Action: AuditUserDisabled, Reason: "incident 42", OccurredAt: base.Add(time.Duration(i) * time.Minute), Detail: map[string]any{"n": i}}
		if i%2 == 0 {
			event.Actor = &AuditSubject{UserID: "u-alice"}
		} else {
			event.Target = &AuditSubject{UserID: "u-alice"}
		}
		audit.Record(ctx, event)
	}

	first, err := audit.ListForUser(ctx, "u-alice", "", 2)
	if err != nil {
		t.Fatalf("ListForUser() error = %v", err)
	}
	if first.NextCursor == nil {
		t.Fatalf("first page has no next cursor")
	}
	second, err := audit.ListForUser(ctx, "u-alice", *first.NextCursor, 2)
	if err != nil {
		t.Fatalf("ListForUser() error = %v", err)
	}
	if second.NextCursor != nil {
		t.Errorf("next cursor on the last page = %q, want nil", *second.NextCursor)
	}
	got := slices.Concat(eventNumbers(first), eventNumbers(second))
	if !slices.Equal(got, []int{2, 1, 0}) {
		t.Errorf("paged events = %v, want [2 1 0]", got)
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

// declaredStringConsts reads every constant of type typeName declared in the
// Go file at path, in order. Parsing the source is the only way to enumerate
// a const block, and it keeps a test over "every X" from falling behind
// when one is added.
func declaredStringConsts(t *testing.T, path, typeName string) []string {
	t.Helper()

	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}
	var values []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if typ, ok := value.Type.(*ast.Ident); !ok || typ.Name != typeName {
				continue
			}
			for _, v := range value.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("a %s constant is not a string literal: %#v", typeName, v)
				}
				name, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("failed to unquote %s: %v", lit.Value, err)
				}
				values = append(values, name)
			}
		}
	}
	return values
}

// auditTaxonomy reads every AuditAction constant declared in
// audit_actions.go.
func auditTaxonomy(t *testing.T) []AuditAction {
	t.Helper()

	var actions []AuditAction
	for _, name := range declaredStringConsts(t, "audit_actions.go", "AuditAction") {
		actions = append(actions, AuditAction(name))
	}
	// A floor rather than an exact count: it catches a parse that silently
	// found nothing without needing an edit for every new action.
	if len(actions) < 20 {
		t.Fatalf("found only %d AuditAction constants in audit_actions.go: %v", len(actions), actions)
	}
	return actions
}

// auditNamespaces reads every Namespace constant declared in
// auditns/auditns.go.
func auditNamespaces(t *testing.T) []auditns.Namespace {
	t.Helper()

	var namespaces []auditns.Namespace
	for _, name := range declaredStringConsts(t, "auditns/auditns.go", "Namespace") {
		namespaces = append(namespaces, auditns.Namespace(name))
	}
	if len(namespaces) == 0 {
		t.Fatal("found no Namespace constants in auditns/auditns.go")
	}
	return namespaces
}

// The web UI groups its action filter by namespace, from the generated
// auditns constants, so an action under an undeclared namespace would land
// in a group with no label.
func TestAuditTaxonomy_ShouldNameEveryActionUnderADeclaredNamespace(t *testing.T) {
	t.Parallel()

	namespaces := auditNamespaces(t)
	for _, action := range auditTaxonomy(t) {
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()

			namespace, _, found := strings.Cut(string(action), ".")
			if !found || !slices.Contains(namespaces, auditns.Namespace(namespace)) {
				t.Errorf("action %q is not under a namespace declared in auditns: %v", action, namespaces)
			}
		})
	}
}

// A declared namespace with no action would offer an empty group.
func TestAuditTaxonomy_ShouldUseEveryDeclaredNamespace(t *testing.T) {
	t.Parallel()

	actions := auditTaxonomy(t)
	for _, namespace := range auditNamespaces(t) {
		t.Run(string(namespace), func(t *testing.T) {
			t.Parallel()

			used := slices.ContainsFunc(actions, func(action AuditAction) bool {
				return strings.HasPrefix(string(action), string(namespace)+".")
			})
			if !used {
				t.Errorf("namespace %q has no action in audit_actions.go", namespace)
			}
		})
	}
}

// Every stored row carries its payload's action in the indexed column, on
// both write paths, so a query on the column finds exactly what a scan of
// the payloads would. Log-only actions write no row and are left out.
func TestRecord_ShouldSetTheActionColumnToThePayloadAction(t *testing.T) {
	t.Parallel()

	writers := []struct {
		name  string
		write func(t *testing.T, audit *AuditService, db *gorm.DB, event AuditEvent)
	}{
		{
			name: "Record",
			write: func(_ *testing.T, audit *AuditService, _ *gorm.DB, event AuditEvent) {
				audit.Record(context.Background(), event)
			},
		},
		{
			name: "RecordTx",
			write: func(t *testing.T, audit *AuditService, db *gorm.DB, event AuditEvent) {
				if err := db.Transaction(func(tx *gorm.DB) error {
					return audit.RecordTx(tx, event)
				}); err != nil {
					t.Fatalf("RecordTx() error = %v", err)
				}
			},
		},
	}

	for _, action := range auditTaxonomy(t) {
		if tableSkipped(action) {
			continue
		}
		for _, writer := range writers {
			t.Run("should store "+string(action)+" in the column when written by "+writer.name, func(t *testing.T) {
				t.Parallel()

				audit, db := newTestAuditService(t)
				writer.write(t, audit, db, AuditEvent{
					Action: action,
					Actor:  &AuditSubject{UserID: "u-actor"},
					Target: &AuditSubject{UserID: "u-target"},
					Reason: "ticket 42",
				})

				var rows []model.AuditEvent
				if err := db.Find(&rows).Error; err != nil {
					t.Fatalf("load audit events: %v", err)
				}
				if len(rows) != 1 {
					t.Fatalf("got %d rows, want 1", len(rows))
				}
				if got, want := rows[0].Action, string(decodePayload(t, rows[0]).Action); got != want || got != string(action) {
					t.Errorf("action column = %q, payload action = %q, want both %q", got, want, action)
				}
			})
		}
	}
}
