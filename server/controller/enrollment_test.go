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
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// fakeEnrollmentService is a test double for service.EnrollmentProvider.
type fakeEnrollmentService struct {
	certificate string
	retrievals  []model.EnrollmentRetrieval
	// retrievalTotal overrides the reported total, for the truncated case
	// where the log holds more rows than the page returns.
	retrievalTotal int
	enrollments    []service.ServiceEnrollment
	err            error
	gotCode        string
	gotSourceIP    string
	gotRequestID   string
	gotIdentity    *service.Identity

	// gotSetEmailID and gotSetEmail record what the notification-address
	// handler passed through, so a test can assert on the trimming and the
	// path parameter without a database behind it.
	gotSetEmailID string
	gotSetEmail   string

	// holders is what the account-holders handler serves, and gotHolderID
	// records the path parameter it was asked for.
	holders     service.AccountHolders
	gotHolderID string

	// gotExpireID and gotExpireReason record the expiry the handler asked
	// for, which is the whole of what that handler does.
	gotExpireID     string
	gotExpireReason string
}

func (f *fakeEnrollmentService) Retrieve(_ context.Context, code string, sourceIP string) (string, error) {
	f.gotCode = code
	f.gotSourceIP = sourceIP
	if f.err != nil {
		return "", f.err
	}
	return f.certificate, nil
}

func (f *fakeEnrollmentService) ListRetrievals(_ context.Context, requestID string, identity *service.Identity) (service.RetrievalLog, error) {
	f.gotRequestID = requestID
	f.gotIdentity = identity
	if f.err != nil {
		return service.RetrievalLog{}, f.err
	}
	total := f.retrievalTotal
	if total == 0 {
		total = len(f.retrievals)
	}
	return service.RetrievalLog{Retrievals: f.retrievals, Total: total}, nil
}

func (f *fakeEnrollmentService) ListForIdentity(_ context.Context, identity *service.Identity) ([]service.ServiceEnrollment, error) {
	f.gotIdentity = identity
	if f.err != nil {
		return nil, f.err
	}
	return f.enrollments, nil
}

func (f *fakeEnrollmentService) ListForAdmin(_ context.Context, _ *service.Identity, _ service.AdminListParams) (service.AdminEnrollmentList, error) {
	if f.err != nil {
		return service.AdminEnrollmentList{}, f.err
	}
	return service.AdminEnrollmentList{}, nil
}

func (f *fakeEnrollmentService) GetEnrollmentDetail(_ context.Context, _ string, _ *service.Identity) (service.AdminEnrollmentDetail, error) {
	if f.err != nil {
		return service.AdminEnrollmentDetail{}, f.err
	}
	return service.AdminEnrollmentDetail{}, nil
}

func (f *fakeEnrollmentService) ListAccountHolders(_ context.Context, id string, identity *service.Identity) (service.AccountHolders, error) {
	f.gotHolderID, f.gotIdentity = id, identity
	if f.err != nil {
		return service.AccountHolders{}, f.err
	}
	return f.holders, nil
}

func (f *fakeEnrollmentService) ExpireForIdentity(_ context.Context, id string, identity *service.Identity, reason string) error {
	f.gotExpireID, f.gotExpireReason, f.gotIdentity = id, reason, identity
	return f.err
}

func (f *fakeEnrollmentService) SetNotificationEmail(_ context.Context, id string, identity *service.Identity, address string) error {
	f.gotSetEmailID, f.gotSetEmail, f.gotIdentity = id, address, identity
	return f.err
}

