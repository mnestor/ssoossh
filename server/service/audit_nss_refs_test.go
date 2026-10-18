package service

// Test methodology: events with NSS refs are recorded through both write
// paths over a real database (testdb, so make test-postgres covers both
// backends), then read back through ListForNSSRow and the sweep. The rows
// the refs name need not exist: a ref has no foreign key to them.

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// accountRef names the account row id on an event.
func accountRef(id string) NSSAuditRef { return NSSAuditRef{Kind: model.NSSAuditRefAccount, RowID: id} }

// refsOf reads the refs of the event with id, sorted by row id.
func refsOf(t *testing.T, db *gorm.DB, eventID string) []NSSAuditRef {
	t.Helper()
	var rows []model.NSSAuditRef
	if err := db.Where("event_id = ?", eventID).Order("kind, row_id").Find(&rows).Error; err != nil {
		t.Fatalf("read refs: %v", err)
	}
	out := make([]NSSAuditRef, 0, len(rows))
	for _, r := range rows {
		out = append(out, NSSAuditRef{Kind: r.Kind, RowID: r.RowID})
	}
	return out
}

// onlyEventID is the id of the one stored event with action.
func onlyEventID(t *testing.T, db *gorm.DB, action AuditAction) string {
	t.Helper()
	var ids []string
	if err := db.Model(&model.AuditEvent{}).Where("action = ?", string(action)).Pluck("id", &ids).Error; err != nil {
		t.Fatalf("read events: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("%s events = %d, want 1", action, len(ids))
	}
	return ids[0]
}

func TestAuditService_ShouldWriteEachDistinctRefWhenAnEventNamesNSSRows(t *testing.T) {
	t.Parallel()

	refs := []NSSAuditRef{
		accountRef("pu-ada"), accountRef("pu-ada"), {Kind: model.NSSAuditRefGroup, RowID: "pg-ops"}, accountRef(""),
	}
	want := []NSSAuditRef{accountRef("pu-ada"), {Kind: model.NSSAuditRefGroup, RowID: "pg-ops"}}
	tests := []struct {
		name   string
		record func(t *testing.T, a *AuditService, db *gorm.DB, event AuditEvent)
	}{
		{
			name: "should write the refs in the transaction when RecordTx records the event",
			record: func(t *testing.T, a *AuditService, db *gorm.DB, event AuditEvent) {
				t.Helper()
				if err := db.Transaction(func(tx *gorm.DB) error { return a.RecordTx(tx, event) }); err != nil {
					t.Fatalf("RecordTx() error = %v", err)
				}
			},
		},
		{
			name: "should write the refs with the row when Record records the event",
			record: func(_ *testing.T, a *AuditService, _ *gorm.DB, event AuditEvent) {
				a.Record(context.Background(), event)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			audit, db := newTestAuditService(t)
			tt.record(t, audit, db, AuditEvent{Action: AuditNSSGroupMembersChanged, System: true, NSSRefs: refs})

			if got := refsOf(t, db, onlyEventID(t, db, AuditNSSGroupMembersChanged)); !slices.Equal(got, want) {
				t.Errorf("refs = %v, want %v", got, want)
			}
		})
	}
}

func TestAuditService_ShouldWriteNoRefsWhenTheActionIsShippedLogOnly(t *testing.T) {
	t.Parallel()

	audit, db := newTestAuditService(t)
	audit.Record(context.Background(), AuditEvent{Action: AuditAdminAuditViewed, NSSRefs: []NSSAuditRef{accountRef("pu-ada")}})

	var n int64
	if err := db.Model(&model.NSSAuditRef{}).Count(&n).Error; err != nil {
		t.Fatalf("count refs: %v", err)
	}
	if n != 0 {
		t.Errorf("refs = %d, want none for an event with no row", n)
	}
}

func TestAuditService_ShouldRollBackTheRefsWhenTheTransactionFails(t *testing.T) {
	t.Parallel()

	audit, db := newTestAuditService(t)
	failed := errors.New("the state change failed")
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := audit.RecordTx(tx, AuditEvent{Action: AuditNSSAccountCreated, System: true, NSSRefs: []NSSAuditRef{accountRef("pu-ada")}}); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("transaction error = %v, want the caller's", err)
	}
	var n int64
	if err := db.Model(&model.NSSAuditRef{}).Count(&n).Error; err != nil {
		t.Fatalf("count refs: %v", err)
	}
	if n != 0 {
		t.Errorf("refs = %d, want none after the rollback", n)
	}
}

