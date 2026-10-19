package controller

// Test methodology: the NSS fields of /users/me through the user
// controller behind the error middleware, with identityMiddleware in place
// of the session and CSRF passing through.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/service"
)

// newNSSUserRouter mounts the user controller for identity.
func newNSSUserRouter(cfg *config.Config, identity *service.Identity) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewUserController(&r.RouterGroup, cfg, identityMiddleware(identity), &mockUserDatabase{}, nil, func(g *gin.Context) { g.Next() })
	return r
}

// currentUserFields reads GET /users/me's data as raw fields, so a test
// can tell null from absent.
func currentUserFields(t *testing.T, r *gin.Engine) map[string]json.RawMessage {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/me", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var fields map[string]json.RawMessage
	decodeEnvelope(t, w.Body.Bytes(), &fields)
	return fields
}

func TestCurrentUserHandler_ShouldReportTheNSSKeyAdminRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		keyGroup string
		groups   []string
		want     bool
	}{
		{name: "should report the role when the caller is in admin.nss_key_group", keyGroup: "nss-key-admins", groups: []string{"nss-key-admins"}, want: true},
		{name: "should report the role when the caller is an admin", keyGroup: "nss-key-admins", groups: []string{"ssh-admins"}, want: true},
		{name: "should report the role when the caller is an admin and no key group is set", keyGroup: "", groups: []string{"ssh-admins"}, want: true},
		{name: "should not report the role when the caller is SOC", keyGroup: "nss-key-admins", groups: []string{"ssh-soc"}},
		{name: "should not report the role when the caller is an auditor", keyGroup: "nss-key-admins", groups: []string{"ssh-auditors"}},
		{name: "should not report the role when the caller is a plain user", keyGroup: "nss-key-admins", groups: []string{"ssh-users"}},
		{name: "should not report the role when the caller is in no group", keyGroup: "nss-key-admins", groups: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := newNSSTestConfig(t)
			cfg.Admin.NSSKeyGroup = tc.keyGroup
			r := newNSSUserRouter(cfg, &service.Identity{Subject: "sub-1", Groups: tc.groups})

			if got := string(currentUserFields(t, r)["is_nss_key_admin"]); got != boolJSON(tc.want) {
				t.Errorf("is_nss_key_admin = %s, want %s", got, boolJSON(tc.want))
			}
		})
	}
}

// boolJSON is b as JSON.
func boolJSON(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func TestCurrentUserHandler_ShouldReportWhetherNSSIsEnabled(t *testing.T) {
	t.Parallel()

	for _, enabled := range []bool{true, false} {
		t.Run("should report nss_enabled "+boolJSON(enabled)+" when the config says so", func(t *testing.T) {
			t.Parallel()

			cfg := newNSSTestConfig(t)
			cfg.NSS.Enabled = enabled
			r := newNSSUserRouter(cfg, &service.Identity{Subject: "sub-1"})

			if got := string(currentUserFields(t, r)["nss_enabled"]); got != boolJSON(enabled) {
				t.Errorf("nss_enabled = %s, want %s", got, boolJSON(enabled))
			}
		})
	}
}
