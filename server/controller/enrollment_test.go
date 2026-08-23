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
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// fakeEnrollmentService is a test double for service.EnrollmentProvider.
type fakeEnrollmentService struct {
	certificate  string
	retrievals   []model.EnrollmentRetrieval
	err          error
	gotCode      string
	gotSourceIP  string
	gotRequestID string
	gotIdentity  *service.Identity
}

func (f *fakeEnrollmentService) Retrieve(_ context.Context, code string, sourceIP string) (string, error) {
	f.gotCode = code
	f.gotSourceIP = sourceIP
	if f.err != nil {
		return "", f.err
	}
	return f.certificate, nil
}

func (f *fakeEnrollmentService) ListRetrievals(_ context.Context, requestID string, identity *service.Identity) ([]model.EnrollmentRetrieval, error) {
	f.gotRequestID = requestID
	f.gotIdentity = identity
	if f.err != nil {
		return nil, f.err
	}
	return f.retrievals, nil
}

func newEnrollmentTestRouter(svc *fakeEnrollmentService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewEnrollmentController(&r.RouterGroup, svc, nil, passthrough)
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

// newRetrievalsTestRouter wires the retrievals route behind a middleware
// that injects identity, mirroring what sessionAuth guarantees in
// production.
func newRetrievalsTestRouter(svc *fakeEnrollmentService, identity *service.Identity) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewEnrollmentController(&r.RouterGroup, svc, nil, func(c *gin.Context) {
		if identity != nil {
			c.Set(middleware.IdentityContextKey, identity)
		}
		c.Next()
	})
	return r
}

func TestRetrievalsHandler_ShouldReturnTheLogNewestFirst(t *testing.T) {
	t.Parallel()

	retrievedAt := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	svc := &fakeEnrollmentService{retrievals: []model.EnrollmentRetrieval{
		{RetrievedAt: retrievedAt, SourceIP: "203.0.113.9", CertificateSerial: 42, Succeeded: true},
	}}
	r := newRetrievalsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodGet, "/certs/requests/req-1/retrievals", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotRequestID != "req-1" {
		t.Errorf("ListRetrievals got request ID %q, want %q", svc.gotRequestID, "req-1")
	}

	var got webtypes.EnrollmentRetrievalsResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if len(got.Retrievals) != 1 {
		t.Fatalf("got %d retrievals, want 1", len(got.Retrievals))
	}
	if got.Retrievals[0].SourceIP != "203.0.113.9" || got.Retrievals[0].CertificateSerial != 42 || !got.Retrievals[0].Succeeded {
		t.Errorf("retrieval row does not match: %+v", got.Retrievals[0])
	}
}

func TestRetrievalsHandler_ShouldReturnAnEmptyLogAsAnEmptyArray(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{}
	r := newRetrievalsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodGet, "/certs/requests/req-1/retrievals", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"retrievals":[]`)) {
		t.Errorf("expected an empty array, not null, body: %s", w.Body.String())
	}
}

func TestRetrievalsHandler_ShouldSurfaceAServiceError(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{err: errors.New("nope")}
	r := newRetrievalsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodGet, "/certs/requests/req-1/retrievals", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected the service error to surface, got status %d", w.Code)
	}
}

func TestRetrievalsHandler_ShouldFailClosedWithoutAnIdentity(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{}
	r := newRetrievalsTestRouter(svc, nil)

	req := httptest.NewRequest(http.MethodGet, "/certs/requests/req-1/retrievals", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusUnauthorized)
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

// newEnrollmentTestRouterRateLimited registers the retrieve route the way
// bootstrap/router.go does in production: behind a middleware that reads the
// enrollment code out of the request body to key a per-code rate limit.
func newEnrollmentTestRouterRateLimited(svc *fakeEnrollmentService, seen *string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	keyed := func(c *gin.Context) {
		*seen = ExtractEnrollmentCodeForRateLimit(c)
		c.Next()
	}
	NewEnrollmentController(&r.RouterGroup, svc, keyed, passthrough)
	return r
}

// The bug this pins: ExtractEnrollmentCodeForRateLimit drains the request
// body via gin's GetRawData, which does not buffer or restore it. With the
// per-code rate limiter enabled, the handler's ShouldBindJSON then read an
// empty stream and every retrieval failed with a 500 carrying io.EOF —
// `service retrieve` could not redeem a code at all on a server with the
// rate limit configured.
func TestRetrieveHandler_ShouldStillBindTheBodyBehindTheCodeRateLimiter(t *testing.T) {
	svc := &fakeEnrollmentService{certificate: "ssh-ed25519-cert-v01@openssh.com AAAA rate-limited"}
	var seen string
	r := newEnrollmentTestRouterRateLimited(svc, &seen)

	body, err := json.Marshal(apitypes.RetrieveRequestBody{Code: "enroll-code-123"})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/certs/service/retrieve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	if svc.gotCode != "enroll-code-123" {
		t.Errorf("handler saw code %q, want the code the rate limiter had already read", svc.gotCode)
	}
}

// The rate limiter still has to get the code, or it degrades to keying every
// request the same and stops limiting per code.
func TestExtractEnrollmentCodeForRateLimit_ShouldReadTheCode(t *testing.T) {
	svc := &fakeEnrollmentService{certificate: "cert"}
	var seen string
	r := newEnrollmentTestRouterRateLimited(svc, &seen)

	body, err := json.Marshal(apitypes.RetrieveRequestBody{Code: "enroll-code-123"})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/certs/service/retrieve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(httptest.NewRecorder(), req)

	if seen != "enroll-code-123" {
		t.Errorf("rate limiter read %q, want %q", seen, "enroll-code-123")
	}
}

// A malformed body must still reach the handler, so the caller is told what
// is actually wrong with their JSON. Before the restore they got "EOF"
// instead, which describes the drained stream rather than anything the
// caller sent. The status itself is whatever the handler already produced
// for malformed JSON without the rate limiter — see
// TestRetrieveHandler_ShouldRejectMalformedJSON.
func TestRetrieveHandler_ShouldReportTheRealJSONErrorBehindTheCodeRateLimiter(t *testing.T) {
	svc := &fakeEnrollmentService{certificate: "cert"}
	var seen string
	r := newEnrollmentTestRouterRateLimited(svc, &seen)

	req := httptest.NewRequest(http.MethodPost, "/certs/service/retrieve", bytes.NewReader([]byte("{not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected a binding error for malformed JSON, got status %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "EOF") {
		t.Errorf("error still reports a drained body rather than the caller's JSON: %s", w.Body.String())
	}
}