func (f *fakeEnrollmentService) Reassign(_ context.Context, _ string, _ string, _ string, _ *service.Identity) error {
	return f.err
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

// The count is what lets the panel say it is showing a slice rather than
// implying the page is the whole history.
func TestRetrievalsHandler_ShouldReportTheTotalAlongsideThePage(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{
		retrievals: []model.EnrollmentRetrieval{
			{RetrievedAt: time.Now(), SourceIP: "203.0.113.9", CertificateSerial: 42, Succeeded: true},
		},
		retrievalTotal: 8760,
	}
	r := newRetrievalsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodGet, "/certs/requests/req-1/retrievals", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var got webtypes.EnrollmentRetrievalsResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Total != 8760 {
		t.Errorf("got total %d, want 8760", got.Total)
	}
	if len(got.Retrievals) != 1 {
		t.Errorf("got %d rows, want the page the service returned", len(got.Retrievals))
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

// newEnrollmentsTestRouter wires the enrollment-list route behind a
// middleware that injects identity, mirroring what sessionAuth guarantees in
// production.
func newEnrollmentsTestRouter(svc *fakeEnrollmentService, identity *service.Identity) *gin.Engine {
	return newRetrievalsTestRouter(svc, identity)
}

// sampleServiceEnrollment is one fully-populated enrollment as the service
// layer hands it over, code included — which is exactly what the response
// must not carry.
func sampleServiceEnrollment() service.ServiceEnrollment {
	requestID := "req-1"
	certDuration := int64(3600)
	redeemedAt := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	lastRetrievedAt := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	return service.ServiceEnrollment{
		Enrollment: model.Enrollment{
			ID:                         "enr-1",
			Code:                       "super-secret-code",
			PublicKey:                  "ssh-ed25519 AAAA svc",
			KeyID:                      "svc-deploy@example.org",
			CertificateRequestID:       &requestID,
			CreatedAt:                  time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC),
			ExpiresAt:                  time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC),
			CertificateDurationSeconds: &certDuration,
			RedeemedAt:                 &redeemedAt,
		},
		Principals:      []string{"svc-deploy"},
		Options:         service.RequestedOptions{Extensions: []string{"permit-pty"}},
		Fingerprint:     "SHA256:abc123",
		RetrievalCount:  7,
		LastRetrievedAt: &lastRetrievedAt,
	}
}

// The address is trimmed server-side, so the handler has to pass the raw
// input through and echo back what the service actually stored — a page that
// assumed its own input would show whitespace the database does not hold.
func TestSetNotificationEmailHandler_ShouldPassTheAddressThroughAndEchoTheTrimmedValue(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{}
	r := newEnrollmentsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	body, err := json.Marshal(webtypes.SetNotificationEmailRequestBody{NotificationEmail: "  deploys@example.com  "})
	if err != nil {
		t.Fatalf("failed to encode request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPatch, "/certs/service/enrollments/enr-1/notification-email", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotSetEmailID != "enr-1" {
		t.Errorf("SetNotificationEmail got enrollment %q, want %q", svc.gotSetEmailID, "enr-1")
	}

	var got webtypes.SetNotificationEmailRequestBody
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.NotificationEmail != "deploys@example.com" {
		t.Errorf("echoed %q, want the trimmed address", got.NotificationEmail)
	}
}

// An empty body is how the page clears the address, so it must reach the
// service rather than being rejected as a missing field.
func TestSetNotificationEmailHandler_ShouldForwardAnEmptyAddressAsAClear(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{}
	r := newEnrollmentsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodPatch, "/certs/service/enrollments/enr-1/notification-email",
		bytes.NewReader([]byte(`{"notification_email":""}`)))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotSetEmail != "" {
		t.Errorf("SetNotificationEmail got %q, want the empty clear", svc.gotSetEmail)
	}
}

// A service-layer refusal (not a holder, unknown enrollment, bad address)
// must reach the caller as its own status rather than a 200.
func TestSetNotificationEmailHandler_ShouldSurfaceAServiceError(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{err: &errorresponses.ForbiddenError{Reason: "not a holder"}}
	r := newEnrollmentsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodPatch, "/certs/service/enrollments/enr-1/notification-email",
		bytes.NewReader([]byte(`{"notification_email":"deploys@example.com"}`)))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("got status %d, want %d, body: %s", w.Code, http.StatusForbidden, w.Body.String())
	}
}

