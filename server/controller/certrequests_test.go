package controller

// Test methodology: same as ca_test.go — httptest.ResponseRecorder against
// a fake service.CertRequestProvider, no real listener. The create
// handlers and the events handler are tested separately since they're now
// separate requests (see server/controller/certrequests.go's doc comment
// on why: the events endpoint is a real SSE connection, GET-only per spec,
// not glued onto the creating POST).

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// passthrough stands in for a middleware under test elsewhere, so these
// tests exercise the handlers rather than the guards in front of them.
// middleware.CsrfMiddleware and SessionAuthMiddleware have their own tests.
func passthrough(c *gin.Context) { c.Next() }

// fakeCertRequestService is a test double for service.CertRequestProvider.
type fakeCertRequestService struct {
	createRequestID string
	createUserCode  string
	createExpiresAt time.Time
	createErr       error
	gotParams       service.NewCertRequestParams

	resolveRequestID string
	resolveErr       error
	gotResolveCode   string

	waitOutcome service.WaitOutcome
	waitErr     error

	approveErr error
	denyErr    error

	gotApproveIdentity   *service.Identity
	gotApproveDC         service.DecisionContext
	gotApprovalSelection service.ApprovalSelection
	gotDenyIdentity      *service.Identity
	gotDenyDC            service.DecisionContext

	detail    *service.RequestDetail
	detailErr error
}

func (f *fakeCertRequestService) CreateRequest(_ context.Context, p service.NewCertRequestParams) (service.CreatedRequest, error) {
	f.gotParams = p
	if f.createErr != nil {
		return service.CreatedRequest{}, f.createErr
	}
	return service.CreatedRequest{
		ID:        f.createRequestID,
		UserCode:  f.createUserCode,
		ExpiresAt: f.createExpiresAt,
	}, nil
}

func (f *fakeCertRequestService) ResolveUserCode(_ context.Context, submitted string, _ *service.Identity) (string, error) {
	f.gotResolveCode = submitted
	return f.resolveRequestID, f.resolveErr
}

func (f *fakeCertRequestService) Detail(_ context.Context, requestID string, _ *service.Identity) (*service.RequestDetail, error) {
	if f.detailErr != nil {
		return nil, f.detailErr
	}
	if f.detail != nil {
		return f.detail, nil
	}
	return &service.RequestDetail{
		Request: model.CertificateRequest{
			ID:     requestID,
			Type:   model.CertificateTypeUser,
			Status: model.CertificateRequestStatusPending,
		},
	}, nil
}

func (f *fakeCertRequestService) Approve(_ context.Context, _ string, identity *service.Identity, dc service.DecisionContext, selection service.ApprovalSelection) error {
	f.gotApproveIdentity = identity
	f.gotApproveDC = dc
	f.gotApprovalSelection = selection
	return f.approveErr
}

func (f *fakeCertRequestService) Deny(_ context.Context, _ string, identity *service.Identity, dc service.DecisionContext) error {
	f.gotDenyIdentity = identity
	f.gotDenyDC = dc
	return f.denyErr
}

func (f *fakeCertRequestService) Wait(_ context.Context, _ string) (service.WaitOutcome, error) {
	return f.waitOutcome, f.waitErr
}

func TestCreateUserRequestHandler_ShouldReturnEventsAndApprovalURLs(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createRequestID: "req-1"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	body := `{"public_key":"ssh-ed25519 AAAA... test","requested_options":{"extensions":["permit-pty"]}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/user", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got struct {
		RequestID   string `json:"request_id"`
		EventsURL   string `json:"events_url"`
		ApprovalURL string `json:"approval_url"`
	}
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.RequestID != "req-1" {
		t.Errorf("got request_id %q, want %q", got.RequestID, "req-1")
	}
	if got.EventsURL != "/api/certs/requests/req-1/events" {
		t.Errorf("got events_url %q, want %q", got.EventsURL, "/api/certs/requests/req-1/events")
	}
	if got.ApprovalURL != "/approve/req-1" {
		t.Errorf("got approval_url %q, want %q", got.ApprovalURL, "/approve/req-1")
	}

	if svc.gotParams.Type != model.CertificateTypeUser {
		t.Errorf("got request type %q, want %q", svc.gotParams.Type, model.CertificateTypeUser)
	}
	if len(svc.gotParams.RequestedOptions.Extensions) != 1 || svc.gotParams.RequestedOptions.Extensions[0] != "permit-pty" {
		t.Errorf("expected requested_options to round-trip into RequestedOptions.Extensions, got %+v", svc.gotParams.RequestedOptions)
	}
}

func TestCreateUserRequestHandler_ShouldRejectMalformedJSON(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/user", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one binding error, got %d", gotErrors)
	}
}

func TestCreateUserRequestHandler_ShouldRegisterErrorWhenCreateFails(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createErr: errors.New("simulated failure")}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	body := `{"public_key":"ssh-ed25519 AAAA... test"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/user", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when CreateRequest fails, got %d", gotErrors)
	}
}

