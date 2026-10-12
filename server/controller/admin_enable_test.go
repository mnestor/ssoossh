package controller

// Test methodology: drive PATCH /admin/users/:id/enable through
// NewAdminController with the real SOC and auditor middleware, and read the
// users row and the audit row back from an in-memory database. The
// properties pinned are the ones issue #127 decided: SOC (and so admin)
// can re-enable anyone whatever disabled them, auditors and plain users
// cannot, and every re-enable is audited with the caller who performed it.
// The anonymous case runs the real session middleware with an empty
// cookie store, so its 401 is the production one, not a stand-in's.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// enableRouter builds the admin routes the way bootstrap orders them:
// session, then role, then CSRF (a passthrough here). A nil identity gets
// the real session middleware over an empty cookie store, which rejects
// the request as unauthenticated; any other identity is injected directly.
func enableRouter(t *testing.T, db *gorm.DB, identity *service.Identity) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	cfg := newTestConfig(t)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	r.Use(sessions.Sessions("ssoossh_session", cookie.NewStore([]byte("test-secret"))))

	sessionAuth := identityMiddleware(identity)
	if identity == nil {
		sessionAuth = middleware.NewSessionAuthMiddleware(time.Hour, time.Hour, nil).Add()
	}

	NewAdminController(
		&r.RouterGroup,
		cfg,
		db,
		sessionAuth,
		middleware.NewAdminAuthMiddleware(cfg).Add(),
		middleware.NewSOCAuthMiddleware(cfg).Add(),
		middleware.NewAuditorAuthMiddleware(cfg).Add(),
		func(c *gin.Context) { c.Next() },
		&stubEnrollmentProvider{},
		service.NewAuditService(cfg, db),
	)
	return r
}

// seedEnableUsers writes a disabled target, disabled by source, and the
// users rows of the callers the tests act as.
func seedEnableUsers(t *testing.T, db *gorm.DB, source model.DisabledSource) {
	t.Helper()

	now := time.Now()
	disabledBy := "u-admin"
	users := []model.User{
		{
			ID: "u-target", Subject: "sub-target", Username: "target",
			DisabledAt: &now, DisabledReason: "prior incident INC-7",
			DisabledSource: &source, DisabledByUserID: &disabledBy,
		},
		{ID: "u-admin", Subject: "sub-admin", Username: "admin"},
		{ID: "u-soc", Subject: "sub-soc", Username: "soc"},
		{ID: "u-auditor", Subject: "sub-auditor", Username: "auditor"},
		{ID: "u-plain", Subject: "sub-plain", Username: "plain"},
	}
	for _, u := range users {
		u.CreatedAt = now
		u.UpdatedAt = now
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("seed %s: %v", u.ID, err)
		}
	}
}

// caller returns the session identity for one of the seeded callers.
func caller(subject, username string, groups ...string) *service.Identity {
	return &service.Identity{Subject: subject, Username: username, Groups: groups}
}

