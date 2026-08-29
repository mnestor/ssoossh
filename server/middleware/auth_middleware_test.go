package middleware

// Test methodology: the identity is injected straight onto the gin context
// under IdentityContextKey rather than round-tripped through a real session,
// because these tests are about the authorization decision alone —
// session_auth_test.go already covers how the identity gets there. Each case
// asserts both the status code and whether the protected handler ran, since a
// middleware that returns 403 but still calls c.Next() would otherwise look
// like it passed.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/service"
)

// identityMode selects what the stub auth middleware puts on the context,
// distinguishing "no identity at all" from "the key holds a nil identity".
type identityMode int

const (
	identityAbsent identityMode = iota
	identityNil
	identityPresent
)

// runAuthMiddleware drives guard behind a stub that seeds the context identity
// per mode, and reports the response status and whether the guarded handler
// ran.
func runAuthMiddleware(t *testing.T, guard gin.HandlerFunc, mode identityMode, groups []string) (int, bool) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(NewErrorHandlerMiddleware().Add())
	r.Use(func(c *gin.Context) {
		switch mode {
		case identityNil:
			c.Set(IdentityContextKey, (*service.Identity)(nil))
		case identityPresent:
			c.Set(IdentityContextKey, &service.Identity{Subject: "sub-1", Username: "user", Groups: groups})
		case identityAbsent:
		}
		c.Next()
	})

	var reached bool
	r.GET("/guarded", guard, func(c *gin.Context) {
		reached = true
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/guarded", nil))
	return w.Code, reached
}

// adminConfig builds a config with the admin, SOC, and auditor groups set.
func adminConfig(requireGroup, socGroup, auditorGroup string) *config.Config {
	c := &config.Config{}
	c.Admin.RequireGroup = requireGroup
	c.Admin.SOCGroup = socGroup
	c.Admin.AuditorGroup = auditorGroup
	return c
}

func TestAdminAuthMiddleware_Add(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		requireGroup string
		socGroup     string
		auditorGroup string
		mode         identityMode
		groups       []string
		wantStatus   int
		wantReached  bool
	}{
		{
			name:         "should allow when the caller is in the admin group",
			requireGroup: "ssh-admins",
			mode:         identityPresent,
			groups:       []string{"staff", "ssh-admins"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "should deny when the caller is only SOC",
			requireGroup: "ssh-admins",
			socGroup:     "ssh-soc",
			mode:         identityPresent,
			groups:       []string{"ssh-soc"},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the caller is in no matching group",
			requireGroup: "ssh-admins",
			mode:         identityPresent,
			groups:       []string{"staff"},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the caller has no groups at all",
			requireGroup: "ssh-admins",
			mode:         identityPresent,
			groups:       nil,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the admin group is unconfigured",
			requireGroup: "",
			mode:         identityPresent,
			groups:       []string{"ssh-admins"},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the caller carries an empty group matching the unset config",
			requireGroup: "",
			mode:         identityPresent,
			groups:       []string{""},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when no identity is on the context",
			requireGroup: "ssh-admins",
			mode:         identityAbsent,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the context identity is nil",
			requireGroup: "ssh-admins",
			mode:         identityNil,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the caller is only an auditor",
			requireGroup: "ssh-admins",
			auditorGroup: "ssh-auditors",
			mode:         identityPresent,
			groups:       []string{"ssh-auditors"},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should match the admin group case-sensitively",
			requireGroup: "ssh-admins",
			mode:         identityPresent,
			groups:       []string{"SSH-Admins"},
			wantStatus:   http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			guard := NewAdminAuthMiddleware(adminConfig(tt.requireGroup, tt.socGroup, tt.auditorGroup)).Add()
			status, reached := runAuthMiddleware(t, guard, tt.mode, tt.groups)

			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if reached != tt.wantReached {
				t.Errorf("handler reached = %v, want %v", reached, tt.wantReached)
			}
		})
	}
}

