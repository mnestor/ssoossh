package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// newTestDB creates an in-memory SQLite database with migrations applied.
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.User{},
		&model.Enrollment{},
		&model.Certificate{},
		&model.AuditEvent{},
	); err != nil {
		t.Fatalf("failed to migrate test database: %v", err)
	}
	return db
}

// newTestConfig returns a minimal config suitable for admin authorization tests.
func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		HTTP: config.HTTPSettings{
			PublicURL: "http://test-server",
			Port:      8080,
		},
		Admin: config.AdminConfig{
			RequireGroup:    "ssh-admins",
			SOCGroup:        "ssh-soc",
			AuditorGroup:    "ssh-auditors",
			ContactEmail:    "admin@example.com",
			DisabledMessage: "Contact support for re-enablement",
		},
	}
}

// routerWithAuth builds a gin router with identity and real authorization middleware.
func routerWithAuth(t *testing.T, cfg *config.Config, db *gorm.DB, identity *service.Identity) *gin.Engine {
	t.Helper()
	// The enrollment provider arrived with the service-code admin work; the
	// tests reaching this helper exercise the user and config routes and do
	// not care which one they get.
	return routerWithEnrollmentService(t, cfg, db, identity, &stubEnrollmentProvider{})
}

// routerWithEnrollmentService is routerWithAuth with the enrollment provider
// supplied by the caller, for tests that drive the admin enrollment routes.
func routerWithEnrollmentService(t *testing.T, cfg *config.Config, db *gorm.DB, identity *service.Identity, enrollments service.EnrollmentProvider) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())

	// Session identity injection (stands in for SessionAuthMiddleware)
	r.Use(identityMiddleware(identity))

	// Register admin controller with real middleware
	NewAdminController(
		&r.RouterGroup,
		cfg,
		db,
		identityMiddleware(identity), // sessionAuthMiddleware
		middleware.NewAdminAuthMiddleware(cfg).Add(),   // adminAuthMiddleware (real)
		middleware.NewSOCAuthMiddleware(cfg).Add(),     // socAuthMiddleware (real)
		middleware.NewAuditorAuthMiddleware(cfg).Add(), // auditorAuthMiddleware (real)
		func(c *gin.Context) { c.Next() },              // csrfMiddleware (passthrough for tests)
		enrollments,
		service.NewAuditService(cfg, db),
	)

	return r
}