func TestCreatePAMRequestHandler_ShouldReturnURLsAndRoundTripUsername(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createRequestID: "req-1"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	body := `{"public_key":"ssh-ed25519 AAAA... test","username":"mnestor"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/pam", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got struct {
		RequestID   string `json:"request_id"`
		EventsURL   string `json:"events_url"`
		ApprovalURL string `json:"approval_url"`
	}
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.RequestID != "req-1" {
		t.Errorf("got request_id %q, want %q", got.RequestID, "req-1")
	}

	if svc.gotParams.Type != model.CertificateTypePAM {
		t.Errorf("got request type %q, want %q", svc.gotParams.Type, model.CertificateTypePAM)
	}
	if svc.gotParams.Username != "mnestor" {
		t.Errorf("got Username %q, want %q", svc.gotParams.Username, "mnestor")
	}
}

func TestCreatePAMRequestHandler_ShouldRejectMalformedJSON(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/pam", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one binding error, got %d", gotErrors)
	}
}

func TestCreatePAMRequestHandler_ShouldRegisterErrorWhenCreateFails(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createErr: errors.New("simulated failure")}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	body := `{"public_key":"ssh-ed25519 AAAA... test","username":"mnestor"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/pam", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when CreateRequest fails, got %d", gotErrors)
	}
}

func TestCreateServiceEnrollRequestHandler_ShouldReturnURLs(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createRequestID: "req-1"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	body := `{"public_key":"ssh-ed25519 AAAA... test"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/service/enroll", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got struct {
		RequestID string `json:"request_id"`
	}
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.RequestID != "req-1" {
		t.Errorf("got request_id %q, want %q", got.RequestID, "req-1")
	}
	if svc.gotParams.Type != model.CertificateTypeService {
		t.Errorf("got request type %q, want %q", svc.gotParams.Type, model.CertificateTypeService)
	}
}

func TestCreateServiceEnrollRequestHandler_ShouldRejectMalformedJSON(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/service/enroll", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one binding error, got %d", gotErrors)
	}
}

func TestCreateServiceEnrollRequestHandler_ShouldRegisterErrorWhenCreateFails(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createErr: errors.New("simulated failure")}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	body := `{"public_key":"ssh-ed25519 AAAA... test"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/service/enroll", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when CreateRequest fails, got %d", gotErrors)
	}
}

func TestEventsHandler_ShouldStreamApprovedOutcome(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{
		waitOutcome: service.WaitOutcome{
			Status:      model.CertificateRequestStatusApproved,
			Certificate: "ssh-ed25519-cert-v01@openssh.com AAAA...",
		},
	}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/certs/requests/req-1/events", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("got Content-Type %q, want a text/event-stream prefix", got)
	}
	if !strings.Contains(w.Body.String(), "event:approved") {
		t.Errorf("expected an 'approved' SSE event, got body: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), svc.waitOutcome.Certificate) {
		t.Errorf("expected the certificate in the SSE payload, got body: %s", w.Body.String())
	}
}

// TestEventsHandler_ShouldStreamTheEnrollmentIdentityWithTheCode covers what
// the CLI operator cannot see: the approval happens in someone's browser, so
// the account it was granted for and the code's own expiry have to travel
// with the code or nobody ever learns them.
func TestEventsHandler_ShouldStreamTheEnrollmentIdentityWithTheCode(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	expiresAt := time.Date(2026, 9, 23, 14, 5, 0, 0, time.UTC)
	svc := &fakeCertRequestService{
		waitOutcome: service.WaitOutcome{
			Status:         model.CertificateRequestStatusEnrolled,
			Code:           "enroll-code-1",
			ServiceAccount: "svc-deploy",
			ExpiresAt:      expiresAt,
		},
	}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/certs/requests/req-1/events", nil)
	r.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "event:enrolled") {
		t.Errorf("expected an 'enrolled' SSE event, got body: %s", body)
	}
	if !strings.Contains(body, `"service_account":"svc-deploy"`) {
		t.Errorf("expected the approved service account in the SSE payload, got body: %s", body)
	}
	if !strings.Contains(body, `"expires_at":"2026-09-23T14:05:00Z"`) {
		t.Errorf("expected the code's expiry in the SSE payload, got body: %s", body)
	}
}

