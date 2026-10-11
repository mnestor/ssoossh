package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// stubNotificationPreferences stands in for NotificationService, so these
// tests cover the HTTP contract rather than the storage behind it.
type stubNotificationPreferences struct {
	settings service.NotificationSettings
	readErr  error

	saved     map[notify.Kind]bool
	saveErr   error
	saveCalls int
}

func (s *stubNotificationPreferences) PreferencesForIdentity(context.Context, *service.Identity) (service.NotificationSettings, error) {
	if s.readErr != nil {
		return service.NotificationSettings{}, s.readErr
	}
	return s.settings, nil
}

func (s *stubNotificationPreferences) SetPreferencesForIdentity(_ context.Context, _ *service.Identity, updates map[notify.Kind]bool) error {
	s.saveCalls++
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = updates
	return nil
}

// stubDetailLevels stands in for DetailLevelService, recording what was set.
type stubDetailLevels struct {
	levels  service.DetailLevels
	readErr error
	saveErr error

	webSet   *model.DetailLevel
	emailSet *model.DetailLevel
}

func (s *stubDetailLevels) Levels(context.Context, *service.Identity) (service.DetailLevels, error) {
	if s.readErr != nil {
		return service.DetailLevels{}, s.readErr
	}
	return s.levels, nil
}

func (s *stubDetailLevels) SetWeb(_ context.Context, _ *service.Identity, level model.DetailLevel) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.webSet = &level
	s.levels.Web = level
	return nil
}

func (s *stubDetailLevels) SetEmail(_ context.Context, _ *service.Identity, level model.DetailLevel) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.emailSet = &level
	s.levels.Email = level
	return nil
}

// newNotificationRouter wires the controller behind a middleware that
// installs identity, mirroring the session auth the real route sits behind.
func newNotificationRouter(t *testing.T, prefs service.NotificationPreferenceProvider, identity *service.Identity) *gin.Engine {
	t.Helper()
	return newNotificationRouterWithLevels(t, prefs, &stubDetailLevels{levels: service.DetailLevels{
		Web: model.DetailLevelSimplified, Email: model.DetailLevelSimplified,
	}}, identity)
}

// newNotificationRouterWithLevels is newNotificationRouter with the detail
// level provider supplied by the test.
func newNotificationRouterWithLevels(t *testing.T, prefs service.NotificationPreferenceProvider, levels service.DetailLevelProvider, identity *service.Identity) *gin.Engine {
	t.Helper()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.NewErrorHandlerMiddleware().Add())

	auth := func(g *gin.Context) {
		if identity == nil {
			handleError(g, &errorresponses.UnauthorizedError{})
			g.Abort()
			return
		}
		g.Set(middleware.IdentityContextKey, identity)
		g.Next()
	}

	NewNotificationController(router.Group("/api"), prefs, levels, auth, func(g *gin.Context) { g.Next() })
	return router
}

func sampleSettings() service.NotificationSettings {
	return service.NotificationSettings{
		MailEnabled: true,
		Address:     "alice@example.com",
		Kinds: []service.KindPreference{
			{Kind: notify.KindServiceEnrollmentCreated, Title: "Created", Description: "when created", Enabled: true},
			{Kind: notify.KindServiceEnrollmentRedeemed, Title: "Redeemed", Description: "when redeemed", Enabled: false},
		},
	}
}

func TestNotificationsHandler_shouldReturnTheCallersPreferences(t *testing.T) {
	prefs := &stubNotificationPreferences{settings: sampleSettings()}
	router := newNotificationRouter(t, prefs, &service.Identity{Subject: "sub-1"})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users/me/notifications", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var envelope struct {
		Data webtypes.NotificationPreferencesResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !envelope.Data.MailEnabled {
		t.Error("mail_enabled is false")
	}
	if envelope.Data.Address != "alice@example.com" {
		t.Errorf("address = %q", envelope.Data.Address)
	}
	if len(envelope.Data.Kinds) != 2 {
		t.Fatalf("got %d kinds, want 2", len(envelope.Data.Kinds))
	}
	if envelope.Data.Kinds[0].Kind != string(notify.KindServiceEnrollmentCreated) {
		t.Errorf("kinds[0].kind = %q", envelope.Data.Kinds[0].Kind)
	}
	if !envelope.Data.Kinds[0].Enabled || envelope.Data.Kinds[1].Enabled {
		t.Error("the enabled flags do not match the service's answer")
	}
}