func TestEnrollmentsHandler_ShouldReturnTheApprovedEnrollments(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{enrollments: []service.ServiceEnrollment{sampleServiceEnrollment()}}
	r := newEnrollmentsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodGet, "/certs/service/enrollments", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got webtypes.ServiceEnrollmentsResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if len(got.Enrollments) != 1 {
		t.Fatalf("got %d enrollments, want 1", len(got.Enrollments))
	}
	if got.Enrollments[0].ID != "enr-1" {
		t.Errorf("got enrollment ID %q, want %q", got.Enrollments[0].ID, "enr-1")
	}
}

// The whole reason this endpoint exists is that the code cannot be shown.
// A field added later that happens to carry it would be a silent regression
// from "printed once" to "readable from any authenticated browser".
func TestEnrollmentsHandler_ShouldNeverReturnTheCode(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{enrollments: []service.ServiceEnrollment{sampleServiceEnrollment()}}
	r := newEnrollmentsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodGet, "/certs/service/enrollments", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if bytes.Contains(w.Body.Bytes(), []byte("super-secret-code")) {
		t.Errorf("the enrollment code reached the wire, body: %s", w.Body.String())
	}
}

func TestEnrollmentsHandler_ShouldReportTheCertificateLifetime(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{enrollments: []service.ServiceEnrollment{sampleServiceEnrollment()}}
	r := newEnrollmentsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodGet, "/certs/service/enrollments", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var got webtypes.ServiceEnrollmentsResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Enrollments[0].CertificateValidSeconds == nil {
		t.Fatalf("expected a certificate lifetime, got nil")
	}
	if *got.Enrollments[0].CertificateValidSeconds != 3600 {
		t.Errorf("got certificate lifetime %d, want 3600", *got.Enrollments[0].CertificateValidSeconds)
	}
}

// A row written before the code and certificate lifetimes were split has no
// stored duration, and reporting the code's window in its place would
// misstate what a redemption hands out.
func TestEnrollmentsHandler_ShouldOmitTheLifetimeForALegacyRow(t *testing.T) {
	t.Parallel()

	enrollment := sampleServiceEnrollment()
	enrollment.Enrollment.CertificateDurationSeconds = nil
	svc := &fakeEnrollmentService{enrollments: []service.ServiceEnrollment{enrollment}}
	r := newEnrollmentsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodGet, "/certs/service/enrollments", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var got webtypes.ServiceEnrollmentsResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Enrollments[0].CertificateValidSeconds != nil {
		t.Errorf("got certificate lifetime %d, want it omitted", *got.Enrollments[0].CertificateValidSeconds)
	}
}

func TestEnrollmentsHandler_ShouldReturnAnEmptyListAsAnEmptyArray(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{}
	r := newEnrollmentsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodGet, "/certs/service/enrollments", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !bytes.Contains(w.Body.Bytes(), []byte(`"enrollments":[]`)) {
		t.Errorf("expected an empty array, not null, body: %s", w.Body.String())
	}
}

