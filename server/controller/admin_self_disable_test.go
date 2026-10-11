package controller

// Test methodology: drive PATCH /admin/users/:id/disable through the real
// router as an admin or a SOC analyst targeting their own account, and read
// the outcome back from the users row and the audit table. A refused
// self-disable must leave both exactly as they were.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// selfDisableReason is the reason the server returns, and the UI shows, when
// an operator tries to disable their own account.
const selfDisableReason = "You can't disable your own account. Ask another admin."

// selfDisableCase is one operator and the groups they hold.
type selfDisableCase struct {
	name   string
	groups func(cfg *config.Config) []string
}

// TestDisableUserHandler_ShouldRefuseSelfDisable covers the server guard:
// an admin or SOC user who targets their own id is refused with 403 and the
// reason the UI shows, and nothing is written.
func TestDisableUserHandler_ShouldRefuseSelfDisable(t *testing.T) {
	cases := []selfDisableCase{
		{name: "admin", groups: func(cfg *config.Config) []string { return []string{cfg.Admin.RequireGroup} }},
		{name: "SOC", groups: func(cfg *config.Config) []string { return []string{"ssh-soc"} }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			if err := db.Create(&model.User{ID: "u-self", Subject: "sub-self", Username: "self"}).Error; err != nil {
				t.Fatalf("seed operator: %v", err)
			}
			cfg := newTestConfig(t)
			r := routerWithAuth(t, cfg, db, &service.Identity{
				Subject:  "sub-self",
				Username: "self",
				Groups:   tc.groups(cfg),
			})

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-self/disable", `{"reason":"offboarded"}`))

			if w.Code != http.StatusForbidden {
				t.Fatalf("PATCH self disable = %d, want %d, body: %s", w.Code, http.StatusForbidden, w.Body.String())
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if body.Error != selfDisableReason {
				t.Errorf("error = %q, want %q", body.Error, selfDisableReason)
			}
		})
	}
}

// TestDisableUserHandler_ShouldLeaveRowAndAuditUnchangedOnSelfDisable pins
// that the refusal happens before any write: the operator's row is not
// disabled, and no audit event of any kind is added (the caller is both
// actor and target, so the count covers every row, not just one target).
func TestDisableUserHandler_ShouldLeaveRowAndAuditUnchangedOnSelfDisable(t *testing.T) {
	db := newTestDB(t)
	if err := db.Create(&model.User{ID: "u-self", Subject: "sub-self", Username: "self"}).Error; err != nil {
		t.Fatalf("seed operator: %v", err)
	}
	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, &service.Identity{
		Subject:  "sub-self",
		Username: "self",
		Groups:   []string{cfg.Admin.RequireGroup},
	})

	var before int64
	if err := db.Model(&model.AuditEvent{}).Count(&before).Error; err != nil {
		t.Fatalf("count audit before: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-self/disable", `{"reason":"offboarded"}`))

	var stored model.User
	if err := db.First(&stored, "id = ?", "u-self").Error; err != nil {
		t.Fatalf("reload operator: %v", err)
	}
	if stored.DisabledAt != nil {
		t.Error("the operator's own account was disabled despite the refusal")
	}

	var after int64
	if err := db.Model(&model.AuditEvent{}).Count(&after).Error; err != nil {
		t.Fatalf("count audit after: %v", err)
	}
	if after != before {
		t.Errorf("audit rows went from %d to %d; a refused self-disable must write none", before, after)
	}
}

// TestDisableUserHandler_ShouldRefuseSelfDisableWithoutABody pins the order
// of checks: the self refusal comes before the reason is validated, so an
// operator who names themselves with no reason is told the account is theirs,
// not that a reason is missing.
func TestDisableUserHandler_ShouldRefuseSelfDisableWithoutABody(t *testing.T) {
	db := newTestDB(t)
	if err := db.Create(&model.User{ID: "u-self", Subject: "sub-self", Username: "self"}).Error; err != nil {
		t.Fatalf("seed operator: %v", err)
	}
	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, &service.Identity{
		Subject:  "sub-self",
		Username: "self",
		Groups:   []string{cfg.Admin.RequireGroup},
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/u-self/disable", nil))

	if w.Code != http.StatusForbidden {
		t.Fatalf("PATCH self disable with no body = %d, want %d, body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), selfDisableReason) {
		t.Errorf("body %s does not carry the self-disable reason", w.Body.String())
	}
}

// TestDisableUserHandler_ShouldDisableAnotherUserWhenOperatorIsDisabling
// pins that the guard refuses only the caller's own id: disabling a different
// account still succeeds for the same operator.
func TestDisableUserHandler_ShouldDisableAnotherUserWhenOperatorIsDisabling(t *testing.T) {
	db := newTestDB(t)
	for _, u := range []model.User{
		{ID: "u-self", Subject: "sub-self", Username: "self"},
		{ID: "u-other", Subject: "sub-other", Username: "other"},
	} {
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("seed %s: %v", u.Username, err)
		}
	}
	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, &service.Identity{
		Subject:  "sub-self",
		Username: "self",
		Groups:   []string{cfg.Admin.RequireGroup},
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-other/disable", `{"reason":"offboarded"}`))

	if w.Code != http.StatusOK {
		t.Fatalf("PATCH other disable = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	var stored model.User
	if err := db.First(&stored, "id = ?", "u-other").Error; err != nil {
		t.Fatalf("reload other: %v", err)
	}
	if stored.DisabledAt == nil {
		t.Error("the other user was not disabled")
	}
}