func TestEnableUserHandler_ShouldAuthorizeByRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity *service.Identity
		want     int
	}{
		{
			name:     "should re-enable when the caller is an admin",
			identity: caller("sub-admin", "admin", "ssh-admins"),
			want:     http.StatusOK,
		},
		{
			name:     "should re-enable when the caller is SOC and not admin",
			identity: caller("sub-soc", "soc", "ssh-soc"),
			want:     http.StatusOK,
		},
		{
			name:     "should refuse when the caller is an auditor",
			identity: caller("sub-auditor", "auditor", "ssh-auditors"),
			want:     http.StatusForbidden,
		},
		{
			name:     "should refuse when the caller holds no role",
			identity: caller("sub-plain", "plain", "ssh-users"),
			want:     http.StatusForbidden,
		},
		{
			name:     "should refuse when the caller is anonymous",
			identity: nil,
			want:     http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := newTestDB(t)
			seedEnableUsers(t, db, model.DisabledSourceAdmin)
			r := enableRouter(t, db, tt.identity)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-target/enable", `{"reason":"cleared with security SEC-1"}`))

			if w.Code != tt.want {
				t.Errorf("status = %d, want %d, body: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestEnableUserHandler_ShouldLeaveARefusedTargetDisabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity *service.Identity
	}{
		{name: "should stay disabled when an auditor tries", identity: caller("sub-auditor", "auditor", "ssh-auditors")},
		{name: "should stay disabled when a plain user tries", identity: caller("sub-plain", "plain", "ssh-users")},
		{name: "should stay disabled when an anonymous caller tries", identity: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := newTestDB(t)
			seedEnableUsers(t, db, model.DisabledSourceAdmin)
			r := enableRouter(t, db, tt.identity)

			r.ServeHTTP(httptest.NewRecorder(), jsonPatch(t, "/admin/users/u-target/enable", `{"reason":"cleared with security SEC-1"}`))

			var target model.User
			if err := db.First(&target, "id = ?", "u-target").Error; err != nil {
				t.Fatalf("reload target: %v", err)
			}
			if target.DisabledAt == nil {
				t.Error("the target was re-enabled by a caller the route refuses")
			}
		})
	}
}

// SOC releases a disable whatever took it: an admin's deliberate one, a
// SOC colleague's, or the directory sync's. Each release is audited with
// the SOC caller as the actor, not whoever disabled the account.
func TestEnableUserHandler_ShouldLetSOCReleaseAnyDisable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source model.DisabledSource
	}{
		{name: "should re-enable when an admin disabled the user", source: model.DisabledSourceAdmin},
		{name: "should re-enable when SOC disabled the user", source: model.DisabledSourceSOC},
		{name: "should re-enable when the directory sync disabled the user", source: model.DisabledSourceLDAPSync},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := newTestDB(t)
			seedEnableUsers(t, db, tt.source)
			r := enableRouter(t, db, caller("sub-soc", "soc", "ssh-soc"))

			w := httptest.NewRecorder()
			r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-target/enable", `{"reason":"cleared with security SEC-1"}`))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
			}

			var target model.User
			if err := db.First(&target, "id = ?", "u-target").Error; err != nil {
				t.Fatalf("reload target: %v", err)
			}
			if target.DisabledAt != nil || target.DisabledSource != nil || target.DisabledByUserID != nil {
				t.Errorf("target still carries the disable: at=%v source=%v by=%v",
					target.DisabledAt, target.DisabledSource, target.DisabledByUserID)
			}

			var rows []model.AuditEvent
			if err := db.Where("target_user_id = ?", "u-target").Find(&rows).Error; err != nil {
				t.Fatalf("load audit rows: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("audit rows = %d, want the re-enable recorded once", len(rows))
			}
			if rows[0].ActorUserID == nil || *rows[0].ActorUserID != "u-soc" {
				t.Errorf("actor_user_id = %v, want the SOC caller u-soc", rows[0].ActorUserID)
			}

			var event service.AuditEvent
			if err := json.Unmarshal([]byte(rows[0].Payload), &event); err != nil {
				t.Fatalf("decode audit payload: %v", err)
			}
			if event.Action != service.AuditUserEnabled {
				t.Errorf("action = %q, want %q", event.Action, service.AuditUserEnabled)
			}
			if event.Actor == nil || event.Actor.Username != "soc" || !slices.Contains(event.Actor.Groups, "ssh-soc") {
				t.Errorf("actor = %+v, want the SOC caller's snapshot", event.Actor)
			}
		})
	}
}

// The handler refuses on its own when no caller reached it, so a wiring
// change that dropped the session middleware could not produce a restore
// with no actor on its audit row.
func TestEnableUserHandler_ShouldRefuseWhenNoCallerReachedIt(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	db := newTestDB(t)
	seedEnableUsers(t, db, model.DisabledSourceAdmin)
	cfg := newTestConfig(t)
	a := &adminController{config: cfg, db: db, audit: service.NewAuditService(cfg, db)}

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	r.PATCH("/admin/users/:id/enable", a.enableUserHandler)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-target/enable", `{"reason":"cleared with security SEC-1"}`))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}