// seedNSSRowEvents records one event per age, newest last, each naming
// the account row id, and returns the events' ids in that order.
func seedNSSRowEvents(t *testing.T, audit *AuditService, db *gorm.DB, id string, at ...time.Time) []string {
	t.Helper()
	ids := make([]string, 0, len(at))
	for _, when := range at {
		if err := db.Transaction(func(tx *gorm.DB) error {
			return audit.RecordTx(tx, AuditEvent{
				Action: AuditNSSAccountRenamed, System: true, OccurredAt: when,
				Detail: map[string]any{"row": id}, NSSRefs: []NSSAuditRef{accountRef(id)},
			})
		}); err != nil {
			t.Fatalf("record: %v", err)
		}
		// The one ref of id not yet seen is the event just recorded.
		var ref model.NSSAuditRef
		if err := db.Where("row_id = ? AND event_id NOT IN ?", id, append([]string{""}, ids...)).Take(&ref).Error; err != nil {
			t.Fatalf("read back: %v", err)
		}
		ids = append(ids, ref.EventID)
	}
	return ids
}

func TestAuditService_ListForNSSRow(t *testing.T) {
	t.Parallel()

	audit, db := newTestAuditService(t)
	base := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	// Two of ada's events share an instant, so the id breaks the tie.
	ada := seedNSSRowEvents(t, audit, db, "pu-ada", base, base.Add(time.Minute), base.Add(2*time.Minute), base.Add(2*time.Minute), base.Add(3*time.Minute))
	bob := seedNSSRowEvents(t, audit, db, "pu-bob", base.Add(90*time.Second))

	// wantOrder is ada's events newest first: by time, then id descending.
	wantOrder := []string{ada[4]}
	tied := []string{ada[2], ada[3]}
	slices.Sort(tied)
	wantOrder = append(wantOrder, tied[1], tied[0], ada[1], ada[0])

	t.Run("should page every event newest first when read two at a time", func(t *testing.T) {
		t.Parallel()
		var got []string
		after := ""
		for range 10 {
			page, err := audit.ListForNSSRow(context.Background(), model.NSSAuditRefAccount, "pu-ada", after, 2)
			if err != nil {
				t.Fatalf("ListForNSSRow() error = %v", err)
			}
			if page.Total != 5 {
				t.Errorf("total = %d, want 5", page.Total)
			}
			for _, e := range page.Events {
				got = append(got, e.ID)
			}
			if page.NextCursor == nil {
				break
			}
			after = *page.NextCursor
		}
		if !slices.Equal(got, wantOrder) {
			t.Errorf("events = %v, want %v", got, wantOrder)
		}
	})

	t.Run("should decode the payload when it reads a page", func(t *testing.T) {
		t.Parallel()
		page, err := audit.ListForNSSRow(context.Background(), model.NSSAuditRefAccount, "pu-bob", "", 25)
		if err != nil {
			t.Fatalf("ListForNSSRow() error = %v", err)
		}
		if len(page.Events) != 1 || page.Events[0].ID != bob[0] || page.Events[0].Event.Detail["row"] != "pu-bob" {
			t.Errorf("page = %+v, want bob's one event decoded", page.Events)
		}
	})

	t.Run("should answer an empty page when the row has no events", func(t *testing.T) {
		t.Parallel()
		page, err := audit.ListForNSSRow(context.Background(), model.NSSAuditRefGroup, "pu-ada", "", 25)
		if err != nil || len(page.Events) != 0 || page.Total != 0 || page.NextCursor != nil || page.Events == nil {
			t.Errorf("page = %+v, %v; want an empty page: the kind is part of the key", page, err)
		}
	})

	cursors := []struct {
		name  string
		after string
	}{
		{name: "should refuse the cursor when it is another row's event", after: bob[0]},
		{name: "should refuse the cursor when it names no event", after: "no-such-event"},
	}
	for _, tt := range cursors {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := audit.ListForNSSRow(context.Background(), model.NSSAuditRefAccount, "pu-ada", tt.after, 2)
			var invalid *errorresponses.InvalidRequestError
			if !errors.As(err, &invalid) {
				t.Errorf("err = %v, want an InvalidRequestError", err)
			}
		})
	}
}

func TestSweepAuditEvents_ShouldRemoveTheRefsWhenItRemovesTheirEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		retention time.Duration
		maxRows   int64
	}{
		{name: "should remove the refs of events past the retention window", retention: 24 * time.Hour},
		{name: "should remove the refs of events past the row cap", maxRows: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			audit, db := newTestAuditService(t)
			now := time.Now()
			ids := seedNSSRowEvents(t, audit, db, "pu-ada", now.Add(-72*time.Hour), now.Add(-48*time.Hour), now.Add(-time.Hour))

			if err := SweepAuditEvents(context.Background(), db, tt.retention, tt.maxRows); err != nil {
				t.Fatalf("SweepAuditEvents() error = %v", err)
			}

			var left []string
			if err := db.Model(&model.NSSAuditRef{}).Pluck("event_id", &left).Error; err != nil {
				t.Fatalf("read refs: %v", err)
			}
			if !slices.Equal(left, ids[2:]) {
				t.Errorf("refs left for events %v, want only the newest event's %v", left, ids[2:])
			}
		})
	}
}