func TestEnrollmentsHandler_ShouldFailClosedWithoutAnIdentity(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{}
	r := newEnrollmentsTestRouter(svc, nil)

	req := httptest.NewRequest(http.MethodGet, "/certs/service/enrollments", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestEnrollmentsHandler_ShouldSurfaceAServiceError(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{err: errors.New("nope")}
	r := newEnrollmentsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	req := httptest.NewRequest(http.MethodGet, "/certs/service/enrollments", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		t.Fatalf("expected the service error to surface, got status %d", w.Code)
	}
}

// The holders route answers "who else has this code", which the enrollment
// row cannot: a code belongs to its service account rather than to whoever
// approved it.
func TestHoldersHandler_ShouldReturnTheAccountsKnownHolders(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{holders: service.AccountHolders{
		ServiceAccount: "svc-deploy",
		Holders: []service.AccountHolder{
			{UserID: "u-alice", Username: "alice", Name: "Alice Ashworth", Email: "alice@example.com"},
			{UserID: "u-mallory", Username: "mallory", Disabled: true},
		},
	}}
	r := newRetrievalsTestRouter(svc, &service.Identity{Subject: "sub-alice"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/service/enrollments/enr-1/holders", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET holders = %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotHolderID != "enr-1" {
		t.Errorf("service was asked for %q, want the path parameter enr-1", svc.gotHolderID)
	}

	var resp struct {
		Data webtypes.AccountHoldersResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v, body: %s", err, w.Body.String())
	}
	if resp.Data.ServiceAccount != "svc-deploy" {
		t.Errorf("service_account = %q, want svc-deploy", resp.Data.ServiceAccount)
	}
	if len(resp.Data.Holders) != 2 {
		t.Fatalf("holders = %+v, want both rows", resp.Data.Holders)
	}
	if !resp.Data.Holders[1].Disabled {
		t.Error("the disabled holder was reported as active")
	}
}

// Holders is validate:"required" and generates as a non-optional array, so
// null there is what stops the panel rendering at all.
func TestHoldersHandler_ShouldRenderNoHoldersAsAnArray(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{holders: service.AccountHolders{ServiceAccount: "svc-orphan"}}
	r := newRetrievalsTestRouter(svc, &service.Identity{Subject: "sub-alice"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/service/enrollments/enr-1/holders", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET holders = %d, want %d", w.Code, http.StatusOK)
	}
	if !strings.Contains(w.Body.String(), `"holders":[]`) {
		t.Errorf("body = %s, want an empty array rather than null", w.Body.String())
	}
}

func TestHoldersHandler_ShouldRejectARequestWithNoIdentity(t *testing.T) {
	t.Parallel()

	r := newRetrievalsTestRouter(&fakeEnrollmentService{}, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/service/enrollments/enr-1/holders", nil))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("GET holders without a session = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// The handler's whole job is to carry the path parameter, the caller's
// identity and the reason through to the service, and to refuse a request
// carrying no session at all. Everything it could get wrong is one of those.

func TestExpireHandler_ShouldPassTheEnrollmentAndReasonThrough(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{}
	r := newRetrievalsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/certs/service/enrollments/enr-1/expire",
		strings.NewReader(`{"reason":"job decommissioned"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("PATCH expire = %d (%s), want %d", w.Code, w.Body.String(), http.StatusOK)
	}
	if svc.gotExpireID != "enr-1" {
		t.Errorf("enrollment id = %q, want enr-1", svc.gotExpireID)
	}
	if svc.gotExpireReason != "job decommissioned" {
		t.Errorf("reason = %q, want the one the caller sent", svc.gotExpireReason)
	}
}

func TestExpireHandler_ShouldRejectARequestWithNoIdentity(t *testing.T) {
	t.Parallel()

	r := newRetrievalsTestRouter(&fakeEnrollmentService{}, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/certs/service/enrollments/enr-1/expire",
		strings.NewReader(`{"reason":"whatever"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("PATCH expire without a session = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// An absent body is a missing reason, which is the service's call to make —
// the handler must reach it rather than answering 400 on the empty read.
func TestExpireHandler_ShouldReachTheServiceWithAnEmptyBody(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{}
	r := newRetrievalsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPatch, "/certs/service/enrollments/enr-1/expire", nil))

	if svc.gotExpireID != "enr-1" {
		t.Errorf("enrollment id = %q, want the service to have been asked anyway", svc.gotExpireID)
	}
}

func TestExpireHandler_ShouldSurfaceARefusalFromTheService(t *testing.T) {
	t.Parallel()

	svc := &fakeEnrollmentService{err: &errorresponses.ForbiddenError{Reason: "not yours"}}
	r := newRetrievalsTestRouter(svc, &service.Identity{Subject: "sub-1"})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPatch, "/certs/service/enrollments/enr-1/expire",
		strings.NewReader(`{"reason":"trying it on"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("PATCH expire on someone else's code = %d, want %d", w.Code, http.StatusForbidden)
	}
}