// An empty list must serialize as [] and not null, or the page's `#each`
// has to defend against a shape the server should never send.
func TestNotificationsHandler_shouldSerializeAnEmptyKindListAsAnArray(t *testing.T) {
	prefs := &stubNotificationPreferences{settings: service.NotificationSettings{}}
	router := newNotificationRouter(t, prefs, &service.Identity{Subject: "sub-1"})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users/me/notifications", nil))

	if !bytes.Contains(rec.Body.Bytes(), []byte(`"kinds":[]`)) {
		t.Errorf("body does not carry an empty array: %s", rec.Body.String())
	}
}

func TestNotificationsHandler_shouldRequireASession(t *testing.T) {
	router := newNotificationRouter(t, &stubNotificationPreferences{}, nil)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users/me/notifications", nil))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestNotificationsHandler_shouldSurfaceAServiceError(t *testing.T) {
	prefs := &stubNotificationPreferences{readErr: &errorresponses.ForbiddenError{Reason: "no user record"}}
	router := newNotificationRouter(t, prefs, &service.Identity{Subject: "sub-1"})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users/me/notifications", nil))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestUpdateNotificationsHandler_shouldSaveTheSubmittedKinds(t *testing.T) {
	prefs := &stubNotificationPreferences{settings: sampleSettings()}
	router := newNotificationRouter(t, prefs, &service.Identity{Subject: "sub-1"})

	body := `{"kinds":{"service_enrollment_created":false}}`
	req := httptest.NewRequest(http.MethodPut, "/api/users/me/notifications", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(prefs.saved) != 1 {
		t.Fatalf("saved %v, want one entry", prefs.saved)
	}
	if enabled, ok := prefs.saved[notify.KindServiceEnrollmentCreated]; !ok || enabled {
		t.Errorf("saved %v, want the submitted value", prefs.saved)
	}
}

// Saving returns the fresh state so the page renders what the server
// actually stored rather than what it hoped it stored.
func TestUpdateNotificationsHandler_shouldReturnThePreferencesAfterSaving(t *testing.T) {
	prefs := &stubNotificationPreferences{settings: sampleSettings()}
	router := newNotificationRouter(t, prefs, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodPut, "/api/users/me/notifications",
		bytes.NewBufferString(`{"kinds":{"service_enrollment_created":false}}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var envelope struct {
		Data webtypes.NotificationPreferencesResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Data.Kinds) != 2 {
		t.Errorf("got %d kinds back from the save", len(envelope.Data.Kinds))
	}
}

func TestUpdateNotificationsHandler_shouldRejectAMalformedBody(t *testing.T) {
	prefs := &stubNotificationPreferences{settings: sampleSettings()}
	router := newNotificationRouter(t, prefs, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodPut, "/api/users/me/notifications", bytes.NewBufferString(`{"kinds":`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if prefs.saveCalls != 0 {
		t.Error("a malformed body reached the service")
	}
}

func TestUpdateNotificationsHandler_shouldRejectAnUnknownKind(t *testing.T) {
	prefs := &stubNotificationPreferences{
		settings: sampleSettings(),
		saveErr:  &errorresponses.InvalidRequestError{Reason: "unknown notification kind"},
	}
	router := newNotificationRouter(t, prefs, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodPut, "/api/users/me/notifications",
		bytes.NewBufferString(`{"kinds":{"nope":true}}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestUpdateNotificationsHandler_shouldRequireASession(t *testing.T) {
	prefs := &stubNotificationPreferences{}
	router := newNotificationRouter(t, prefs, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/users/me/notifications",
		bytes.NewBufferString(`{"kinds":{}}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if prefs.saveCalls != 0 {
		t.Error("an unauthenticated request reached the service")
	}
}

// The email detail level travels with the notification preferences, in
// both directions: GET reports it, and a PUT that names it stores it.
func TestNotificationsHandler_shouldReportTheEmailDetailLevel(t *testing.T) {
	levels := &stubDetailLevels{levels: service.DetailLevels{Web: model.DetailLevelSimplified, Email: model.DetailLevelAdvanced}}
	router := newNotificationRouterWithLevels(t, &stubNotificationPreferences{settings: sampleSettings()}, levels, &service.Identity{Subject: "sub-1"})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users/me/notifications", nil))

	var envelope struct {
		Data webtypes.NotificationPreferencesResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.Data.DetailLevel != model.DetailLevelAdvanced {
		t.Errorf("detail_level = %q, want the email level, not the web one", envelope.Data.DetailLevel)
	}
}

func TestUpdateNotificationsHandler_detailLevel(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		wantStatus    int
		wantEmailSet  *model.DetailLevel
		wantKindsSave int
	}{
		{
			name:          "should store the email level when the body names one",
			body:          `{"kinds":{},"detail_level":"advanced"}`,
			wantStatus:    http.StatusOK,
			wantEmailSet:  ptrTo(model.DetailLevelAdvanced),
			wantKindsSave: 1,
		},
		{
			name:          "should leave the email level alone when the body omits it",
			body:          `{"kinds":{"service_enrollment_created":true}}`,
			wantStatus:    http.StatusOK,
			wantKindsSave: 1,
		},
		{
			name:          "should reject an unknown level before saving any kind",
			body:          `{"kinds":{"service_enrollment_created":true},"detail_level":"verbose"}`,
			wantStatus:    http.StatusBadRequest,
			wantKindsSave: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prefs := &stubNotificationPreferences{settings: sampleSettings()}
			levels := &stubDetailLevels{levels: service.DetailLevels{Web: model.DetailLevelSimplified, Email: model.DetailLevelSimplified}}
			router := newNotificationRouterWithLevels(t, prefs, levels, &service.Identity{Subject: "sub-1"})

			req := httptest.NewRequest(http.MethodPut, "/api/users/me/notifications", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if prefs.saveCalls != tc.wantKindsSave {
				t.Errorf("kind saves = %d, want %d", prefs.saveCalls, tc.wantKindsSave)
			}
			switch {
			case tc.wantEmailSet == nil && levels.emailSet != nil:
				t.Errorf("stored email level %q, want none", *levels.emailSet)
			case tc.wantEmailSet != nil && (levels.emailSet == nil || *levels.emailSet != *tc.wantEmailSet):
				t.Errorf("stored email level %v, want %q", levels.emailSet, *tc.wantEmailSet)
			}
		})
	}
}

// The page heads its kinds from the registry's groups, in the registry's
// order, and places each kind by the group it names.
func TestNotificationsHandler_groups(t *testing.T) {
	settings := sampleSettings()
	settings.Kinds[0].Group = notify.GroupServiceCodes
	router := newNotificationRouter(t, &stubNotificationPreferences{settings: settings}, &service.Identity{Subject: "sub-1"})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/users/me/notifications", nil))

	var envelope struct {
		Data webtypes.NotificationPreferencesResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{name: "should list every registered group", got: len(envelope.Data.Groups), want: len(notify.Groups())},
		{name: "should keep the registry's order", got: envelope.Data.Groups[0].Group, want: string(notify.Groups()[0].Group)},
		{name: "should carry each group's title", got: envelope.Data.Groups[0].Title, want: notify.Groups()[0].Title},
		{name: "should name the group on each kind", got: envelope.Data.Kinds[0].Group, want: string(notify.GroupServiceCodes)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}
