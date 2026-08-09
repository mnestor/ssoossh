package controller

// Test methodology: same as ca_test.go — httptest.ResponseRecorder against
// a fake service.CertRequestProvider, no real listener.

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

// fakeCertRequestService is a test double for service.CertRequestProvider.
type fakeCertRequestService struct {
	createRequestID string
	createErr       error
	gotParams       service.NewCertRequestParams

	waitStatus model.CertificateRequestStatus
	waitCert   string
	waitErr    error

	denyErr error
}

func (f *fakeCertRequestService) CreateRequest(_ context.Context, p service.NewCertRequestParams) (string, error) {
	f.gotParams = p
	return f.createRequestID, f.createErr
}

func (f *fakeCertRequestService) ListPending(_ context.Context) ([]model.CertificateRequest, error) {
	return nil, nil
}

func (f *fakeCertRequestService) Approve(_ context.Context, _ string, _ *service.Identity) (string, error) {
	return "", nil
}

func (f *fakeCertRequestService) Deny(_ context.Context, _ string) error {
	return f.denyErr
}

func (f *fakeCertRequestService) Wait(_ context.Context, _ string) (model.CertificateRequestStatus, string, error) {
	return f.waitStatus, f.waitCert, f.waitErr
}

func TestCreateUserRequestHandler_ShouldStreamApprovedOutcomeOnSameConnection(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{
		createRequestID: "req-1",
		waitStatus:      model.CertificateRequestStatusApproved,
		waitCert:        "ssh-ed25519-cert-v01@openssh.com AAAA...",
	}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, func(c *gin.Context) { c.Next() })

	body := `{"public_key":"ssh-ed25519 AAAA... test","requested_options":{"extensions":["permit-pty"]}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/user", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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
	NewCertRequestController(&r.RouterGroup, svc, func(c *gin.Context) { c.Next() })

	body := `{"public_key":"ssh-ed25519 AAAA... test"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/user", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when CreateRequest fails, got %d", gotErrors)
	}
}

func TestCreateUserRequestHandler_ShouldNotRegisterAStreamByIDRoute(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, func(c *gin.Context) { c.Next() })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/certs/requests/req-1/stream", nil)
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Error("expected no route to exist for GET /certs/requests/:id/stream")
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
	NewCertRequestController(&r.RouterGroup, svc, sessionAuth)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/approve", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
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
	NewCertRequestController(&r.RouterGroup, svc, func(c *gin.Context) { c.Next() })

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/req-1/deny", nil)
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error to be attached when Deny fails, got %d", gotErrors)
	}
}
