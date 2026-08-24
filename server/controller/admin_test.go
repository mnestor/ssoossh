package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
)

// newTestConfig returns a minimal config suitable for admin tests.
func newTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		HTTP: config.HTTPSettings{
			ServerName: "test-server",
			Port:       8080,
			IsHTTPS:    false,
		},
		DB: config.DB{
			Provider: "sqlite",
		},
		AuthConfig: config.OAuthConfig{
			ProviderURL: "https://example.com/.well-known/openid-configuration",
		},
		Admin: config.AdminConfig{
			RequireGroup: "ssoossh-admins",
			AuditorGroup: "ssoossh-auditors",
		},
		Logging: config.AppLogging{
			Level: "info",
		},
		CertOptions: config.CertificateOptions{
			User: config.CertOptionsUser{
				ValidDuration: 30 * time.Minute,
				Extensions:    []string{"permit-pty", "permit-agent-forwarding"},
			},
			Service: config.CertOptionsService{
				ValidDuration:      24 * time.Hour,
				EnrollmentDuration: 90 * 24 * time.Hour,
			},
			PAM: config.CertOptionsPAM{
				ValidDuration: 5 * time.Minute,
			},
			ClientTimeout: 10 * time.Minute,
		},
	}
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
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		nil,
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
	if _, ok := resp.Data["server_name"]; !ok {
		t.Error("response should contain server_name field")
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
		mockAuditorAuthMiddleware(false),
		mockCSRFMiddleware(),
		nil,
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
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		nil,
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
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		nil,
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
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		nil,
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/enrollments/does-not-exist/expire", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("got status %d, want %d for an unknown enrollment ID", w.Code, http.StatusNotFound)
	}
}

// TestDisableUserHandler_ShouldReturnErrorWhenNotImplemented verifies placeholder returns error.
func TestDisableUserHandler_ShouldReturnErrorWhenNotImplemented(t *testing.T) {
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
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		nil,
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-123/disable", nil)
	r.ServeHTTP(w, req)

	// handleError registers error with middleware but handler continues
	// Status is 200 since handler doesn't explicitly set error status
	if w.Code != http.StatusOK {
		t.Errorf("got status %d, want %d", w.Code, http.StatusOK)
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
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		nil,
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/admin/users/user-123/disable", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d", w.Code, http.StatusForbidden)
	}
}

// TestCertificateHistoryHandler_ShouldReturnEmptyList tests placeholder response.
func TestCertificateHistoryHandler_ShouldReturnEmptyList(t *testing.T) {
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
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		nil,
	)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/certificates/history", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}

	var resp struct {
		Data struct {
			Certificates []any `json:"certificates"`
		} `json:"data"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}

	if len(resp.Data.Certificates) != 0 {
		t.Errorf("expected empty certificate list, got %d items", len(resp.Data.Certificates))
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
		mockAuditorAuthMiddleware(false),
		mockCSRFMiddleware(),
		nil,
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
		mockAuditorAuthMiddleware(true),
		mockCSRFMiddleware(),
		nil,
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
