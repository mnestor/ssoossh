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
	createErr       error
	gotParams       service.NewCertRequestParams

	waitStatus model.CertificateRequestStatus
	waitCert   string
	waitCode   string
	waitErr    error

	approveErr error
	denyErr    error

	detail    *service.RequestDetail
	detailErr error
}

func (f *fakeCertRequestService) CreateRequest(_ context.Context, p service.NewCertRequestParams) (string, error) {
	f.gotParams = p
	return f.createRequestID, f.createErr
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

func (f *fakeCertRequestService) Approve(_ context.Context, _ string, _ *service.Identity) error {
	return f.approveErr
}

func (f *fakeCertRequestService) Deny(_ context.Context, _ string) error {
	return f.denyErr
}

func (f *fakeCertRequestService) Wait(_ context.Context, _ string) (model.CertificateRequestStatus, string, string, error) {
	return f.waitStatus, f.waitCert, f.waitCode, f.waitErr
}

func TestCreateUserRequestHandler_ShouldReturnEventsAndApprovalURLs(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createRequestID: "req-1"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough)

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
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough)

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
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough)

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

func TestEventsHandler_ShouldStreamApprovedOutcome(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{
		waitStatus: model.CertificateRequestStatusApproved,
		waitCert:   "ssh-ed25519-cert-v01@openssh.com AAAA...",
	}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough)

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
	if !strings.Contains(w.Body.String(), svc.waitCert) {
		t.Errorf("expected the certificate in the SSE payload, got body: %s", w.Body.String())
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
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough)

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
	NewCertRequestController(&r.RouterGroup, svc, sessionAuth, passthrough)

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
	NewCertRequestController(&r.RouterGroup, svc, sessionAuth, passthrough)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/approve", nil)
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when Approve fails, got %d", gotErrors)
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
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/deny", nil)
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when Deny fails, got %d", gotErrors)
	}
}
