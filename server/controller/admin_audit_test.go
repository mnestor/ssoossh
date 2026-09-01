package controller

// Test methodology: drive the two auditor-only audit endpoints through the
// same router-with-real-middleware harness the other admin tests use, with
// events seeded through the real AuditService against in-memory SQLite. The
// interesting properties are the authorization boundary, the newest-first
// ordering, the one-row-serves-both-sides user timeline, and the fact that
// looking at the feed is itself audited exactly once per visit.

import (
	"encoding/json"
	"fmt"
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

// Visiting the feed is itself audited — one event per visit, not one per
// event displayed, which is what keeps the feed from feeding itself.
func TestAuditFeed_ShouldRecordTheVisitOnce(t *testing.T) {
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
	if len(rows) != 1 {
		t.Fatalf("audit rows after one visit = %d, want exactly the visit event", len(rows))
	}
	var recorded service.AuditEvent
	if err := json.Unmarshal([]byte(rows[0].Payload), &recorded); err != nil {
		t.Fatalf("decoding the visit event: %v", err)
	}
	if recorded.Action != service.AuditAdminAuditViewed {
		t.Errorf("recorded action = %q, want %q", recorded.Action, service.AuditAdminAuditViewed)
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

// The window carries across pages: a second request at the returned offset
// gets the remainder, and the last page reports no next offset.
func TestAuditFeed_ShouldPageWithNextOffset(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)

	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	for i := range 3 {
		seedAuditEvent(t, db, service.AuditEvent{
			Action:     service.AuditAuthLogin,
			Actor:      &service.AuditSubject{UserID: fmt.Sprintf("user-%d", i)},
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	r := routerWithAuth(t, cfg, db, auditorIdentity())

	code, first := getAuditPage(t, r, "/admin/audit?limit=2")
	if code != http.StatusOK {
		t.Fatalf("first page: got %d, want %d", code, http.StatusOK)
	}
	if len(first.Events) != 2 || first.NextOffset != 2 {
		t.Errorf("first page: %d events, NextOffset %d, want 2 events and NextOffset 2", len(first.Events), first.NextOffset)
	}

	// The first visit itself became event four, so the second page holds
	// the remaining seeded event plus that visit, and the window closes.
	code, second := getAuditPage(t, r, "/admin/audit?limit=2&offset=2")
	if code != http.StatusOK {
		t.Fatalf("second page: got %d, want %d", code, http.StatusOK)
	}
	if len(second.Events) != 2 || second.NextOffset != 0 {
		t.Errorf("second page: %d events, NextOffset %d, want 2 events and NextOffset 0", len(second.Events), second.NextOffset)
	}
	if got := second.Events[1].Action; got != string(service.AuditAuthLogin) {
		t.Errorf("second page oldest action = %q, want the first seeded event", got)
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

func TestUserAudit_ShouldRejectMalformedPaging(t *testing.T) {
	t.Parallel()

	r := routerWithAuth(t, newTestConfig(t), newTestDB(t), auditorIdentity())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/user-1/audit?offset=-1", nil))

	if w.Code != http.StatusBadRequest {
		t.Errorf("GET /admin/users/:id/audit?offset=-1: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}
