package controller

// Test methodology: drive the two auditor-only audit endpoints through the
// same router-with-real-middleware harness the other admin tests use, with
// events seeded through the real AuditService against in-memory SQLite. The
// interesting properties are the authorization boundary, the newest-first
// ordering, the one-row-serves-both-sides user timeline, and the fact that
// looking at the feed is itself audited exactly once per visit.

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// auditorIdentity is a session identity in the auditor group and nothing
// else, matching newTestConfig's ssh-auditors.
func auditorIdentity() *service.Identity {
	return &service.Identity{
		Subject:  "sub-auditor",
		Username: "auditor",
		Email:    "auditor@example.com",
		Groups:   []string{"ssh-auditors"},
	}
}

// seedAuditEvent records one event through the real service so the row and
// payload have exactly the stored shape the handlers decode.
func seedAuditEvent(t *testing.T, db *gorm.DB, event service.AuditEvent) {
	t.Helper()

	audit := service.NewAuditService(newTestConfig(t), db)
	audit.Record(t.Context(), event)
}

// getAuditPage performs one GET against path and decodes the data envelope.
func getAuditPage(t *testing.T, r http.Handler, path string) (int, webtypes.AuditEventsResponse) {
	t.Helper()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

	if w.Code != http.StatusOK {
		return w.Code, webtypes.AuditEventsResponse{}
	}
	var envelope struct {
		Data webtypes.AuditEventsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decoding %s response: %v, body: %s", path, err, w.Body.String())
	}
	return w.Code, envelope.Data
}

