package controller

// Test methodology: httptest.ResponseRecorder against fake services, no
// real listener. These cover the web-UI read endpoints added for the
// approval page — the envelope shape they return, the scoping they apply,
// and that they fail closed without a session.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// fakeCertificateService is a test double for service.CertificateProvider.
type fakeCertificateService struct {
	certs      []model.Certificate
	err        error
	gotSubject string
}

func (f *fakeCertificateService) ListForIdentity(_ context.Context, identity *service.Identity, _ *string, _ int) ([]service.CertificateWithDecision, *string, error) {
	f.gotSubject = identity.Subject
	out := make([]service.CertificateWithDecision, 0, len(f.certs))
	for _, c := range f.certs {
		out = append(out, service.CertificateWithDecision{Certificate: c, Decision: nil})
	}
	return out, nil, f.err
}

// mockUserDatabase implements a minimal interface for testing CurrentUserHandler's
// extra fields hydration. It holds the users map indexed by subject.
type mockUserDatabase struct {
	users map[string]model.User
}

// GetUser implements the interface that hydratExtraFields checks for.
// It returns the user by subject from the mock's users map.
func (m *mockUserDatabase) GetUser(subject string, dest *model.User) error {
	if user, ok := m.users[subject]; ok {
		*dest = user
	}
	return nil
}

// identityMiddleware stands in for SessionAuthMiddleware, putting identity
// on the context the way a logged-in session would.
func identityMiddleware(identity *service.Identity) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(middleware.IdentityContextKey, identity)
		c.Next()
	}
}

