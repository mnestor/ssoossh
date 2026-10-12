package controller

// Test methodology: drive PATCH /admin/users/:id/disable through the real
// router as an admin or a SOC analyst targeting their own account, and read
// the outcome back from the users row and the audit table. A refused
// self-disable must leave the users row exactly as it was and add exactly
// one user.disable_refused audit row.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
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
// reason the UI shows.
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

// TestDisableUserHandler_ShouldLeaveUserRowUnchangedOnSelfDisable pins that
// the refusal happens before any write to the users row: the operator's
// account reads back exactly as it was seeded.
func TestDisableUserHandler_ShouldLeaveUserRowUnchangedOnSelfDisable(t *testing.T) {
	db := newTestDB(t)
	seedSelfOperator(t, db)
	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, selfOperatorIdentity(cfg))

	var before model.User
	if err := db.First(&before, "id = ?", "u-self").Error; err != nil {
		t.Fatalf("load operator before: %v", err)
	}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-self/disable", `{"reason":"offboarded"}`))

	var after model.User
	if err := db.First(&after, "id = ?", "u-self").Error; err != nil {
		t.Fatalf("reload operator: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("the operator's row changed despite the refusal:\nbefore %+v\nafter  %+v", before, after)
	}
}

// TestDisableUserHandler_ShouldAuditRefusedSelfDisable pins the one audit
// row a refused self-disable writes. The caller is both actor and target, so
// counting every row in the table also proves nothing else was written.
func TestDisableUserHandler_ShouldAuditRefusedSelfDisable(t *testing.T) {
	db := newTestDB(t)
	seedSelfOperator(t, db)
	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, selfOperatorIdentity(cfg))

	req := jsonPatch(t, "/admin/users/u-self/disable", `{"reason":"offboarded"}`)
	req.RemoteAddr = "198.51.100.7:4242"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("PATCH self disable = %d, want %d, body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
	rows := loadAuditRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want exactly 1 for a refused self-disable", len(rows))
	}
	row := rows[0]
	event := decodeAuditPayload(t, row)

	cases := []struct {
		name string
		got  any
		want any
	}{
		{name: "should record the refused action", got: event.Action, want: service.AuditUserDisableRefused},
		{name: "should index the operator as actor", got: derefString(row.ActorUserID), want: "u-self"},
		{name: "should index the operator as target", got: derefString(row.TargetUserID), want: "u-self"},
		{name: "should snapshot the actor's username", got: usernameOf(event.Actor), want: "self"},
		{name: "should snapshot the target's username", got: usernameOf(event.Target), want: "self"},
		{name: "should record the refusal reason", got: event.Reason, want: "an operator cannot disable their own account"},
		{name: "should record the source address the server observed", got: event.Detail["source_ip"], want: "198.51.100.7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

// TestDisableUserHandler_ShouldAuditObservedAddressNotForwardedHeader pins
// that the recorded address is the one the server resolved, not a
// forwarding header the client wrote: with no trusted proxy configured, an
// X-Forwarded-For value must be ignored.
func TestDisableUserHandler_ShouldAuditObservedAddressNotForwardedHeader(t *testing.T) {
	db := newTestDB(t)
	seedSelfOperator(t, db)
	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, selfOperatorIdentity(cfg))
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}

	req := jsonPatch(t, "/admin/users/u-self/disable", `{"reason":"offboarded"}`)
	req.RemoteAddr = "198.51.100.7:4242"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	r.ServeHTTP(httptest.NewRecorder(), req)

	rows := loadAuditRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	if got := decodeAuditPayload(t, rows[0]).Detail["source_ip"]; got != "198.51.100.7" {
		t.Errorf("source_ip = %v, want the peer address 198.51.100.7, not the forwarded one", got)
	}
}

// TestDisableUserHandler_ShouldNotAuditCredentialHeadersOnSelfDisable pins
// the invariant that audit records never capture Cookie or Authorization.
func TestDisableUserHandler_ShouldNotAuditCredentialHeadersOnSelfDisable(t *testing.T) {
	db := newTestDB(t)
	seedSelfOperator(t, db)
	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, selfOperatorIdentity(cfg))

	req := jsonPatch(t, "/admin/users/u-self/disable", `{"reason":"offboarded"}`)
	req.Header.Set("Cookie", "session=cookie-secret-value")
	req.Header.Set("Authorization", "Bearer auth-secret-value")
	r.ServeHTTP(httptest.NewRecorder(), req)

	for _, row := range loadAuditRows(t, db) {
		for _, secret := range []string{"cookie-secret-value", "auth-secret-value"} {
			if strings.Contains(row.Payload, secret) {
				t.Errorf("audit payload carries %q: %s", secret, row.Payload)
			}
		}
	}
}