func TestAuditFeed_ShouldDenyAnonymous(t *testing.T) {
	t.Parallel()

	r := routerWithAuth(t, newTestConfig(t), newTestDB(t), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/audit", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/audit with no identity: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestAuditFeed_ShouldDenyAPlainUser(t *testing.T) {
	t.Parallel()

	identity := &service.Identity{
		Subject:  "sub-user",
		Username: "user",
		Groups:   []string{"ssh-users"},
	}
	r := routerWithAuth(t, newTestConfig(t), newTestDB(t), identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/audit", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/audit as a plain user: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

func TestAuditFeed_ShouldReturnEventsNewestFirst(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)

	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for i, action := range []service.AuditAction{
		service.AuditAuthLogin,
		service.AuditCertApproved,
		service.AuditCertDenied,
	} {
		seedAuditEvent(t, db, service.AuditEvent{
			Action:     action,
			Actor:      &service.AuditSubject{UserID: "user-1", Username: "alice"},
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	r := routerWithAuth(t, cfg, db, auditorIdentity())
	code, page := getAuditPage(t, r, "/admin/audit")
	if code != http.StatusOK {
		t.Fatalf("GET /admin/audit as auditor: got %d, want %d", code, http.StatusOK)
	}

	if page.Total != 3 {
		t.Errorf("Total = %d, want 3", page.Total)
	}
	if len(page.Events) != 3 {
		t.Fatalf("len(Events) = %d, want 3", len(page.Events))
	}
	// Newest first: the last seeded action leads.
	if got := page.Events[0].Action; got != string(service.AuditCertDenied) {
		t.Errorf("Events[0].Action = %q, want %q", got, service.AuditCertDenied)
	}
	if got := page.Events[2].Action; got != string(service.AuditAuthLogin) {
		t.Errorf("Events[2].Action = %q, want %q", got, service.AuditAuthLogin)
	}
	if actor := page.Events[0].Actor; actor == nil || actor.Username != "alice" {
		t.Errorf("Events[0].Actor = %+v, want the seeded snapshot", actor)
	}
}

// Visiting the feed is still audited to the shipped log, but no longer to
// the table the feed itself renders. A visit that wrote a row left the feed
// consuming its own output: every read added an event ahead of the ones
// being read, which shifted the page window under "load more" and produced
// repeats. See service.tableSkipped.
func TestAuditFeed_ShouldNotRecordTheVisitInTheTable(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	r := routerWithAuth(t, cfg, db, auditorIdentity())

	if code, _ := getAuditPage(t, r, "/admin/audit"); code != http.StatusOK {
		t.Fatalf("GET /admin/audit as auditor: got %d, want %d", code, http.StatusOK)
	}

	var rows []model.AuditEvent
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("listing audit rows: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("audit rows after one visit = %d, want none: the visit is shipped-log only", len(rows))
	}
}

// The feed reads the same rows however many times it is read. This is the
// property "load more" depends on: a page boundary that moves between two
// requests hands the same event out twice, which is the each_key_duplicate
// the UI reported.
func TestAuditFeed_ShouldNotGrowWhenItIsRead(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	seedAuditEvent(t, db, service.AuditEvent{
		Action: service.AuditAuthLogin,
		Actor:  &service.AuditSubject{UserID: "user-0"},
	})
	r := routerWithAuth(t, cfg, db, auditorIdentity())

	var totals []int64
	for range 3 {
		code, page := getAuditPage(t, r, "/admin/audit")
		if code != http.StatusOK {
			t.Fatalf("GET /admin/audit: got %d, want %d", code, http.StatusOK)
		}
		totals = append(totals, page.Total)
	}
	if totals[0] != 1 || totals[1] != 1 || totals[2] != 1 {
		t.Errorf("totals across three reads = %v, want the same one row every time", totals)
	}
}

func TestAuditFeed_ShouldRejectMalformedPaging(t *testing.T) {
	t.Parallel()

	r := routerWithAuth(t, newTestConfig(t), newTestDB(t), auditorIdentity())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/audit?limit=abc", nil))

	if w.Code != http.StatusBadRequest {
		t.Errorf("GET /admin/audit?limit=abc: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUserAudit_ShouldDenyAPlainUser(t *testing.T) {
	t.Parallel()

	identity := &service.Identity{
		Subject:  "sub-user",
		Username: "user",
		Groups:   []string{"ssh-users"},
	}
	r := routerWithAuth(t, newTestConfig(t), newTestDB(t), identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/user-1/audit", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/users/:id/audit as a plain user: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

// One row serves both sides of the timeline: the same event shows on the
// actor's history and the target's page, and unrelated events stay out.
func TestUserAudit_ShouldReturnActorAndTargetRows(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)

	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	seedAuditEvent(t, db, service.AuditEvent{
		Action:     service.AuditAuthLogin,
		Actor:      &service.AuditSubject{UserID: "user-1", Username: "alice"},
		OccurredAt: base,
	})
	seedAuditEvent(t, db, service.AuditEvent{
		Action:     service.AuditUserDisabled,
		Actor:      &service.AuditSubject{UserID: "user-2", Username: "soc"},
		Target:     &service.AuditSubject{UserID: "user-1", Username: "alice"},
		Reason:     "offboarding ticket OPS-1",
		OccurredAt: base.Add(time.Minute),
	})
	seedAuditEvent(t, db, service.AuditEvent{
		Action:     service.AuditAuthLogin,
		Actor:      &service.AuditSubject{UserID: "user-3", Username: "bob"},
		OccurredAt: base.Add(2 * time.Minute),
	})

	r := routerWithAuth(t, cfg, db, auditorIdentity())
	code, page := getAuditPage(t, r, "/admin/users/user-1/audit")
	if code != http.StatusOK {
		t.Fatalf("GET /admin/users/user-1/audit as auditor: got %d, want %d", code, http.StatusOK)
	}

	if page.Total != 2 {
		t.Errorf("Total = %d, want the actor-side and target-side rows only", page.Total)
	}
	if len(page.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2", len(page.Events))
	}
	if got := page.Events[0].Action; got != string(service.AuditUserDisabled) {
		t.Errorf("Events[0].Action = %q, want %q", got, service.AuditUserDisabled)
	}
	if target := page.Events[0].Target; target == nil || target.UserID != "user-1" {
		t.Errorf("Events[0].Target = %+v, want user-1", target)
	}
	if page.Events[0].Reason != "offboarding ticket OPS-1" {
		t.Errorf("Events[0].Reason = %q, want the recorded reason", page.Events[0].Reason)
	}
}

// The visit's shipped-log line records the paging it was asked for: the page
// size, and past the first page the cursor it resumed from, never an offset.
// Not parallel: it swaps the default logger the audit recorder writes to.
func TestAuditFeed_ShouldRecordTheCursorOnTheVisit(t *testing.T) {
	tests := []struct {
		name      string
		after     bool
		wantAfter bool
	}{
		{name: "should record no cursor when the first page is read", after: false, wantAfter: false},
		{name: "should record the cursor when a later page is read", after: true, wantAfter: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			prior := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
			t.Cleanup(func() { slog.SetDefault(prior) })

			db := newTestDB(t)
			seedAuditEvent(t, db, service.AuditEvent{Action: service.AuditAuthLogin, Actor: &service.AuditSubject{UserID: "user-1"}})
			var row model.AuditEvent
			if err := db.First(&row).Error; err != nil {
				t.Fatalf("read seeded row: %v", err)
			}
			r := routerWithAuth(t, newTestConfig(t), db, auditorIdentity())

			path := "/admin/audit?limit=5"
			if tt.after {
				path += "&after=" + row.ID
			}
			if code, _ := getAuditPage(t, r, path); code != http.StatusOK {
				t.Fatalf("GET %s: got %d, want %d", path, code, http.StatusOK)
			}

			var visit map[string]any
			for line := range bytes.SplitSeq(buf.Bytes(), []byte("\n")) {
				var entry map[string]any
				if json.Unmarshal(line, &entry) == nil && entry["action"] == string(service.AuditAdminAuditViewed) {
					visit = entry
				}
			}
			if visit == nil {
				t.Fatalf("no %s line in the log: %s", service.AuditAdminAuditViewed, buf.String())
			}
			if _, ok := visit["offset"]; ok {
				t.Errorf("visit line carries an offset: %v", visit)
			}
			if visit["limit"] != float64(5) {
				t.Errorf("visit limit = %v, want 5", visit["limit"])
			}
			gotAfter, hasAfter := visit["after"]
			if hasAfter != tt.wantAfter || (tt.wantAfter && gotAfter != row.ID) {
				t.Errorf("visit after = %v (present %v), want present %v with %q", gotAfter, hasAfter, tt.wantAfter, row.ID)
			}
		})
	}
}
