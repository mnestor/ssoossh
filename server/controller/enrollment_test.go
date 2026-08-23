package controller

// Test methodology: httptest.ResponseRecorder against a fake
// service.EnrollmentProvider, matching webapi_test.go's approach.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/middleware"
)

// fakeEnrollmentService is a test double for service.EnrollmentProvider.
type fakeEnrollmentService struct {
	certificate string
	err         error
	gotCode     string
	gotSourceIP string
}

func (f *fakeEnrollmentService) Retrieve(_ context.Context, code string, sourceIP string) (string, error) {
	f.gotCode = code
	f.gotSourceIP = sourceIP
	if f.err != nil {
		return "", f.err
	}
	return f.certificate, nil
}

func newEnrollmentTestRouter(svc *fakeEnrollmentService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewEnrollmentController(&r.RouterGroup, svc, nil)
	return r
}

func TestRetrieveHandler_ShouldReturnTheRedeemedCertificate(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{certificate: "ssh-ed25519-cert-v01@openssh.com AAAA... enrolled"}
	r := newEnrollmentTestRouter(svc)

	body, err := json.Marshal(apitypes.RetrieveRequestBody{Code: "enroll-code-1"})
	if err != nil {
		t.Fatalf("failed to encode request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/certs/service/retrieve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotCode != "enroll-code-1" {
		t.Errorf("Retrieve got code %q, want %q", svc.gotCode, "enroll-code-1")
	}

	var got apitypes.RetrieveResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Certificate != svc.certificate {
		t.Errorf("got certificate %q, want %q", got.Certificate, svc.certificate)
	}
}

func TestRetrieveHandler_ShouldRejectAMissingCode(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{}
	r := newEnrollmentTestRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/certs/service/retrieve", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected a binding error for a missing code, got status %d, body: %s", w.Code, w.Body.String())
	}
}

func TestRetrieveHandler_ShouldRejectMalformedJSON(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{}
	r := newEnrollmentTestRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/certs/service/retrieve", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected a binding error for malformed JSON, got status %d, body: %s", w.Code, w.Body.String())
	}
}

func TestRetrieveHandler_ShouldSurfaceAServiceError(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{err: errors.New("enrollment not found")}
	r := newEnrollmentTestRouter(svc)

	body, err := json.Marshal(apitypes.RetrieveRequestBody{Code: "bad-code"})
	if err != nil {
		t.Fatalf("failed to encode request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/certs/service/retrieve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected the service error to surface, got status %d, body: %s", w.Code, w.Body.String())
	}
}