// TestAdminUsersListHandler_AnonymousUserDenied tests GET /api/admin/users
// denies anonymous (no identity).
func TestAdminUsersListHandler_AnonymousUserDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	r := routerWithAuth(t, cfg, db, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/users with no identity: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestAdminUsersListHandler_PlainUserDenied tests GET /api/admin/users
// denies a plain user (no admin/auditor group).
func TestAdminUsersListHandler_PlainUserDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	identity := &service.Identity{
		Subject:  "sub-alice",
		Username: "alice",
		Groups:   []string{"ssh-users"}, // Not in admin or auditor group
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/users as plain user: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestAdminGetUserHandler_AnonymousUserDenied tests GET /api/admin/users/:id
// denies anonymous.
func TestAdminGetUserHandler_AnonymousUserDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	r := routerWithAuth(t, cfg, db, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/user-1", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/users/:id with no identity: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestAdminGetUserHandler_PlainUserDenied tests GET /api/admin/users/:id
// denies a plain user.
func TestAdminGetUserHandler_PlainUserDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	identity := &service.Identity{
		Subject:  "sub-alice",
		Username: "alice",
		Groups:   []string{"ssh-users"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/user-1", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/users/:id as plain user: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestAdminGetUserHandler_AuditorAllowed tests GET /api/admin/users/:id
// allows an auditor.
func TestAdminGetUserHandler_AuditorAllowed(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	testUser := model.User{
		ID:        "user-1",
		Subject:   "sub-alice",
		Username:  "alice",
		Email:     "alice@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&testUser).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{"ssh-auditors"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/user-1", nil))

	if w.Code != http.StatusOK {
		t.Errorf("GET /admin/users/:id as auditor: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// TestAdminGetUserHandler_AdminAllowed tests GET /api/admin/users/:id
// allows an admin.
func TestAdminGetUserHandler_AdminAllowed(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	testUser := model.User{
		ID:        "user-1",
		Subject:   "sub-alice",
		Username:  "alice",
		Email:     "alice@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&testUser).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{"ssh-admins"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/user-1", nil))

	if w.Code != http.StatusOK {
		t.Errorf("GET /admin/users/:id as admin: got %d, want %d", w.Code, http.StatusOK)
	}
}

// TestAdminDisableUserHandler_AnonymousUserDenied tests PATCH /api/admin/users/:id/disable
// denies anonymous.
func TestAdminDisableUserHandler_AnonymousUserDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	r := routerWithAuth(t, cfg, db, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("PATCH /admin/users/:id/disable with no identity: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestAdminDisableUserHandler_PlainUserDenied tests PATCH /api/admin/users/:id/disable
// denies a plain user.
func TestAdminDisableUserHandler_PlainUserDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	identity := &service.Identity{
		Subject:  "sub-alice",
		Username: "alice",
		Groups:   []string{"ssh-users"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("PATCH /admin/users/:id/disable as plain user: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestAdminDisableUserHandler_AuditorDenied tests PATCH /api/admin/users/:id/disable
// denies an auditor (even though they have read access). This is the critical test
// asserting auditors are read-only.
func TestAdminDisableUserHandler_AuditorDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	// Create a user and an auditor to run the test
	testUser := model.User{
		ID:        "user-1",
		Subject:   "sub-alice",
		Username:  "alice",
		Email:     "alice@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&testUser).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	auditorIdentity := model.User{
		ID:        "user-auditor",
		Subject:   "sub-auditor",
		Username:  "auditor",
		Email:     "auditor@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&auditorIdentity).Error; err != nil {
		t.Fatalf("failed to create auditor user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-auditor",
		Username: "auditor",
		Groups:   []string{"ssh-auditors"}, // Auditor group, NOT admin
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("PATCH /admin/users/:id/disable as auditor: got %d, want %d (auditors should be read-only)", w.Code, http.StatusForbidden)
	}
}

// TestAdminDisableUserHandler_AdminAllowed tests PATCH /api/admin/users/:id/disable
// allows an admin.
func TestAdminDisableUserHandler_AdminAllowed(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	testUser := model.User{
		ID:        "user-1",
		Subject:   "sub-alice",
		Username:  "alice",
		Email:     "alice@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&testUser).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	adminUser := model.User{
		ID:        "user-admin",
		Subject:   "sub-admin",
		Username:  "admin",
		Email:     "admin@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&adminUser).Error; err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{"ssh-admins"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	body := []byte(`{"reason":"test reason"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("PATCH /admin/users/:id/disable as admin: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// TestAdminDisableUserHandler_SOCAllowed tests PATCH /api/admin/users/:id/disable
// allows a SOC group member who is not an admin: disabling is a containment
// operation and the core of what the SOC role exists for.
func TestAdminDisableUserHandler_SOCAllowed(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	testUser := model.User{
		ID:        "user-1",
		Subject:   "sub-alice",
		Username:  "alice",
		Email:     "alice@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&testUser).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	socUser := model.User{
		ID:        "user-soc",
		Subject:   "sub-soc",
		Username:  "soc",
		Email:     "soc@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&socUser).Error; err != nil {
		t.Fatalf("failed to create SOC user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-soc",
		Username: "soc",
		Groups:   []string{"ssh-soc"}, // SOC group, NOT admin
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	body := []byte(`{"reason":"test reason"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("PATCH /admin/users/:id/disable as SOC: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// TestAdminEnableUserHandler_SOCDenied tests PATCH /api/admin/users/:id/enable
// denies a SOC group member: re-enabling is restorative, not containment, so
// a SOC analyst must not be able to undo a disable.
func TestAdminEnableUserHandler_SOCDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	now := time.Now()
	disabledUser := model.User{
		ID:         "user-1",
		Subject:    "sub-alice",
		Username:   "alice",
		Email:      "alice@example.com",
		CreatedAt:  now,
		UpdatedAt:  now,
		DisabledAt: &now,
	}
	if err := db.Create(&disabledUser).Error; err != nil {
		t.Fatalf("failed to create disabled test user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-soc",
		Username: "soc",
		Groups:   []string{"ssh-soc"}, // SOC group, NOT admin
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/user-1/enable", `{"reason":"test reason"}`))

	if w.Code != http.StatusForbidden {
		t.Errorf("PATCH /admin/users/:id/enable as SOC: got %d, want %d (re-enabling stays admin-only)", w.Code, http.StatusForbidden)
	}
}

// TestAdminExpireEnrollmentHandler_SOCAllowed tests PATCH
// /api/admin/enrollments/:id/expire allows a SOC group member who is not an
// admin: expiring a credential is the other containment operation SOC holds.
func TestAdminExpireEnrollmentHandler_SOCAllowed(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	enrollment := model.Enrollment{
		ID:        "enroll-1",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := db.Create(&enrollment).Error; err != nil {
		t.Fatalf("failed to create test enrollment: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-soc",
		Username: "soc",
		Groups:   []string{"ssh-soc"}, // SOC group, NOT admin
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/enrollments/enroll-1/expire", `{"reason":"test reason"}`))

	if w.Code != http.StatusOK {
		t.Errorf("PATCH /admin/enrollments/:id/expire as SOC: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// TestAdminExpireEnrollmentHandler_AuditorDenied tests PATCH
// /api/admin/enrollments/:id/expire denies an auditor: expiring moved from
// the admin group to the SOC group, and the read-only role must not have
// picked it up in the move.
func TestAdminExpireEnrollmentHandler_AuditorDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	enrollment := model.Enrollment{
		ID:        "enroll-1",
		UserID:    "user-1",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := db.Create(&enrollment).Error; err != nil {
		t.Fatalf("failed to create test enrollment: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-auditor",
		Username: "auditor",
		Groups:   []string{"ssh-auditors"}, // Auditor group, NOT admin or SOC
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/enrollments/enroll-1/expire", `{"reason":"test reason"}`))

	if w.Code != http.StatusForbidden {
		t.Errorf("PATCH /admin/enrollments/:id/expire as auditor: got %d, want %d (auditors are read-only)", w.Code, http.StatusForbidden)
	}
}

// TestAdminUsersListHandler_SOCAllowed tests GET /api/admin/users allows a
// SOC group member: SOC holds auditor-level reads so an analyst can find the
// user or enrollment they need to contain.
func TestAdminUsersListHandler_SOCAllowed(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	identity := &service.Identity{
		Subject:  "sub-soc",
		Username: "soc",
		Groups:   []string{"ssh-soc"}, // SOC group, NOT admin or auditor
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users", nil))

	if w.Code != http.StatusOK {
		t.Errorf("GET /admin/users as SOC: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// TestAdminEnableUserHandler_AnonymousUserDenied tests PATCH /api/admin/users/:id/enable
// denies anonymous.
func TestAdminEnableUserHandler_AnonymousUserDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	r := routerWithAuth(t, cfg, db, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/user-1/enable", `{"reason":"test reason"}`))

	if w.Code != http.StatusForbidden {
		t.Errorf("PATCH /admin/users/:id/enable with no identity: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestAdminEnableUserHandler_PlainUserDenied tests PATCH /api/admin/users/:id/enable
// denies a plain user.
func TestAdminEnableUserHandler_PlainUserDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	identity := &service.Identity{
		Subject:  "sub-alice",
		Username: "alice",
		Groups:   []string{"ssh-users"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/user-1/enable", `{"reason":"test reason"}`))

	if w.Code != http.StatusForbidden {
		t.Errorf("PATCH /admin/users/:id/enable as plain user: got %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestAdminEnableUserHandler_AuditorDenied tests PATCH /api/admin/users/:id/enable
// denies an auditor (they are read-only).
func TestAdminEnableUserHandler_AuditorDenied(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	testUser := model.User{
		ID:         "user-1",
		Subject:    "sub-alice",
		Username:   "alice",
		Email:      "alice@example.com",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		DisabledAt: &time.Time{},
	}
	if err := db.Create(&testUser).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-auditor",
		Username: "auditor",
		Groups:   []string{"ssh-auditors"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/user-1/enable", `{"reason":"test reason"}`))

	if w.Code != http.StatusForbidden {
		t.Errorf("PATCH /admin/users/:id/enable as auditor: got %d, want %d (auditors should be read-only)", w.Code, http.StatusForbidden)
	}
}

// TestAdminEnableUserHandler_AdminAllowed tests PATCH /api/admin/users/:id/enable
// allows an admin.
func TestAdminEnableUserHandler_AdminAllowed(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	disabledTime := time.Now().Add(-time.Hour)
	testUser := model.User{
		ID:         "user-1",
		Subject:    "sub-alice",
		Username:   "alice",
		Email:      "alice@example.com",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		DisabledAt: &disabledTime,
	}
	if err := db.Create(&testUser).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	adminUser := model.User{
		ID:        "user-admin",
		Subject:   "sub-admin",
		Username:  "admin",
		Email:     "admin@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&adminUser).Error; err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{"ssh-admins"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/user-1/enable", `{"reason":"test reason"}`))

	if w.Code != http.StatusOK {
		t.Errorf("PATCH /admin/users/:id/enable as admin: got %d, want %d", w.Code, http.StatusOK)
	}
}

// TestAdminGetUserHandler_NotFound tests GET /api/admin/users/:id returns 404
// for an unknown user ID.
func TestAdminGetUserHandler_NotFound(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	identity := &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{"ssh-auditors"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/nonexistent-id", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("GET nonexistent user: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestAdminDisableUserHandler_NotFound tests PATCH /api/admin/users/:id/disable
// returns 404 for an unknown user ID.
func TestAdminDisableUserHandler_NotFound(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	adminUser := model.User{
		ID:        "user-admin",
		Subject:   "sub-admin",
		Username:  "admin",
		Email:     "admin@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&adminUser).Error; err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{"ssh-admins"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	body := []byte(`{"reason":"test reason"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/nonexistent-id/disable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("PATCH nonexistent user disable: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestAdminEnableUserHandler_NotFound tests PATCH /api/admin/users/:id/enable
// returns 404 for an unknown user ID.
func TestAdminEnableUserHandler_NotFound(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	adminUser := model.User{
		ID:        "user-admin",
		Subject:   "sub-admin",
		Username:  "admin",
		Email:     "admin@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&adminUser).Error; err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{"ssh-admins"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/nonexistent-id/enable", `{"reason":"test reason"}`))

	if w.Code != http.StatusNotFound {
		t.Errorf("PATCH nonexistent user enable: got %d, want %d", w.Code, http.StatusNotFound)
	}
}

// TestAdminDisableUserHandler_Idempotent tests that disabling the same user
// twice succeeds both times.
func TestAdminDisableUserHandler_Idempotent(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	testUser := model.User{
		ID:        "user-1",
		Subject:   "sub-alice",
		Username:  "alice",
		Email:     "alice@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&testUser).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	adminUser := model.User{
		ID:        "user-admin",
		Subject:   "sub-admin",
		Username:  "admin",
		Email:     "admin@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&adminUser).Error; err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{"ssh-admins"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	// First disable
	w := httptest.NewRecorder()
	body := []byte(`{"reason":"test reason"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first disable: got %d, want %d", w.Code, http.StatusOK)
	}

	// Second disable (should also succeed, idempotent)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/user-1/disable", `{"reason":"test reason"}`))
	if w.Code != http.StatusOK {
		t.Errorf("second disable: got %d, want %d (should be idempotent)", w.Code, http.StatusOK)
	}
}

// TestAdminEnableUserHandler_Idempotent tests that enabling the same user
// twice succeeds both times.
func TestAdminEnableUserHandler_Idempotent(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	disabledTime := time.Now().Add(-time.Hour)
	testUser := model.User{
		ID:         "user-1",
		Subject:    "sub-alice",
		Username:   "alice",
		Email:      "alice@example.com",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		DisabledAt: &disabledTime,
	}
	if err := db.Create(&testUser).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	adminUser := model.User{
		ID:        "user-admin",
		Subject:   "sub-admin",
		Username:  "admin",
		Email:     "admin@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&adminUser).Error; err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{"ssh-admins"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	// First enable
	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/user-1/enable", `{"reason":"test reason"}`))
	if w.Code != http.StatusOK {
		t.Errorf("first enable: got %d, want %d", w.Code, http.StatusOK)
	}

	// Second enable (should also succeed, idempotent)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/user-1/enable", `{"reason":"test reason"}`))
	if w.Code != http.StatusOK {
		t.Errorf("second enable: got %d, want %d (should be idempotent)", w.Code, http.StatusOK)
	}
}

// TestAdminDisableUserHandler_ConsequencesIncludeEnrollmentCount tests that
// the disable response includes the count of active enrollments.
func TestAdminDisableUserHandler_ConsequencesIncludeEnrollmentCount(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	testUser := model.User{
		ID:        "user-1",
		Subject:   "sub-alice",
		Username:  "alice",
		Email:     "alice@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&testUser).Error; err != nil {
		t.Fatalf("failed to create test user: %v", err)
	}

	// Create 3 active enrollments for the user
	for i := 0; i < 3; i++ {
		enrollment := model.Enrollment{
			ID:        fmt.Sprintf("enroll-%d", i),
			UserID:    testUser.ID,
			ExpiresAt: time.Now().Add(time.Hour),
		}
		if err := db.Create(&enrollment).Error; err != nil {
			t.Fatalf("failed to create enrollment: %v", err)
		}
	}

	adminUser := model.User{
		ID:        "user-admin",
		Subject:   "sub-admin",
		Username:  "admin",
		Email:     "admin@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&adminUser).Error; err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	identity := &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{"ssh-admins"},
	}
	r := routerWithAuth(t, cfg, db, identity)

	w := httptest.NewRecorder()
	body := []byte(`{"reason":"test reason"}`)
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("disable with enrollments: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Data struct {
			ServiceEnrollmentCount int `json:"service_enrollment_count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Data.ServiceEnrollmentCount != 3 {
		t.Errorf("expected 3 enrollments in consequence, got %d", resp.Data.ServiceEnrollmentCount)
	}
}

// TestListUsersHandler_AgainstARealDatabase exercises the directory against a
// real schema rather than a mock handle.
//
// The authorization tests above answer "who may call this" and deliberately
// use a mock database, so they pass whatever the query does. That left the
// query itself unexercised, and it was broken: the count ran on a handle with
// no model, so every call failed with gorm's "Table not set" and the
// directory never listed anyone. These cases are the ones that would have
// caught it.
func TestListUsersHandler_AgainstARealDatabase(t *testing.T) {
	seed := []model.User{
		{ID: "u-alice", Subject: "sub-alice", Username: "alice", Email: "alice@corp.example"},
		{ID: "u-bob", Subject: "sub-bob", Username: "bob", Email: "bob@corp.example"},
		{ID: "u-carol", Subject: "sub-carol", Username: "carol", Email: "carol@other.example"},
	}

	tests := []struct {
		name      string
		query     string
		wantCount int
		wantTotal float64
	}{
		{
			name:      "should list every user when nothing is searched for",
			query:     "",
			wantCount: 3,
			wantTotal: 3,
		},
		{
			name:      "should narrow to the users a search matches",
			query:     "?q=corp",
			wantCount: 2,
			wantTotal: 2,
		},
		{
			name:      "should report a total that counts matches, not the page",
			query:     "?limit=1",
			wantCount: 1,
			wantTotal: 3,
		},
		{
			name:      "should return an empty page for a search matching nobody",
			query:     "?q=nobody-by-that-name",
			wantCount: 0,
			wantTotal: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			for i := range seed {
				if err := db.Create(&seed[i]).Error; err != nil {
					t.Fatalf("seed %s: %v", seed[i].Username, err)
				}
			}

			cfg := newTestConfig(t)
			identity := &service.Identity{
				Subject:  "sub-admin",
				Username: "admin",
				Groups:   []string{cfg.Admin.RequireGroup},
			}
			r := routerWithAuth(t, cfg, db, identity)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users"+tt.query, nil))

			if w.Code != http.StatusOK {
				t.Fatalf("GET /admin/users%s = %d, want 200, body: %s", tt.query, w.Code, w.Body.String())
			}

			var resp struct {
				Data struct {
					Users []map[string]any `json:"users"`
					Meta  map[string]any   `json:"meta"`
				} `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode: %v, body: %s", err, w.Body.String())
			}

			if len(resp.Data.Users) != tt.wantCount {
				t.Errorf("returned %d users, want %d", len(resp.Data.Users), tt.wantCount)
			}
			if got, _ := resp.Data.Meta["total"].(float64); got != tt.wantTotal {
				t.Errorf("meta.total = %v, want %v", got, tt.wantTotal)
			}
		})
	}
}

// TestGetUserHandler_AgainstARealDatabase exercises the detail endpoint
// against a real schema, for the same reason as the directory test above: the
// authorization cases run on a mock handle and cannot see a broken query.
func TestGetUserHandler_AgainstARealDatabase(t *testing.T) {
	db := newTestDB(t)
	seeded := model.User{
		ID:              "u-alice",
		Subject:         "sub-alice",
		Username:        "alice",
		Email:           "alice@corp.example",
		OtherAccounts:   `["a.smith"]`,
		ServiceAccounts: `["svc-deploy"]`,
		ExtraFields:     `{"employee_id":"E-40921"}`,
	}
	if err := db.Create(&seeded).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := newTestConfig(t)
	identity := &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{cfg.Admin.RequireGroup},
	}
	r := routerWithAuth(t, cfg, db, identity)

	t.Run("should return the stored identity", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/u-alice", nil))

		if w.Code != http.StatusOK {
			t.Fatalf("GET /admin/users/u-alice = %d, want 200, body: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Data["username"] != "alice" {
			t.Errorf("username = %v, want alice", resp.Data["username"])
		}
	})

	t.Run("should decode the JSON-encoded account lists", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/u-alice", nil))

		var resp struct {
			Data struct {
				OtherAccounts   []string       `json:"other_accounts"`
				ServiceAccounts []string       `json:"service_accounts"`
				ExtraFields     map[string]any `json:"extra_fields"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Data.OtherAccounts) != 1 || resp.Data.OtherAccounts[0] != "a.smith" {
			t.Errorf("other_accounts = %v, want [a.smith]", resp.Data.OtherAccounts)
		}
		if resp.Data.ExtraFields["employee_id"] != "E-40921" {
			t.Errorf("extra_fields = %v, want employee_id E-40921", resp.Data.ExtraFields)
		}
	})

	t.Run("should answer 404 for a user that does not exist", func(t *testing.T) {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/nobody", nil))

		if w.Code != http.StatusNotFound {
			t.Errorf("GET /admin/users/nobody = %d, want 404, body: %s", w.Code, w.Body.String())
		}
	})
}

// TestGetUserHandler_EmptyAccountListsAreArrays pins the wire contract for a
// user whose row carries no accounts.
//
// AdminUserDetail declares these validate:"required" and the generated
// TypeScript types them string[], so null is not an allowed value. A nil
// slice marshals to null, and that is exactly what a freshly-created user
// produces -- which stopped the detail page rendering at all, while a seeded
// user with populated arrays passed every test.
func TestGetUserHandler_EmptyAccountListsAreArrays(t *testing.T) {
	db := newTestDB(t)
	if err := db.Create(&model.User{ID: "u-new", Subject: "sub-new", Username: "newcomer"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{cfg.Admin.RequireGroup},
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/u-new", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/users/u-new = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	for _, field := range []string{"other_accounts", "service_accounts"} {
		if strings.Contains(body, `"`+field+`":null`) {
			t.Errorf("%s serialized as null, want an empty array; body: %s", field, body)
		}
	}
}

// TestDisableUserHandler_ShouldRejectADisableWithNoReason pins the reason
// requirement. The reason used to be optional and was then silently
// discarded, so the next admin opening a disabled account had no way to
// learn why -- the case the audit work exists for. It is now required and
// server-validated, and a body-less disable is refused rather than
// performed.
//
// The 400 must also be a well-formed one. The handler once used gin's
// BindJSON (MustBindWith), which writes a 400 and aborts before returning
// the error; the handler ignored that error because the body was optional
// and wrote its success payload underneath the status gin had already set,
// producing a 400 carrying {"error": null} while the row had in fact been
// updated. ShouldBindJSON is what keeps the status and the body agreeing.
func TestDisableUserHandler_ShouldRejectADisableWithNoReason(t *testing.T) {
	db := newTestDB(t)
	if err := db.Create(&model.User{ID: "u-target", Subject: "sub-target", Username: "target"}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := db.Create(&model.User{ID: "u-admin", Subject: "sub-admin", Username: "admin"}).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{cfg.Admin.RequireGroup},
	})

	tests := []struct {
		name string
		body io.Reader
	}{
		{name: "no body at all", body: nil},
		{name: "body with no reason", body: strings.NewReader(`{}`)},
		{name: "body with a blank reason", body: strings.NewReader(`{"reason":"   "}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPatch, "/admin/users/u-target/disable", tt.body)
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("PATCH .../disable = %d, want 400, body: %s", w.Code, w.Body.String())
			}

			// The refusal has to be real: a 400 with the row disabled
			// anyway is the exact failure the old handler had.
			var stored model.User
			if err := db.First(&stored, "id = ?", "u-target").Error; err != nil {
				t.Fatalf("reload target: %v", err)
			}
			if stored.DisabledAt != nil {
				t.Error("the user was disabled despite the request being refused")
			}
		})
	}
}

// TestDisableUserHandler_ShouldPersistTheReason covers the motivating case:
// the reason survives to the users row, where the person deciding whether
// to re-enable will see it, and to the audit stream.
func TestDisableUserHandler_ShouldPersistTheReason(t *testing.T) {
	db := newTestDB(t)
	if err := db.Create(&model.User{ID: "u-target", Subject: "sub-target", Username: "target"}).Error; err != nil {
		t.Fatalf("seed target: %v", err)
	}
	if err := db.Create(&model.User{ID: "u-admin", Subject: "sub-admin", Username: "admin"}).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{cfg.Admin.RequireGroup},
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/u-target/disable",
		strings.NewReader(`{"reason":"offboarded, SEC-1234"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PATCH .../disable = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var stored model.User
	if err := db.First(&stored, "id = ?", "u-target").Error; err != nil {
		t.Fatalf("reload target: %v", err)
	}
	if stored.DisabledAt == nil {
		t.Fatal("the user was not disabled")
	}
	if stored.DisabledReason != "offboarded, SEC-1234" {
		t.Errorf("disabled_reason = %q, want the submitted reason", stored.DisabledReason)
	}

	// The audit row commits in the same transaction as the disable, so a
	// successful disable always has one.
	var events []model.AuditEvent
	if err := db.Where("target_user_id = ?", "u-target").Find(&events).Error; err != nil {
		t.Fatalf("load audit events: %v", err)
	}
	var found bool
	for _, e := range events {
		if strings.Contains(e.Payload, string(service.AuditUserDisabled)) &&
			strings.Contains(e.Payload, "offboarded, SEC-1234") {
			found = true
		}
	}
	if !found {
		t.Errorf("no user.disabled audit event carrying the reason; events: %+v", events)
	}
}

// jsonPatch builds a PATCH carrying a JSON body, which the containment
// handlers now require: their reason is server-validated, so a body-less
// request is refused before it reaches the behavior under test.
func jsonPatch(t *testing.T, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// mockSessionAuthMiddleware is a test double that sets authenticated identity in context.
func mockSessionAuthMiddleware(authenticated bool, identity string) gin.HandlerFunc {
	return func(g *gin.Context) {
		if authenticated {
			g.Set("user_id", identity)
		}
		g.Next()
	}
}

// mockAdminAuthMiddleware is a test double that only allows authorized admins.
func mockAdminAuthMiddleware(authorized bool) gin.HandlerFunc {
	return func(g *gin.Context) {
		if !authorized {
			g.JSON(http.StatusForbidden, gin.H{"error": "not authorized"})
			g.Abort()
			return
		}
		g.Next()
	}
}

// mockSOCAuthMiddleware is a test double for SOC (containment) authorization.
func mockSOCAuthMiddleware(authorized bool) gin.HandlerFunc {
	return func(g *gin.Context) {
		if !authorized {
			g.JSON(http.StatusForbidden, gin.H{"error": "not authorized"})
			g.Abort()
			return
		}
		g.Next()
	}
}

// mockAuditorAuthMiddleware is a test double for auditor authorization.
func mockAuditorAuthMiddleware(authorized bool) gin.HandlerFunc {
	return func(g *gin.Context) {
		if !authorized {
			g.JSON(http.StatusForbidden, gin.H{"error": "not authorized"})
			g.Abort()
			return
		}
		g.Next()
	}
}

// mockCSRFMiddleware is a test double that passes through.
func mockCSRFMiddleware() gin.HandlerFunc {
	return func(g *gin.Context) {
		g.Next()
	}
}

// mockDB returns a minimal *gorm.DB for testing (handlers don't use it for most operations).
func mockDB() *gorm.DB {
	return &gorm.DB{}
}

// TestEffectiveConfigHandler_ShouldReturnConfigData verifies GET /api/admin/config response.
func TestEffectiveConfigHandler_ShouldReturnConfigData(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	r := gin.New()
	r.Use(mockSessionAuthMiddleware(true, "user-123"))
	r.Use(mockAuditorAuthMiddleware(true))

	NewAdminController(
		&r.RouterGroup,
		cfg,
		mockDB(),
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(true),
		mockSOCAuthMiddleware(true),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	// Verify response has expected fields (structure is correct)
	if resp.Data == nil {
		t.Fatal("response data should not be nil")
	}
	if _, ok := resp.Data["public_url"]; !ok {
		t.Error("response should contain public_url field")
	}
	if _, ok := resp.Data["port"]; !ok {
		t.Error("response should contain port field")
	}
}

// TestEffectiveConfigHandler_RequiresAuditorAuth verifies authorization check.
func TestEffectiveConfigHandler_RequiresAuditorAuth(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	r := gin.New()
	r.Use(mockSessionAuthMiddleware(true, "user-123"))
	r.Use(mockAuditorAuthMiddleware(false)) // Not authorized

	NewAdminController(
		&r.RouterGroup,
		cfg,
		mockDB(),
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(false),
		mockSOCAuthMiddleware(false),
		mockAuditorAuthMiddleware(false),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestExpireEnrollmentHandler_ShouldRejectMissingID tests parameter validation.
func TestExpireEnrollmentHandler_ShouldRejectMissingID(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	r := gin.New()
	r.Use(mockSessionAuthMiddleware(true, "user-123"))
	r.Use(mockAdminAuthMiddleware(true))

	NewAdminController(
		&r.RouterGroup,
		cfg,
		mockDB(),
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(true),
		mockSOCAuthMiddleware(true),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	// Request without :id parameter will not match the route (404)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/enrollments/expire", nil)
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Errorf("got status %d, want non-200 for missing ID", w.Code)
	}
}

// TestExpireEnrollmentHandler_RequiresAdminAuth verifies authorization check.
func TestExpireEnrollmentHandler_RequiresAdminAuth(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	r := gin.New()
	r.Use(mockSessionAuthMiddleware(true, "user-123"))
	r.Use(mockAdminAuthMiddleware(false)) // Not authorized

	NewAdminController(
		&r.RouterGroup,
		cfg,
		mockDB(),
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(false),
		mockSOCAuthMiddleware(false),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/enrollments/enroll-123/expire", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestExpireEnrollmentHandler_ShouldReturn404ForUnknownID pins the fix for a
// silent success: a valid UPDATE that matches no rows must surface as a 404,
// not report {"expired": true} for an enrollment that does not exist. Uses a
// real in-memory DB (the shared mockDB is a bare handle that errors before
// reaching the zero-rows branch) with only the enrollments table the handler
// touches.
func TestExpireEnrollmentHandler_ShouldReturn404ForUnknownID(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&adminEnrollmentModel{}); err != nil {
		t.Fatalf("migrate enrollments table: %v", err)
	}

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewAdminController(
		&r.RouterGroup,
		cfg,
		db,
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(true),
		mockSOCAuthMiddleware(true),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	w := httptest.NewRecorder()
	req := jsonPatch(t, "/admin/enrollments/does-not-exist/expire", `{"reason":"test reason"}`)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want %d for an unknown enrollment ID", w.Code, http.StatusNotFound)
	}
}

// TestDisableUserHandler_RequiresAdminAuth verifies authorization check.
func TestDisableUserHandler_RequiresAdminAuth(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	r := gin.New()
	r.Use(mockSessionAuthMiddleware(true, "user-123"))
	r.Use(mockAdminAuthMiddleware(false)) // Not authorized

	NewAdminController(
		&r.RouterGroup,
		cfg,
		mockDB(),
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(false),
		mockSOCAuthMiddleware(false),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-123/disable", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestCertificateHistoryHandler_ShouldReturnEmptyList tests empty certificate list with page metadata.
func TestCertificateHistoryHandler_ShouldReturnEmptyList(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}

	if err := db.AutoMigrate(&model.Certificate{}, &model.User{}); err != nil {
		t.Fatalf("migrate tables: %v", err)
	}

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewAdminController(
		&r.RouterGroup,
		cfg,
		db,
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(true),
		mockSOCAuthMiddleware(true),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/certificates/history", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Data struct {
			Certificates []any          `json:"certificates"`
			PageMeta     map[string]any `json:"page_meta"`
		} `json:"data"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp.Data.Certificates) != 0 {
		t.Errorf("expected empty certificate list, got %d items", len(resp.Data.Certificates))
	}
	if total, ok := resp.Data.PageMeta["total"]; !ok || total != float64(0) {
		t.Errorf("expected total=0, got %v", total)
	}
}

// TestCertificateHistoryHandler_RequiresAuditorAuth verifies authorization check.
func TestCertificateHistoryHandler_RequiresAuditorAuth(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	r := gin.New()
	r.Use(mockSessionAuthMiddleware(true, "user-123"))
	r.Use(mockAuditorAuthMiddleware(false)) // Not authorized

	NewAdminController(
		&r.RouterGroup,
		cfg,
		mockDB(),
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(false),
		mockSOCAuthMiddleware(false),
		mockAuditorAuthMiddleware(false),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/certificates/history", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestAdminEnrollmentModelTableName verifies GORM table name.
func TestAdminEnrollmentModelTableName(t *testing.T) {
	t.Parallel()

	model := adminEnrollmentModel{}
	if got := model.TableName(); got != "enrollments" {
		t.Errorf("TableName() = %q, want %q", got, "enrollments")
	}
}

// TestNewAdminController_RoutesAreRegistered verifies routes are registered without error.
func TestNewAdminController_RoutesAreRegistered(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	r := gin.New()

	// Should not panic or error
	NewAdminController(
		&r.RouterGroup,
		cfg,
		mockDB(),
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(true),
		mockSOCAuthMiddleware(true),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	// Basic test: GET /admin/config should be accessible
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/config", nil)
	r.ServeHTTP(w, req)

	// Should reach the handler (may be unauthorized, that's OK for this test)
	if w.Code == http.StatusNotFound {
		t.Errorf("route /admin/config not found, status %d", w.Code)
	}
}

// TestAdminConfigPredicates tests config predicate methods.
func TestAdminConfigPredicates(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)

	// Test admin role is enabled
	if !cfg.Admin.IsAdminEnabled() {
		t.Error("IsAdminEnabled() should return true when RequireGroup is set")
	}

	// Test auditor role is enabled
	if !cfg.Admin.IsAuditorEnabled() {
		t.Error("IsAuditorEnabled() should return true when AuditorGroup is set")
	}
}

// TestCertificateHistoryHandler_SearchesCertificates tests that search over
// key ID, principals, serial number, fingerprint, username, and email works.
func TestCertificateHistoryHandler_SearchesCertificates(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}

	// Migrate tables we need.
	if err := db.AutoMigrate(&model.User{}, &model.Certificate{}); err != nil {
		t.Fatalf("migrate tables: %v", err)
	}

	// Create a user.
	user := model.User{
		ID:       "user1",
		Subject:  "sub1",
		Username: "alice",
		Email:    "alice@example.com",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create a certificate.
	now := time.Now()
	cert := model.Certificate{
		ID:                   "cert1",
		Type:                 model.CertificateTypeUser,
		UserID:               &user.ID,
		SerialNumber:         12345,
		KeyID:                "my-key-id",
		Principals:           "alice,alice@server",
		PublicKeyFingerprint: "SHA256:abcdef123456",
		IssuedAt:             now,
		ExpiresAt:            now.Add(1 * time.Hour),
	}
	if err := db.Create(&cert).Error; err != nil {
		t.Fatalf("failed to create cert: %v", err)
	}

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewAdminController(
		&r.RouterGroup,
		cfg,
		db,
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(false),
		mockSOCAuthMiddleware(false),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	// Test searching by key ID.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/certificates/history?q=my-key", nil)
	r.ServeHTTP(w, req)

	var resp struct {
		Data struct {
			Certificates []any `json:"certificates"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Data.Certificates) != 1 {
		t.Errorf("search by key ID: got %d results, want 1", len(resp.Data.Certificates))
	}

	// Test searching by username.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/admin/certificates/history?q=alice", nil)
	r.ServeHTTP(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Data.Certificates) != 1 {
		t.Errorf("search by username: got %d results, want 1", len(resp.Data.Certificates))
	}
}

// TestCertificateHistoryHandler_FiltersByType tests certificate type filtering.
func TestCertificateHistoryHandler_FiltersByType(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}

	if err := db.AutoMigrate(&model.User{}, &model.Certificate{}); err != nil {
		t.Fatalf("migrate tables: %v", err)
	}

	user := model.User{
		ID:       "user1",
		Subject:  "sub1",
		Username: "alice",
		Email:    "alice@example.com",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	now := time.Now()
	// Create one user cert.
	cert1 := model.Certificate{
		ID:           "cert1",
		Type:         model.CertificateTypeUser,
		UserID:       &user.ID,
		SerialNumber: 1000,
		IssuedAt:     now,
		ExpiresAt:    now.Add(1 * time.Hour),
	}
	if err := db.Create(&cert1).Error; err != nil {
		t.Fatalf("failed to create cert1: %v", err)
	}

	// Create one service cert.
	cert2 := model.Certificate{
		ID:           "cert2",
		Type:         model.CertificateTypeService,
		UserID:       &user.ID,
		SerialNumber: 1001,
		IssuedAt:     now,
		ExpiresAt:    now.Add(1 * time.Hour),
	}
	if err := db.Create(&cert2).Error; err != nil {
		t.Fatalf("failed to create cert2: %v", err)
	}

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewAdminController(
		&r.RouterGroup,
		cfg,
		db,
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(false),
		mockSOCAuthMiddleware(false),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	// Filter by user type.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/certificates/history?type=user", nil)
	r.ServeHTTP(w, req)

	var resp struct {
		Data struct {
			Certificates []map[string]any `json:"certificates"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Data.Certificates) != 1 {
		t.Errorf("filter by user type: got %d results, want 1", len(resp.Data.Certificates))
	}
	if typ, ok := resp.Data.Certificates[0]["type"]; !ok || typ != "user" {
		t.Errorf("filtered result should have type=user")
	}
}

// TestCertificateHistoryHandler_FiltersByStatus tests expiration status filtering.
func TestCertificateHistoryHandler_FiltersByStatus(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}

	if err := db.AutoMigrate(&model.User{}, &model.Certificate{}); err != nil {
		t.Fatalf("migrate tables: %v", err)
	}

	user := model.User{
		ID:       "user1",
		Subject:  "sub1",
		Username: "alice",
		Email:    "alice@example.com",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	now := time.Now()
	// Create one live cert.
	cert1 := model.Certificate{
		ID:           "cert1",
		Type:         model.CertificateTypeUser,
		UserID:       &user.ID,
		SerialNumber: 1000,
		IssuedAt:     now.Add(-1 * time.Hour),
		ExpiresAt:    now.Add(1 * time.Hour),
	}
	if err := db.Create(&cert1).Error; err != nil {
		t.Fatalf("failed to create cert1: %v", err)
	}

	// Create one expired cert.
	cert2 := model.Certificate{
		ID:           "cert2",
		Type:         model.CertificateTypeUser,
		UserID:       &user.ID,
		SerialNumber: 1001,
		IssuedAt:     now.Add(-2 * time.Hour),
		ExpiresAt:    now.Add(-1 * time.Hour),
	}
	if err := db.Create(&cert2).Error; err != nil {
		t.Fatalf("failed to create cert2: %v", err)
	}

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewAdminController(
		&r.RouterGroup,
		cfg,
		db,
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(false),
		mockSOCAuthMiddleware(false),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	// Filter for live certs only.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/certificates/history?status=live", nil)
	r.ServeHTTP(w, req)

	var resp struct {
		Data struct {
			Certificates []any `json:"certificates"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Data.Certificates) != 1 {
		t.Errorf("filter by live status: got %d results, want 1", len(resp.Data.Certificates))
	}
}

// TestCertificateHistoryHandler_Pagination tests offset-based pagination.
func TestCertificateHistoryHandler_Pagination(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	cfg := newTestConfig(t)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}

	if err := db.AutoMigrate(&model.User{}, &model.Certificate{}); err != nil {
		t.Fatalf("migrate tables: %v", err)
	}

	user := model.User{
		ID:       "user1",
		Subject:  "sub1",
		Username: "alice",
		Email:    "alice@example.com",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	now := time.Now()
	// Create 10 certificates.
	for i := 0; i < 10; i++ {
		cert := model.Certificate{
			ID:           "cert" + string(rune(i)),
			Type:         model.CertificateTypeUser,
			UserID:       &user.ID,
			SerialNumber: uint64(1000 + i),
			IssuedAt:     now.Add(-time.Duration(i) * time.Hour),
			ExpiresAt:    now.Add(time.Duration(1-i) * time.Hour),
		}
		if err := db.Create(&cert).Error; err != nil {
			t.Fatalf("failed to create cert: %v", err)
		}
	}

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewAdminController(
		&r.RouterGroup,
		cfg,
		db,
		mockSessionAuthMiddleware(true, "user-123"),
		mockAdminAuthMiddleware(false),
		mockSOCAuthMiddleware(false),
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		&stubEnrollmentProvider{},
		nil, // auditor: these cases assert routing and payloads, not auditing
	)

	// Request first page with limit=5.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/certificates/history?limit=5&offset=0", nil)
	r.ServeHTTP(w, req)

	var resp struct {
		Data struct {
			Certificates []any          `json:"certificates"`
			PageMeta     map[string]any `json:"page_meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if len(resp.Data.Certificates) != 5 {
		t.Errorf("page 1: got %d certs, want 5", len(resp.Data.Certificates))
	}
	if total, ok := resp.Data.PageMeta["total"]; !ok || total != float64(10) {
		t.Errorf("page 1: got total %v, want 10", total)
	}
}