// TestEventsHandler_ShouldOmitTheExpiryWhenThereIsNone keeps the zero time
// off the wire: a client reading `expires_at` would otherwise be told every
// approved certificate's enrollment died in the year 1.
func TestEventsHandler_ShouldOmitTheExpiryWhenThereIsNone(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{
		waitOutcome: service.WaitOutcome{
			Status:      model.CertificateRequestStatusApproved,
			Certificate: "ssh-ed25519-cert-v01@openssh.com AAAA...",
		},
	}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/certs/requests/req-1/events", nil)
	r.ServeHTTP(w, req)

	if strings.Contains(w.Body.String(), "expires_at") {
		t.Errorf("expected no expires_at field, got body: %s", w.Body.String())
	}
}

func TestEventsHandler_ShouldRegisterErrorOnWaitFailure(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{waitErr: errors.New("simulated failure")}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/certs/requests/req-1/events", nil)
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when Wait fails, got %d", gotErrors)
	}
}

func TestApproveHandler_ShouldReadIdentityFromContext(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{}

	r := gin.New()
	sessionAuth := func(c *gin.Context) {
		c.Set(middleware.IdentityContextKey, &service.Identity{Username: "alice"})
		c.Next()
	}
	NewCertRequestController(&r.RouterGroup, svc, sessionAuth, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/approve", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got struct {
		Status string `json:"status"`
	}
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Status != "signing" {
		t.Errorf(`got status %q, want "signing"`, got.Status)
	}
}

