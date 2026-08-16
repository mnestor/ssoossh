package controller

// Test methodology: httptest.ResponseRecorder against a fake
// service.HostProvider, matching webapi_test.go's approach. hostCertAuth is
// a passthrough stand-in for the not-yet-implemented HostCertAuthMiddleware
// (see host.go's TODOs) — testing what NewHostController wires, not that
// unbuilt middleware.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/middleware"
)

// fakeHostService is a test double for service.HostProvider.
type fakeHostService struct {
	certificate string
	renewErr    error
	principals  string
	syncErr     error

	gotHostname     string
	gotExistingCert string
	gotPublicKey    string
	gotSyncHostname string
}

func (f *fakeHostService) Renew(_ context.Context, hostname, existingCert, newPublicKey string) (string, error) {
	f.gotHostname = hostname
	f.gotExistingCert = existingCert
	f.gotPublicKey = newPublicKey
	if f.renewErr != nil {
		return "", f.renewErr
	}
	return f.certificate, nil
}

func (f *fakeHostService) SyncPrincipals(_ context.Context, hostname string) (string, error) {
	f.gotSyncHostname = hostname
	if f.syncErr != nil {
		return "", f.syncErr
	}
	return f.principals, nil
}

func newHostTestRouter(svc *fakeHostService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewHostController(&r.RouterGroup, svc, passthrough)
	return r
}

func TestRenewHandler_ShouldReturnTheRenewedCertificate(t *testing.T) {
	t.Parallel()

	svc := &fakeHostService{certificate: "ssh-ed25519-cert-v01@openssh.com AAAA... host"}
	r := newHostTestRouter(svc)

	body, err := json.Marshal(renewHostRequestBody{PublicKey: "ssh-ed25519 AAAA hostkey"})
	if err != nil {
		t.Fatalf("failed to encode request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/certs/host/renew", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotPublicKey != "ssh-ed25519 AAAA hostkey" {
		t.Errorf("Renew got public key %q, want %q", svc.gotPublicKey, "ssh-ed25519 AAAA hostkey")
	}

	var got struct {
		Certificate string `json:"certificate"`
	}
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Certificate != svc.certificate {
		t.Errorf("got certificate %q, want %q", got.Certificate, svc.certificate)
	}
}

func TestRenewHandler_ShouldRejectAMissingPublicKey(t *testing.T) {
	t.Parallel()

	svc := &fakeHostService{}
	r := newHostTestRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/certs/host/renew", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected a binding error for a missing public key, got status %d, body: %s", w.Code, w.Body.String())
	}
}

func TestRenewHandler_ShouldSurfaceAServiceError(t *testing.T) {
	t.Parallel()

	svc := &fakeHostService{renewErr: errors.New("renewal not implemented")}
	r := newHostTestRouter(svc)

	body, err := json.Marshal(renewHostRequestBody{PublicKey: "ssh-ed25519 AAAA hostkey"})
	if err != nil {
		t.Fatalf("failed to encode request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/certs/host/renew", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected the service error to surface, got status %d, body: %s", w.Code, w.Body.String())
	}
}

func TestSyncHandler_ShouldReturnThePrincipalMapping(t *testing.T) {
	t.Parallel()

	svc := &fakeHostService{principals: "alice,bob"}
	r := newHostTestRouter(svc)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/hosts/web-01/sync", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotSyncHostname != "web-01" {
		t.Errorf("SyncPrincipals got hostname %q, want %q", svc.gotSyncHostname, "web-01")
	}

	var got struct {
		Principals string `json:"principals"`
	}
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Principals != "alice,bob" {
		t.Errorf("got principals %q, want %q", got.Principals, "alice,bob")
	}
}

func TestSyncHandler_ShouldSurfaceAServiceError(t *testing.T) {
	t.Parallel()

	svc := &fakeHostService{syncErr: errors.New("host not found")}
	r := newHostTestRouter(svc)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/hosts/unknown/sync", nil))

	if w.Code == http.StatusOK {
		t.Fatalf("expected the service error to surface, got status %d, body: %s", w.Code, w.Body.String())
	}
}