// TestSOCAuthMiddleware_Add covers SOC as a child role of admin: admin group
// membership satisfies SOC routes on its own, including when soc_group is
// left unset, while auditor membership never does — SOC guards containment
// writes, not reads.
func TestSOCAuthMiddleware_Add(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		requireGroup string
		socGroup     string
		auditorGroup string
		mode         identityMode
		groups       []string
		wantStatus   int
		wantReached  bool
	}{
		{
			name:         "should allow when the caller is in the SOC group",
			requireGroup: "ssh-admins",
			socGroup:     "ssh-soc",
			mode:         identityPresent,
			groups:       []string{"ssh-soc"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "should allow an admin who is not in the SOC group",
			requireGroup: "ssh-admins",
			socGroup:     "ssh-soc",
			mode:         identityPresent,
			groups:       []string{"ssh-admins"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "should allow an admin when the SOC group is unconfigured",
			requireGroup: "ssh-admins",
			socGroup:     "",
			mode:         identityPresent,
			groups:       []string{"ssh-admins"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "should allow a SOC member when the admin group is unconfigured",
			requireGroup: "",
			socGroup:     "ssh-soc",
			mode:         identityPresent,
			groups:       []string{"ssh-soc"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "should deny an auditor who is in neither the admin nor the SOC group",
			requireGroup: "ssh-admins",
			socGroup:     "ssh-soc",
			auditorGroup: "ssh-auditors",
			mode:         identityPresent,
			groups:       []string{"ssh-auditors"},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny a non-admin when the SOC group is unconfigured",
			requireGroup: "ssh-admins",
			socGroup:     "",
			mode:         identityPresent,
			groups:       []string{"staff"},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when neither group is configured",
			requireGroup: "",
			socGroup:     "",
			mode:         identityPresent,
			groups:       []string{"staff"},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the caller carries an empty group matching both unset groups",
			requireGroup: "",
			socGroup:     "",
			mode:         identityPresent,
			groups:       []string{""},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the caller has no groups at all",
			requireGroup: "ssh-admins",
			socGroup:     "ssh-soc",
			mode:         identityPresent,
			groups:       nil,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when no identity is on the context",
			requireGroup: "ssh-admins",
			socGroup:     "ssh-soc",
			mode:         identityAbsent,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the context identity is nil",
			requireGroup: "ssh-admins",
			socGroup:     "ssh-soc",
			mode:         identityNil,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should match the SOC group case-sensitively",
			requireGroup: "ssh-admins",
			socGroup:     "ssh-soc",
			mode:         identityPresent,
			groups:       []string{"SSH-SOC"},
			wantStatus:   http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			guard := NewSOCAuthMiddleware(adminConfig(tt.requireGroup, tt.socGroup, tt.auditorGroup)).Add()
			status, reached := runAuthMiddleware(t, guard, tt.mode, tt.groups)

			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if reached != tt.wantReached {
				t.Errorf("handler reached = %v, want %v", reached, tt.wantReached)
			}
		})
	}
}

// TestAuditorAuthMiddleware_Add covers auditor as a child role of admin and
// SOC: membership in either of those groups satisfies auditor routes on its
// own, including when auditor_group is left unset, so narrowing the auditor
// group never locks admins or SOC out of the audit endpoints.
func TestAuditorAuthMiddleware_Add(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		requireGroup string
		socGroup     string
		auditorGroup string
		mode         identityMode
		groups       []string
		wantStatus   int
		wantReached  bool
	}{
		{
			name:         "should allow a SOC member who is not in the auditor group",
			requireGroup: "ssh-admins",
			socGroup:     "ssh-soc",
			auditorGroup: "ssh-auditors",
			mode:         identityPresent,
			groups:       []string{"ssh-soc"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "should allow a SOC member when the auditor group is unconfigured",
			requireGroup: "ssh-admins",
			socGroup:     "ssh-soc",
			auditorGroup: "",
			mode:         identityPresent,
			groups:       []string{"ssh-soc"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "should allow when the caller is in the auditor group",
			requireGroup: "ssh-admins",
			auditorGroup: "ssh-auditors",
			mode:         identityPresent,
			groups:       []string{"ssh-auditors"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "should allow an admin who is not in the auditor group",
			requireGroup: "ssh-admins",
			auditorGroup: "ssh-auditors",
			mode:         identityPresent,
			groups:       []string{"ssh-admins"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "should allow an admin when the auditor group is unconfigured",
			requireGroup: "ssh-admins",
			auditorGroup: "",
			mode:         identityPresent,
			groups:       []string{"ssh-admins"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "should allow an auditor when the admin group is unconfigured",
			requireGroup: "",
			auditorGroup: "ssh-auditors",
			mode:         identityPresent,
			groups:       []string{"ssh-auditors"},
			wantStatus:   http.StatusOK,
			wantReached:  true,
		},
		{
			name:         "should deny a non-admin when the auditor group is unconfigured",
			requireGroup: "ssh-admins",
			auditorGroup: "",
			mode:         identityPresent,
			groups:       []string{"staff"},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when neither group is configured",
			requireGroup: "",
			auditorGroup: "",
			mode:         identityPresent,
			groups:       []string{"staff"},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the caller carries an empty group matching both unset groups",
			requireGroup: "",
			auditorGroup: "",
			mode:         identityPresent,
			groups:       []string{""},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the caller is in neither group",
			requireGroup: "ssh-admins",
			auditorGroup: "ssh-auditors",
			mode:         identityPresent,
			groups:       []string{"staff"},
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the caller has no groups at all",
			requireGroup: "ssh-admins",
			auditorGroup: "ssh-auditors",
			mode:         identityPresent,
			groups:       nil,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when no identity is on the context",
			requireGroup: "ssh-admins",
			auditorGroup: "ssh-auditors",
			mode:         identityAbsent,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should deny when the context identity is nil",
			requireGroup: "ssh-admins",
			auditorGroup: "ssh-auditors",
			mode:         identityNil,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:         "should match the auditor group case-sensitively",
			requireGroup: "ssh-admins",
			auditorGroup: "ssh-auditors",
			mode:         identityPresent,
			groups:       []string{"SSH-Auditors"},
			wantStatus:   http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			guard := NewAuditorAuthMiddleware(adminConfig(tt.requireGroup, tt.socGroup, tt.auditorGroup)).Add()
			status, reached := runAuthMiddleware(t, guard, tt.mode, tt.groups)

			if status != tt.wantStatus {
				t.Errorf("status = %d, want %d", status, tt.wantStatus)
			}
			if reached != tt.wantReached {
				t.Errorf("handler reached = %v, want %v", reached, tt.wantReached)
			}
		})
	}
}

func TestContainsString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		haystack []string
		needle   string
		want     bool
	}{
		{
			name:     "should match when the needle is present",
			haystack: []string{"a", "b", "c"},
			needle:   "b",
			want:     true,
		},
		{
			name:     "should not match when the needle is absent",
			haystack: []string{"a", "b"},
			needle:   "c",
		},
		{
			name:     "should never match an empty needle against an empty entry",
			haystack: []string{""},
			needle:   "",
		},
		{
			name:     "should never match an empty needle",
			haystack: []string{"a"},
			needle:   "",
		},
		{
			name:     "should not match against a nil haystack",
			haystack: nil,
			needle:   "a",
		},
		{
			name:     "should not match against an empty haystack",
			haystack: []string{},
			needle:   "a",
		},
		{
			name:     "should be case-sensitive",
			haystack: []string{"Admins"},
			needle:   "admins",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := containsString(tt.haystack, tt.needle); got != tt.want {
				t.Errorf("containsString(%q, %q) = %v, want %v", tt.haystack, tt.needle, got, tt.want)
			}
		})
	}
}
