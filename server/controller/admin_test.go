package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
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
			ServerName: "test-server",
			Port:       8080,
			IsHTTPS:    false,
		},
		Admin: config.AdminConfig{
			RequireGroup:       "ssh-admins",
			AuditorGroup:       "ssh-auditors",
			DisableGracePeriod: 30 * time.Minute,
			ContactEmail:       "admin@example.com",
			DisabledMessage:    "Contact support for re-enablement",
		},
	}
}

// routerWithAuth builds a gin router with identity and real authorization middleware.
func routerWithAuth(t *testing.T, cfg *config.Config, db *gorm.DB, identity *service.Identity) *gin.Engine {
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
		middleware.NewAuditorAuthMiddleware(cfg).Add(), // auditorAuthMiddleware (real)
		func(c *gin.Context) { c.Next() },              // csrfMiddleware (passthrough for tests)
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
	body := []byte("{}")
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("PATCH /admin/users/:id/disable as admin: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
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
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/enable", nil))

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
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/enable", nil))

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
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/enable", nil))

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
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/enable", nil))

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
	body := []byte("{}")
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
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/nonexistent-id/enable", nil))

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
	body := []byte("{}")
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("first disable: got %d, want %d", w.Code, http.StatusOK)
	}

	// Second disable (should also succeed, idempotent)
	w = httptest.NewRecorder()
	body = []byte("{}")
	req = httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
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
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/enable", nil))
	if w.Code != http.StatusOK {
		t.Errorf("first enable: got %d, want %d", w.Code, http.StatusOK)
	}

	// Second enable (should also succeed, idempotent)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/enable", nil))
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
	body := []byte("{}")
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("disable with enrollments: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Data struct {
			ServiceEnrollmentCount int       `json:"service_enrollment_count"`
			GracePeriodSeconds     int64     `json:"grace_period_seconds"`
			ExpireAtTimestamp      time.Time `json:"expire_at_timestamp"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Data.ServiceEnrollmentCount != 3 {
		t.Errorf("expected 3 enrollments in consequence, got %d", resp.Data.ServiceEnrollmentCount)
	}
	if resp.Data.GracePeriodSeconds != int64(cfg.Admin.DisableGracePeriod.Seconds()) {
		t.Errorf("expected grace period %v, got %d seconds", cfg.Admin.DisableGracePeriod, resp.Data.GracePeriodSeconds)
	}
}

// TestAdminDisableUserHandler_ConsequencesShowExpireTime tests that disable
// returns the correct expiry timestamp (now + grace period).
func TestAdminDisableUserHandler_ConsequencesShowExpireTime(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	cfg.Admin.DisableGracePeriod = 30 * time.Minute // Use a fixed grace period
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

	before := time.Now()
	w := httptest.NewRecorder()
	body := []byte("{}")
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-1/disable", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	after := time.Now()

	if w.Code != http.StatusOK {
		t.Errorf("disable: got %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp struct {
		Data struct {
			ExpireAtTimestamp time.Time `json:"expire_at_timestamp"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	expectedMin := before.Add(cfg.Admin.DisableGracePeriod)
	expectedMax := after.Add(cfg.Admin.DisableGracePeriod)
	if resp.Data.ExpireAtTimestamp.Before(expectedMin) || resp.Data.ExpireAtTimestamp.After(expectedMax) {
		t.Errorf("expiry time not within expected window: %v (expected between %v and %v)",
			resp.Data.ExpireAtTimestamp, expectedMin, expectedMax)
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

// TestDisableUserHandler_EmptyBodyIsNotABadRequest pins the status of a
// disable with no body, which is how the UI calls it.
//
// The handler used gin's BindJSON, which is MustBindWith: on an empty body it
// writes a 400 and aborts before returning the error. The handler then
// ignored that error, because the body is optional, and wrote its success
// payload underneath the status gin had already set. The response was a 400
// carrying {"error": null} -- a shape no client can interpret, and one the
// browser treated as a failure while the database row had in fact been
// updated.
func TestDisableUserHandler_EmptyBodyIsNotABadRequest(t *testing.T) {
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
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/admin/users/u-target/disable", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("PATCH .../disable with no body = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	// And the row really moved, so a 200 is not merely cosmetic.
	var stored model.User
	if err := db.First(&stored, "id = ?", "u-target").Error; err != nil {
		t.Fatalf("reload target: %v", err)
	}
	if stored.DisabledAt == nil {
		t.Error("the user was not disabled")
	}
}