// decodeEnvelope pulls the data half out of the {data, error} envelope.
func decodeEnvelope(t *testing.T, body []byte, into any) {
	t.Helper()

	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error *string         `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("failed to decode envelope: %v, body: %s", err, body)
	}
	if envelope.Error != nil {
		t.Fatalf("expected no error in the envelope, got %q", *envelope.Error)
	}
	if err := json.Unmarshal(envelope.Data, into); err != nil {
		t.Fatalf("failed to decode envelope data: %v, data: %s", err, envelope.Data)
	}
}

func TestCurrentUserHandler_ShouldReturnTheSessionIdentity(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	identity := &service.Identity{
		Subject:         "sub-alice",
		Username:        "alice",
		Email:           "alice@example.com",
		Groups:          []string{"ssh-users"},
		OtherAccounts:   []string{"alice.adm"},
		ServiceAccounts: []string{"svc-backup"},
	}

	r := gin.New()
	NewUserController(&r.RouterGroup, &config.Config{}, identityMiddleware(identity), &mockUserDatabase{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/me", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got webtypes.CurrentUserResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if got.Username != "alice" {
		t.Errorf("got username %q, want %q", got.Username, "alice")
	}
	if got.Subject != "sub-alice" {
		t.Errorf("got subject %q, want %q", got.Subject, "sub-alice")
	}
	if len(got.Groups) != 1 || got.Groups[0] != "ssh-users" {
		t.Errorf("got groups %v, want [ssh-users]", got.Groups)
	}
	if len(got.OtherAccounts) != 1 || got.OtherAccounts[0] != "alice.adm" {
		t.Errorf("got other accounts %v, want [alice.adm]", got.OtherAccounts)
	}
	if len(got.ServiceAccounts) != 1 || got.ServiceAccounts[0] != "svc-backup" {
		t.Errorf("got service accounts %v, want [svc-backup]", got.ServiceAccounts)
	}
}

// TestCurrentUserHandler_ShouldRenderGroupsAsAnEmptyArray keeps the UI from
// having to handle null: a user in no groups gets [], not null.
func TestCurrentUserHandler_ShouldRenderGroupsAsAnEmptyArray(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	r := gin.New()
	NewUserController(&r.RouterGroup, &config.Config{}, identityMiddleware(&service.Identity{Subject: "sub-alice"}), &mockUserDatabase{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/me", nil))

	if !json.Valid(w.Body.Bytes()) {
		t.Fatalf("response is not valid JSON: %s", w.Body.String())
	}
	if got := w.Body.String(); !strings.Contains(got, `"groups":[]`) {
		t.Errorf("expected groups to render as [], got %s", got)
	}
	if got := w.Body.String(); !strings.Contains(got, `"other_accounts":[]`) {
		t.Errorf("expected other_accounts to render as [], got %s", got)
	}
	if got := w.Body.String(); !strings.Contains(got, `"service_accounts":[]`) {
		t.Errorf("expected service_accounts to render as [], got %s", got)
	}
}

// TestCurrentUserHandler_ShouldRejectWithoutAnIdentityOnContext covers
// currentUserHandler's own guard, bypassing SessionAuthMiddleware (which
// would otherwise abort the request before this handler ever runs) with a
// passthrough that never sets IdentityContextKey — the handler must not
// assume its middleware already caught this.
func TestCurrentUserHandler_ShouldRejectWithoutAnIdentityOnContext(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewUserController(&r.RouterGroup, &config.Config{}, passthrough, &mockUserDatabase{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/me", nil))

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error when no identity is on the context, got %d", gotErrors)
	}
}

// TestCurrentUserHandler_ShouldHydrateExtraFieldsFromTheUsersRow verifies
// extra fields are fetched from the users table and included in the response.
func TestCurrentUserHandler_ShouldHydrateExtraFieldsFromTheUsersRow(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	identity := &service.Identity{
		Subject:  "sub-alice",
		Username: "alice",
		Email:    "alice@example.com",
	}

	// Mock database that returns a user with extra fields.
	mockUserDB := &mockUserDatabase{
		users: map[string]model.User{
			"sub-alice": {
				Subject:  "sub-alice",
				Username: "alice",
				Email:    "alice@example.com",
				// ExtraFields is JSON: {"employee_id": "E-40921", "cost_center": ["CC-7781"]}
				ExtraFields: `{"employee_id":"E-40921","cost_center":["CC-7781"]}`,
			},
		},
	}

	r := gin.New()
	NewUserController(&r.RouterGroup, &config.Config{}, identityMiddleware(identity), mockUserDB)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/me", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got webtypes.CurrentUserResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if got.Extra == nil {
		t.Fatalf("got nil Extra field, expected map")
	}

	// Check scalar extra field.
	if v, ok := got.Extra["employee_id"]; !ok {
		t.Errorf("employee_id not in Extra")
	} else if v != "E-40921" {
		t.Errorf("got employee_id %v, want %q", v, "E-40921")
	}

	// Check list extra field.
	if v, ok := got.Extra["cost_center"]; !ok {
		t.Errorf("cost_center not in Extra")
	} else if list, ok := v.([]any); !ok {
		t.Errorf("cost_center is not a []any, got %T", v)
	} else if len(list) != 1 || list[0] != "CC-7781" {
		t.Errorf("got cost_center %v, want [CC-7781]", list)
	}
}

// TestCurrentUserHandler_ShouldIncludeEmptyExtraWhenNoUserRowExists
// verifies that if the database has no row for the session's subject, the
// extra field is still present but empty.
func TestCurrentUserHandler_ShouldIncludeEmptyExtraWhenNoUserRowExists(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	identity := &service.Identity{
		Subject:  "sub-unknown",
		Username: "unknown",
		Email:    "unknown@example.com",
	}

	mockUserDB := &mockUserDatabase{users: map[string]model.User{}}

	r := gin.New()
	NewUserController(&r.RouterGroup, &config.Config{}, identityMiddleware(identity), mockUserDB)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/me", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got webtypes.CurrentUserResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if got.Extra == nil {
		t.Fatalf("got nil Extra field, expected empty map")
	}
	if len(got.Extra) != 0 {
		t.Errorf("got Extra with %d entries, want empty map", len(got.Extra))
	}
}

// TestCurrentUserHandler_ShouldHandleMalformedExtraFields verifies that
// malformed JSON in ExtraFields does not cause a 500 error — the field
// degrades to empty instead.
func TestCurrentUserHandler_ShouldHandleMalformedExtraFields(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	identity := &service.Identity{
		Subject:  "sub-alice",
		Username: "alice",
		Email:    "alice@example.com",
	}

	mockUserDB := &mockUserDatabase{
		users: map[string]model.User{
			"sub-alice": {
				Subject:     "sub-alice",
				Username:    "alice",
				Email:       "alice@example.com",
				ExtraFields: "{invalid json}",
			},
		},
	}

	r := gin.New()
	NewUserController(&r.RouterGroup, &config.Config{}, identityMiddleware(identity), mockUserDB)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/me", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got webtypes.CurrentUserResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if got.Extra == nil {
		t.Fatalf("got nil Extra field, expected empty map")
	}
	if len(got.Extra) != 0 {
		t.Errorf("got Extra with %d entries, want empty map", len(got.Extra))
	}
}

// TestCurrentUserHandler_ShouldRenderListValuedExtraFieldAsJSONArray verifies
// list-valued extra fields round-trip through JSON correctly.
func TestCurrentUserHandler_ShouldRenderListValuedExtraFieldAsJSONArray(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	identity := &service.Identity{
		Subject:  "sub-alice",
		Username: "alice",
		Email:    "alice@example.com",
	}

	mockUserDB := &mockUserDatabase{
		users: map[string]model.User{
			"sub-alice": {
				Subject:     "sub-alice",
				Username:    "alice",
				Email:       "alice@example.com",
				ExtraFields: `{"groups":["team-a","team-b"]}`,
			},
		},
	}

	r := gin.New()
	NewUserController(&r.RouterGroup, &config.Config{}, identityMiddleware(identity), mockUserDB)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/me", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got webtypes.CurrentUserResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if got.Extra == nil {
		t.Fatalf("got nil Extra field, expected map")
	}

	if v, ok := got.Extra["groups"]; !ok {
		t.Errorf("groups not in Extra")
	} else if list, ok := v.([]any); !ok {
		t.Errorf("groups is not a []any, got %T", v)
	} else if len(list) != 2 || list[0] != "team-a" || list[1] != "team-b" {
		t.Errorf("got groups %v, want [team-a team-b]", list)
	}
}

func TestCertificateListHandler_ShouldReturnTheCallersCertificates(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	issued := time.Now().Add(-time.Hour)
	svc := &fakeCertificateService{certs: []model.Certificate{{
		ID:           "cert-1",
		Type:         model.CertificateTypeUser,
		SerialNumber: 42,
		KeyID:        "alice",
		Principals:   "alice",
		IssuedAt:     issued,
		ExpiresAt:    issued.Add(10 * time.Hour),
	}}}

	r := gin.New()
	NewCertificateController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got webtypes.CertificateListResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if len(got.Certificates) != 1 {
		t.Fatalf("got %d certificates, want 1", len(got.Certificates))
	}
	if got.Certificates[0].SerialNumber != 42 {
		t.Errorf("got serial %d, want 42", got.Certificates[0].SerialNumber)
	}
	if got.NextCursor != nil {
		t.Errorf("got next cursor %v, want nil (only one result)", got.NextCursor)
	}

	// The scoping subject must come from the session, not from anything the
	// caller can influence.
	if svc.gotSubject != "sub-alice" {
		t.Errorf("got scoping subject %q, want %q", svc.gotSubject, "sub-alice")
	}
}

// TestCertificateListHandler_ShouldRenderNoCertificatesAsAnEmptyArray keeps
// the UI from having to handle null for a user who has never had one issued.
func TestCertificateListHandler_ShouldRenderNoCertificatesAsAnEmptyArray(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	r := gin.New()
	NewCertificateController(&r.RouterGroup, &fakeCertificateService{}, identityMiddleware(&service.Identity{Subject: "sub-alice"}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs", nil))

	var got webtypes.CertificateListResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if len(got.Certificates) != 0 {
		t.Errorf("expected 0 certificates, got %d", len(got.Certificates))
	}
	if got.NextCursor != nil {
		t.Errorf("expected next cursor nil, got %v", got.NextCursor)
	}
}

// TestCertificateListHandler_ShouldRejectWithoutAnIdentityOnContext covers
// listHandler's own guard; see the currentUserHandler test above for why
// this needs a passthrough rather than the real SessionAuthMiddleware.
func TestCertificateListHandler_ShouldRejectWithoutAnIdentityOnContext(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertificateController(&r.RouterGroup, &fakeCertificateService{}, passthrough)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs", nil))

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error when no identity is on the context, got %d", gotErrors)
	}
}

func TestCertificateListHandler_ShouldRegisterErrorWhenTheServiceFails(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertificateController(&r.RouterGroup,
		&fakeCertificateService{err: errors.New("simulated failure")},
		identityMiddleware(&service.Identity{Subject: "sub-alice"}))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs", nil))

	if gotErrors != 1 {
		t.Errorf("expected exactly one error to be attached, got %d", gotErrors)
	}
}

// TestDetailHandler_ShouldSurfaceRequestedAndGrantedSeparately is the point
// of the detail endpoint: server config trims what a client asked for, and
// the human approving has to see both sides before deciding.
func TestDetailHandler_ShouldSurfaceRequestedAndGrantedSeparately(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{detail: &service.RequestDetail{
		Request: model.CertificateRequest{
			ID:        "req-1",
			Type:      model.CertificateTypeUser,
			Status:    model.CertificateRequestStatusPending,
			PublicKey: "ssh-ed25519 AAAA test",
			CreatedAt: time.Now(),
		},
		Requested:     service.RequestedOptions{Extensions: []string{"permit-pty", "permit-port-forwarding"}},
		Narrowed:      service.RequestedOptions{Extensions: []string{"permit-pty"}},
		Principals:    []string{"alice"},
		ValidDuration: 10 * time.Hour,
	}}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), passthrough, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/requests/req-1", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got webtypes.RequestDetailResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if len(got.Requested.Extensions) != 2 {
		t.Errorf("got requested extensions %v, want both", got.Requested.Extensions)
	}
	if len(got.Granted.Extensions) != 1 || got.Granted.Extensions[0] != "permit-pty" {
		t.Errorf("got granted extensions %v, want [permit-pty] — the trimmed set must be visible before approval", got.Granted.Extensions)
	}
	if got.ValidSeconds != 36000 {
		t.Errorf("got valid_seconds %d, want 36000", got.ValidSeconds)
	}
	if got.ApprovalURL != "/approve/req-1" {
		t.Errorf("got approval_url %q, want %q", got.ApprovalURL, "/approve/req-1")
	}
	if got.AlreadyClosed {
		t.Error("expected already_closed to be false for a pending request")
	}
}

// TestDetailHandler_ShouldSurfaceDecisionAudit covers newRequestDetailResponse's
// setDecisionFields: once a request has been decided, every Decided* field
// on the response must reflect the decision row, including the JSON-decoded
// list fields (Groups/OtherAccounts/ServiceAccounts).
func TestDetailHandler_ShouldSurfaceDecisionAudit(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	decidedAt := time.Now()
	svc := &fakeCertRequestService{detail: &service.RequestDetail{
		Request: model.CertificateRequest{
			ID:        "req-1",
			Type:      model.CertificateTypeUser,
			Status:    model.CertificateRequestStatusApproved,
			PublicKey: "ssh-ed25519 AAAA test",
			CreatedAt: time.Now(),
		},
		Decision: &model.CertificateRequestDecision{
			Outcome:         model.CertificateRequestDecisionApproved,
			Subject:         "sub-alice",
			Username:        "alice",
			Email:           "alice@example.org",
			Groups:          `["engineering","sre"]`,
			OtherAccounts:   `["alice.other"]`,
			ServiceAccounts: `["svc-backup"]`,
			SourceIP:        "198.51.100.7",
			UserAgent:       "curl/8.0.0",
			AcceptLanguage:  "en-US",
			ForwardedFor:    "198.51.100.7, 10.0.0.1",
			DecidedAt:       decidedAt,
		},
	}}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), passthrough, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/requests/req-1", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got webtypes.RequestDetailResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if got.DecidedByOutcome != "approved" {
		t.Errorf("got DecidedByOutcome %q, want %q", got.DecidedByOutcome, "approved")
	}
	if got.DecidedBySubject != "sub-alice" || got.DecidedByUsername != "alice" || got.DecidedByEmail != "alice@example.org" {
		t.Errorf("got decider identity %q/%q/%q, want %q/%q/%q", got.DecidedBySubject, got.DecidedByUsername, got.DecidedByEmail, "sub-alice", "alice", "alice@example.org")
	}
	if len(got.DecidedByGroups) != 2 || got.DecidedByGroups[0] != "engineering" || got.DecidedByGroups[1] != "sre" {
		t.Errorf("got DecidedByGroups %v, want [engineering sre]", got.DecidedByGroups)
	}
	if len(got.DecidedByOtherAccounts) != 1 || got.DecidedByOtherAccounts[0] != "alice.other" {
		t.Errorf("got DecidedByOtherAccounts %v, want [alice.other]", got.DecidedByOtherAccounts)
	}
	if len(got.DecidedByServiceAccounts) != 1 || got.DecidedByServiceAccounts[0] != "svc-backup" {
		t.Errorf("got DecidedByServiceAccounts %v, want [svc-backup]", got.DecidedByServiceAccounts)
	}
	if got.DecidedSourceIP != "198.51.100.7" || got.DecidedUserAgent != "curl/8.0.0" {
		t.Errorf("got connection context %q/%q, want %q/%q", got.DecidedSourceIP, got.DecidedUserAgent, "198.51.100.7", "curl/8.0.0")
	}
	if got.DecidedAt == nil {
		t.Fatal("expected a non-nil DecidedAt")
	}
}

// TestDetailHandler_ShouldOmitDecisionFieldsForAPendingRequest is the
// counterpart: a request with no Decision yet must not carry any Decided*
// noise on the wire.
func TestDetailHandler_ShouldOmitDecisionFieldsForAPendingRequest(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{detail: &service.RequestDetail{
		Request: model.CertificateRequest{ID: "req-1", Type: model.CertificateTypeUser, Status: model.CertificateRequestStatusPending},
		// Decision left nil.
	}}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), passthrough, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/requests/req-1", nil))

	if got := w.Body.String(); strings.Contains(got, "decided_by") {
		t.Errorf("expected no decided_by* fields for a pending request, got %s", got)
	}
}

// TestDecodeDecisionStringList_ShouldReturnNilOnMalformedJSON covers the
// parse-failure branch directly: malformed JSON in a decision's list column
// (which should never happen, since it's only ever written by newDecision's
// own json.Marshal) must degrade to an empty field, not panic or fail the
// whole response.
func TestDecodeDecisionStringList_ShouldReturnNilOnMalformedJSON(t *testing.T) {
	t.Parallel()

	if got := decodeDecisionStringList("groups", "not valid json"); got != nil {
		t.Errorf("got %v, want nil for malformed JSON", got)
	}
	if got := decodeDecisionStringList("groups", ""); got != nil {
		t.Errorf("got %v, want nil for an empty string", got)
	}
}

// TestDetailHandler_ShouldRejectWithoutAnIdentityOnContext covers
// detailHandler's own guard; see TestCurrentUserHandler_ShouldRejectWithoutAnIdentityOnContext
// for why this needs a passthrough rather than the real SessionAuthMiddleware.
func TestDetailHandler_ShouldRejectWithoutAnIdentityOnContext(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertRequestController(&r.RouterGroup, &fakeCertRequestService{}, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/requests/req-1", nil))

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error when no identity is on the context, got %d", gotErrors)
	}
}

// TestDetailHandler_ShouldNormalizeNilSlicesToEmpty guards the UI contract:
// a request with no principals resolved yet, or options with no extensions,
// must render as [] rather than null (see newRequestDetailResponse and
// newCertificateOptionsResponse).
func TestDetailHandler_ShouldNormalizeNilSlicesToEmpty(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{detail: &service.RequestDetail{
		Request: model.CertificateRequest{ID: "req-1", Type: model.CertificateTypeUser, Status: model.CertificateRequestStatusPending},
		// Principals, Requested.Extensions, and Narrowed.Extensions all left
		// nil on purpose.
	}}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), passthrough, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/requests/req-1", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); !strings.Contains(got, `"principals":[]`) {
		t.Errorf("expected principals to render as [], got %s", got)
	}
	if got := w.Body.String(); strings.Count(got, `"extensions":[]`) != 2 {
		t.Errorf("expected both requested and granted extensions to render as [], got %s", got)
	}
}

// TestDetailHandler_ShouldPropagateAForbiddenFromTheBinding covers a second
// user opening someone else's approval page: they get a refusal on load
// rather than after clicking approve.
func TestDetailHandler_ShouldPropagateAForbiddenFromTheBinding(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{detailErr: &errorresponses.ForbiddenError{Reason: "certificate request belongs to another user"}}

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewCertRequestController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-bob"}), passthrough, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/requests/req-1", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d, body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

// TestWebReadEndpoints_ShouldFailClosedWithoutASession pins that every new
// read endpoint refuses an unauthenticated caller, rather than any of them
// being registered outside the session group by mistake.
func TestWebReadEndpoints_ShouldFailClosedWithoutASession(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	paths := []string{
		"/users/me",
		"/certs",
		"/certs/requests/req-1",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			r := gin.New()
			r.Use(middleware.NewErrorHandlerMiddleware().Add())
			// SessionAuthMiddleware reads the session, which panics if the
			// store middleware isn't registered — initEngine always does, so
			// mirror that here rather than working around it.
			r.Use(sessions.Sessions("ssoossh_session", cookie.NewStore([]byte("test-secret"))))
			sessionAuth := middleware.NewSessionAuthMiddleware(5*time.Minute, time.Hour).Add()

			NewUserController(&r.RouterGroup, &config.Config{}, sessionAuth, &mockUserDatabase{})
			NewCertificateController(&r.RouterGroup, &fakeCertificateService{}, sessionAuth)
			NewCertRequestController(&r.RouterGroup, &fakeCertRequestService{}, sessionAuth, passthrough, nil)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))

			if w.Code != http.StatusUnauthorized {
				t.Errorf("got status %d, want %d for an unauthenticated request", w.Code, http.StatusUnauthorized)
			}
		})
	}
}
