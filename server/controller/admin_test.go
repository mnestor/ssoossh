package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
