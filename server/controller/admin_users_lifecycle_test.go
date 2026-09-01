package controller

// Test methodology: the error and audit branches of the user
// disable/enable lifecycle, through the same real-middleware router the
// other admin tests use. The properties pinned here are the audit
// contract — a containment change and its audit row commit together, and
// the restore records why the account had been disabled — and the
// error shapes a UI depends on (404 for an unknown user, 400 for a body
// that does not parse).

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// seedDisabledUser creates a user already disabled with a recorded reason.
func seedDisabledUser(t *testing.T, db *gorm.DB, id string) {
	t.Helper()

	now := time.Now()
	user := model.User{
		ID:             id,
		Subject:        "sub-" + id,
		Username:       id,
		Email:          id + "@example.com",
		DisabledAt:     &now,
		DisabledReason: "prior incident INC-7",
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seeding disabled user: %v", err)
	}
}

func TestEnableUserHandler_ShouldRejectAnEnableWithNoReason(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	seedDisabledUser(t, db, "user-1")
	r := routerWithAuth(t, newTestConfig(t), db, adminIdentity("ssh-admins"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/enable",
		strings.NewReader(`{"reason":""}`)))

	if w.Code != http.StatusBadRequest {
		t.Errorf("enable with no reason: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestEnableUserHandler_ShouldRejectAMalformedBody(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	seedDisabledUser(t, db, "user-1")
	r := routerWithAuth(t, newTestConfig(t), db, adminIdentity("ssh-admins"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/enable",
		strings.NewReader(`{`)))

	if w.Code != http.StatusBadRequest {
		t.Errorf("enable with a malformed body: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestEnableUserHandler_ShouldReportAnUnknownUser(t *testing.T) {
	t.Parallel()

	r := routerWithAuth(t, newTestConfig(t), newTestDB(t), adminIdentity("ssh-admins"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/nope/enable",
		strings.NewReader(`{"reason":"cleared with security SEC-1"}`)))

	if w.Code != http.StatusNotFound {
		t.Errorf("enable of an unknown user: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

// The restore clears the disable, and its audit event carries both the new
// reason and why the account had been disabled — the next admin's context.
func TestEnableUserHandler_ShouldClearTheDisableAndAuditIt(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	seedDisabledUser(t, db, "user-1")
	r := routerWithAuth(t, newTestConfig(t), db, adminIdentity("ssh-admins"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/enable",
		strings.NewReader(`{"reason":"cleared with security SEC-1"}`)))

	if w.Code != http.StatusOK {
		t.Fatalf("enable: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var user model.User
	if err := db.First(&user, "id = ?", "user-1").Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.DisabledAt != nil {
		t.Error("the user is still disabled after the enable")
	}
	if user.DisabledReason != "" {
		t.Errorf("disabled_reason = %q, want it cleared", user.DisabledReason)
	}

	var rows []model.AuditEvent
	if err := db.Where("target_user_id = ?", "user-1").Find(&rows).Error; err != nil {
		t.Fatalf("load audit rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want the enable recorded once", len(rows))
	}
	var event service.AuditEvent
	if err := json.Unmarshal([]byte(rows[0].Payload), &event); err != nil {
		t.Fatalf("decode audit payload: %v", err)
	}
	if event.Action != service.AuditUserEnabled {
		t.Errorf("audit action = %q, want %q", event.Action, service.AuditUserEnabled)
	}
	if event.Reason != "cleared with security SEC-1" {
		t.Errorf("audit reason = %q, want the operator's reason", event.Reason)
	}
	if got := event.Detail["previous_disable_reason"]; got != "prior incident INC-7" {
		t.Errorf("previous_disable_reason = %v, want the prior reason preserved", got)
	}
}

// seedActingAdmin creates the users row the disable handler resolves the
// acting admin against; without one the request fails outright.
func seedActingAdmin(t *testing.T, db *gorm.DB) {
	t.Helper()

	now := time.Now()
	admin := model.User{
		ID: "user-admin", Subject: "sub-admin", Username: "admin",
		Email: "admin@example.com", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatalf("seeding acting admin: %v", err)
	}
}

func TestDisableUserHandler_ShouldReportAnUnknownUser(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	seedActingAdmin(t, db)
	r := routerWithAuth(t, newTestConfig(t), db, adminIdentity("ssh-admins"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/nope/disable",
		strings.NewReader(`{"reason":"offboarding OPS-1"}`)))

	if w.Code != http.StatusNotFound {
		t.Errorf("disable of an unknown user: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestDisableUserHandler_ShouldRejectAMalformedBody(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	seedActingAdmin(t, db)
	seedDisabledUser(t, db, "user-1")
	r := routerWithAuth(t, newTestConfig(t), db, adminIdentity("ssh-admins"))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable",
		strings.NewReader(`{`)))

	if w.Code != http.StatusBadRequest {
		t.Errorf("disable with a malformed body: got %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetUserHandler_ShouldReportAnUnknownUser(t *testing.T) {
	t.Parallel()

	auditor := &service.Identity{
		Subject:  "sub-auditor",
		Username: "auditor",
		Groups:   []string{"ssh-auditors"},
	}
	r := routerWithAuth(t, newTestConfig(t), newTestDB(t), auditor)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/nope", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("get of an unknown user: got %d, want %d", w.Code, http.StatusNotFound)
	}
}