// TestDisableUserHandler_ShouldRefuseSelfDisableWithNoAuditService pins
// that the refusal still holds when the controller has no audit recorder.
func TestDisableUserHandler_ShouldRefuseSelfDisableWithNoAuditService(t *testing.T) {
	db := newTestDB(t)
	seedSelfOperator(t, db)
	cfg := newTestConfig(t)
	identity := selfOperatorIdentity(cfg)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	r.Use(identityMiddleware(identity))
	NewAdminController(
		&r.RouterGroup,
		cfg,
		db,
		identityMiddleware(identity),
		middleware.NewAdminAuthMiddleware(cfg).Add(),
		middleware.NewSOCAuthMiddleware(cfg).Add(),
		middleware.NewAuditorAuthMiddleware(cfg).Add(),
		func(c *gin.Context) { c.Next() },
		&stubEnrollmentProvider{},
		nil,
	)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-self/disable", `{"reason":"offboarded"}`))

	if w.Code != http.StatusForbidden {
		t.Errorf("PATCH self disable with no audit service = %d, want %d", w.Code, http.StatusForbidden)
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

// TestDisableUserHandler_ShouldWriteOnlyTheDisabledRowForAnotherUser pins
// that the refusal audit is scoped to self-disable: disabling a different
// account writes its one user.disabled row and no refusal row.
func TestDisableUserHandler_ShouldWriteOnlyTheDisabledRowForAnotherUser(t *testing.T) {
	db := newTestDB(t)
	seedSelfOperator(t, db)
	if err := db.Create(&model.User{ID: "u-other", Subject: "sub-other", Username: "other"}).Error; err != nil {
		t.Fatalf("seed other: %v", err)
	}
	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, selfOperatorIdentity(cfg))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-other/disable", `{"reason":"offboarded"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH other disable = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var actions []service.AuditAction
	for _, row := range loadAuditRows(t, db) {
		actions = append(actions, decodeAuditPayload(t, row).Action)
	}
	want := []service.AuditAction{service.AuditUserDisabled}
	if !reflect.DeepEqual(actions, want) {
		t.Errorf("audit actions = %v, want %v", actions, want)
	}
}

// seedSelfOperator writes the operator row the self-disable tests target.
func seedSelfOperator(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Create(&model.User{ID: "u-self", Subject: "sub-self", Username: "self", Email: "self@example.com"}).Error; err != nil {
		t.Fatalf("seed operator: %v", err)
	}
}

// selfOperatorIdentity is the session identity of the seeded operator,
// holding the admin group.
func selfOperatorIdentity(cfg *config.Config) *service.Identity {
	return &service.Identity{
		Subject:  "sub-self",
		Username: "self",
		Email:    "self@example.com",
		Groups:   []string{cfg.Admin.RequireGroup},
	}
}

// loadAuditRows returns every audit row in the table.
func loadAuditRows(t *testing.T, db *gorm.DB) []model.AuditEvent {
	t.Helper()
	var rows []model.AuditEvent
	if err := db.Find(&rows).Error; err != nil {
		t.Fatalf("load audit rows: %v", err)
	}
	return rows
}

// decodeAuditPayload decodes one stored row's payload back into its event.
func decodeAuditPayload(t *testing.T, row model.AuditEvent) service.AuditEvent {
	t.Helper()
	var event service.AuditEvent
	if err := json.Unmarshal([]byte(row.Payload), &event); err != nil {
		t.Fatalf("decode audit payload %q: %v", row.Payload, err)
	}
	return event
}

// derefString reads an optional indexed column, empty when unset.
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// usernameOf reads a snapshot's username, empty when there is no snapshot.
func usernameOf(subject *service.AuditSubject) string {
	if subject == nil {
		return ""
	}
	return subject.Username
}