// should forward the body's service account to the service, and default it
// to empty when the request carries no body at all (user/PAM approvals).
func TestApproveHandler_ShouldForwardTheChosenServiceAccount(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{}

	r := gin.New()
	sessionAuth := func(c *gin.Context) {
		c.Set(middleware.IdentityContextKey, &service.Identity{Username: "alice"})
		c.Next()
	}
	NewCertRequestController(&r.RouterGroup, svc, sessionAuth, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/approve",
		strings.NewReader(`{"service_account":"svc-deploy"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotApprovalSelection.ServiceAccount != "svc-deploy" {
		t.Errorf("got forwarded service account %q, want %q", svc.gotApprovalSelection.ServiceAccount, "svc-deploy")
	}

	svc.gotApprovalSelection.ServiceAccount = "stale"
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/approve", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("body-less approve: got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotApprovalSelection.ServiceAccount != "" {
		t.Errorf("body-less approve forwarded %q, want empty", svc.gotApprovalSelection.ServiceAccount)
	}
}

func TestApproveHandler_ShouldRegisterErrorWhenServiceFails(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{approveErr: errors.New("simulated failure")}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	sessionAuth := func(c *gin.Context) {
		c.Set(middleware.IdentityContextKey, &service.Identity{Username: "alice"})
		c.Next()
	}
	NewCertRequestController(&r.RouterGroup, svc, sessionAuth, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/approve", nil)
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when Approve fails, got %d", gotErrors)
	}
}

func TestApproveHandler_ShouldRejectWithoutAnIdentityOnContext(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	// sessionAuthMiddleware is a passthrough that never sets IdentityContextKey
	// — approveHandler must fail closed rather than assume it's there.
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/approve", nil)
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when no identity is on the context, got %d", gotErrors)
	}
}

func TestDenyHandler_ShouldReturnDeniedStatus(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{}

	r := gin.New()
	sessionAuth := func(c *gin.Context) {
		c.Set(middleware.IdentityContextKey, &service.Identity{Username: "alice"})
		c.Next()
	}
	NewCertRequestController(&r.RouterGroup, svc, sessionAuth, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/deny", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got struct {
		Status string `json:"status"`
	}
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Status != string(model.CertificateRequestStatusDenied) {
		t.Errorf("got status %q, want %q", got.Status, model.CertificateRequestStatusDenied)
	}
}

func TestDenyHandler_ShouldRejectWithoutAnIdentityOnContext(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	// sessionAuthMiddleware is a passthrough that never sets IdentityContextKey
	// — denyHandler must fail closed rather than assume it's there, the same
	// as approveHandler.
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/deny", nil)
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when no identity is on the context, got %d", gotErrors)
	}
}

func TestDenyHandler_ShouldRegisterErrorWhenServiceFails(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{denyErr: errors.New("simulated failure")}

	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	sessionAuth := func(c *gin.Context) {
		c.Set(middleware.IdentityContextKey, &service.Identity{Username: "alice"})
		c.Next()
	}
	NewCertRequestController(&r.RouterGroup, svc, sessionAuth, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/deny", nil)
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when Deny fails, got %d", gotErrors)
	}
}

// TestDecisionContext_ShouldCaptureExactlyTheAllowlistedHeaders is the
// regression test for the deliberate header allowlist on
// service.DecisionContext: it must capture User-Agent/Accept-Language/
// X-Forwarded-For and SourceIP, and nothing else — a Cookie header on the
// same request (which would carry the live session token) must never
// appear anywhere in the result. DecisionContext has no field a Cookie
// value could even be assigned to, so this also guards against someone
// later adding one without updating this test.
func TestDecisionContext_ShouldCaptureExactlyTheAllowlistedHeaders(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/approve", nil)
	c.Request.RemoteAddr = "203.0.113.9:54321"
	c.Request.Header.Set("User-Agent", "curl/8.0.0")
	c.Request.Header.Set("Accept-Language", "en-US")
	c.Request.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	c.Request.Header.Set("Cookie", "session=super-secret-session-token")

	got := decisionContext(c)

	want := service.DecisionContext{
		SourceIP:       "203.0.113.9",
		UserAgent:      "curl/8.0.0",
		AcceptLanguage: "en-US",
		ForwardedFor:   "203.0.113.9, 10.0.0.1",
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestApproveHandler_ShouldForwardIdentityAndConnectionContext asserts the
// controller wiring: approveHandler must pass the session identity and a
// DecisionContext built from the real request (client IP, headers) through
// to Approve, not a zero value.
func TestApproveHandler_ShouldForwardIdentityAndConnectionContext(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{}

	r := gin.New()
	wantIdentity := &service.Identity{Username: "alice", Subject: "sub-1"}
	sessionAuth := func(c *gin.Context) {
		c.Set(middleware.IdentityContextKey, wantIdentity)
		c.Next()
	}
	NewCertRequestController(&r.RouterGroup, svc, sessionAuth, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/approve", nil)
	req.RemoteAddr = "203.0.113.9:54321"
	req.Header.Set("User-Agent", "curl/8.0.0")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotApproveIdentity != wantIdentity {
		t.Errorf("got identity %+v, want the session identity %+v", svc.gotApproveIdentity, wantIdentity)
	}
	if svc.gotApproveDC.SourceIP != "203.0.113.9" {
		t.Errorf("got DecisionContext.SourceIP %q, want %q", svc.gotApproveDC.SourceIP, "203.0.113.9")
	}
	if svc.gotApproveDC.UserAgent != "curl/8.0.0" {
		t.Errorf("got DecisionContext.UserAgent %q, want %q", svc.gotApproveDC.UserAgent, "curl/8.0.0")
	}
}

// TestDenyHandler_ShouldForwardIdentityAndConnectionContext mirrors
// TestApproveHandler_ShouldForwardIdentityAndConnectionContext for the deny
// path.
func TestDenyHandler_ShouldForwardIdentityAndConnectionContext(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{}

	r := gin.New()
	wantIdentity := &service.Identity{Username: "bob", Subject: "sub-2"}
	sessionAuth := func(c *gin.Context) {
		c.Set(middleware.IdentityContextKey, wantIdentity)
		c.Next()
	}
	NewCertRequestController(&r.RouterGroup, svc, sessionAuth, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/deny", nil)
	req.RemoteAddr = "198.51.100.4:1234"
	req.Header.Set("User-Agent", "Mozilla/5.0")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotDenyIdentity != wantIdentity {
		t.Errorf("got identity %+v, want the session identity %+v", svc.gotDenyIdentity, wantIdentity)
	}
	if svc.gotDenyDC.SourceIP != "198.51.100.4" {
		t.Errorf("got DecisionContext.SourceIP %q, want %q", svc.gotDenyDC.SourceIP, "198.51.100.4")
	}
	if svc.gotDenyDC.UserAgent != "Mozilla/5.0" {
		t.Errorf("got DecisionContext.UserAgent %q, want %q", svc.gotDenyDC.UserAgent, "Mozilla/5.0")
	}
}
