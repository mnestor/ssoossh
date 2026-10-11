package controller

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// newDisplayRouter wires the user controller with levels behind an identity
// middleware and the error handler the real router installs.
func newDisplayRouter(c *config.Config, identity *service.Identity, levels service.DetailLevelProvider) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewUserController(&r.RouterGroup, c, identityMiddleware(identity), &mockUserDatabase{}, levels, func(g *gin.Context) { g.Next() })
	return r
}

func TestCurrentUserHandler_detailLevel(t *testing.T) {
	admin := config.AdminConfig{RequireGroup: "admins"}
	tests := []struct {
		name   string
		levels service.DetailLevelProvider
		groups []string
		want   model.DetailLevel
	}{
		{
			name:   "should report the stored web level when it can be read",
			levels: &stubDetailLevels{levels: service.DetailLevels{Web: model.DetailLevelAdvanced, Email: model.DetailLevelSimplified}},
			groups: nil,
			want:   model.DetailLevelAdvanced,
		},
		{
			name:   "should fall back to the role default when the read fails",
			levels: &stubDetailLevels{readErr: errors.New("database is down")},
			groups: []string{"admins"},
			want:   model.DetailLevelAdvanced,
		},
		{
			name:   "should fall back to simplified for a role-less session when the read fails",
			levels: &stubDetailLevels{readErr: errors.New("database is down")},
			groups: []string{"devs"},
			want:   model.DetailLevelSimplified,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newDisplayRouter(&config.Config{Admin: admin}, &service.Identity{Subject: "sub-1", Groups: tc.groups}, tc.levels)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/me", nil))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}

			var got webtypes.CurrentUserResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			if got.DetailLevel != tc.want {
				t.Errorf("detail_level = %q, want %q", got.DetailLevel, tc.want)
			}
		})
	}
}

func TestUpdateDisplayHandler(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		levels     *stubDetailLevels
		wantStatus int
		wantSet    *model.DetailLevel
	}{
		{
			name:       "should store and return the new level",
			body:       `{"detail_level":"advanced"}`,
			levels:     &stubDetailLevels{},
			wantStatus: http.StatusOK,
			wantSet:    ptrTo(model.DetailLevelAdvanced),
		},
		{
			name:       "should reject a malformed body",
			body:       `{"detail_level":`,
			levels:     &stubDetailLevels{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "should pass on the service's refusal of an unknown level",
			body:       `{"detail_level":"verbose"}`,
			levels:     &stubDetailLevels{saveErr: &errorresponses.InvalidRequestError{Reason: "unknown detail level"}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "should refuse a session with no user record",
			body:       `{"detail_level":"simplified"}`,
			levels:     &stubDetailLevels{saveErr: &errorresponses.ForbiddenError{Reason: "no user record"}},
			wantStatus: http.StatusForbidden,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newDisplayRouter(&config.Config{}, &service.Identity{Subject: "sub-1"}, tc.levels)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/users/me/display", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantSet == nil {
				if tc.levels.webSet != nil {
					t.Errorf("stored %q, want nothing stored", *tc.levels.webSet)
				}
				return
			}
			var got webtypes.DisplayPreferencesResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			if got.DetailLevel != *tc.wantSet {
				t.Errorf("returned %q, want %q", got.DetailLevel, *tc.wantSet)
			}
		})
	}
}

// ptrTo returns a pointer to v, for table fields that are optional.
func ptrTo[T any](v T) *T {
	return &v
}
