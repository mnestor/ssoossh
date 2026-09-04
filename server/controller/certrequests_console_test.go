package controller

// Test methodology: the same fake CertRequestProvider and bare gin engine
// the rest of certrequests_test.go uses. What is under test here is the
// wiring — that the console body reaches the service as console params,
// that the create response carries the code in its display form, and that
// code submission is a session-authed POST whose three failure modes reach
// the caller intact.

import (
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
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// createRequestBody is the shape every create endpoint answers with, plus
// the console-only fields.
type createRequestBody struct {
	RequestID               string `json:"request_id"`
	EventsURL               string `json:"events_url"`
	ApprovalURL             string `json:"approval_url"`
	UserCode                string `json:"user_code"`
	VerificationURL         string `json:"verification_url"`
	VerificationURLComplete string `json:"verification_url_complete"`
	ExpiresAt               string `json:"expires_at"`
}

func TestCreateConsoleRequestHandler_ShouldReturnTheCodeAndVerificationURLs(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	expires := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	svc := &fakeCertRequestService{
		createRequestID: "req-console",
		createUserCode:  "K7M4QP2X",
		createExpiresAt: expires,
	}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	body := `{"public_key":"ssh-ed25519 AAAA... console","username":"alice"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/console", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got createRequestBody
	decodeEnvelope(t, w.Body.Bytes(), &got)

	// Grouped, because this is the value a human reads off a console
	// screen. The stored and submitted form carries no separator.
	if got.UserCode != "K7M4-QP2X" {
		t.Errorf("got user_code %q, want the display form %q", got.UserCode, "K7M4-QP2X")
	}
	if got.VerificationURL != "/console" {
		t.Errorf("got verification_url %q, want %q", got.VerificationURL, "/console")
	}
	if got.VerificationURLComplete != "/c/K7M4QP2X" {
		t.Errorf("got verification_url_complete %q, want %q", got.VerificationURLComplete, "/c/K7M4QP2X")
	}
	if got.ExpiresAt != expires.Format(time.RFC3339) {
		t.Errorf("got expires_at %q, want %q", got.ExpiresAt, expires.Format(time.RFC3339))
	}
	if got.EventsURL != "/api/certs/requests/req-console/events" {
		t.Errorf("got events_url %q, want the usual shape", got.EventsURL)
	}
}

func TestCreateConsoleRequestHandler_ShouldPassTheConsoleContextToTheService(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createRequestID: "req-console"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	body := `{"public_key":"ssh-ed25519 AAAA... console","username":"alice",` +
		`"hostname":"web01","pam_service":"login","tty":"tty1","remote_host":"198.51.100.7"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/console", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	if svc.gotParams.Type != model.CertificateTypeConsole {
		t.Errorf("got request type %q, want %q", svc.gotParams.Type, model.CertificateTypeConsole)
	}
	for _, field := range []struct {
		name string
		got  string
		want string
	}{
		{name: "Username", got: svc.gotParams.Username, want: "alice"},
		{name: "Hostname", got: svc.gotParams.Hostname, want: "web01"},
		{name: "PAMService", got: svc.gotParams.PAMService, want: "login"},
		{name: "TTY", got: svc.gotParams.TTY, want: "tty1"},
		{name: "RemoteHost", got: svc.gotParams.RemoteHost, want: "198.51.100.7"},
	} {
		if field.got != field.want {
			t.Errorf("%s = %q, want %q", field.name, field.got, field.want)
		}
	}
}

// The context fields are optional, so a client that reads no PAM items
// still works.
func TestCreateConsoleRequestHandler_ShouldAcceptARequestWithNoContext(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createRequestID: "req-console"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	body := `{"public_key":"ssh-ed25519 AAAA... console","username":"alice"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/console", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
}

// Username is required: a console login with no account named is not a
// request anyone can be shown.
func TestCreateConsoleRequestHandler_ShouldRejectABodyWithNoUsername(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodPost, "/certs/console",
		strings.NewReader(`{"public_key":"ssh-ed25519 AAAA... console"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if gotErrors != 1 {
		t.Fatalf("expected exactly one binding error, got %d", gotErrors)
	}
}

// Every other create endpoint has to keep answering exactly as it did, so
// the code fields stay absent rather than empty-but-present.
func TestCreateRequestHandlers_ShouldOmitConsoleFieldsForOtherTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		body string
	}{
		{name: "user", path: "/certs/user", body: `{"public_key":"ssh-ed25519 AAAA... u"}`},
		{name: "service", path: "/certs/service/enroll", body: `{"public_key":"ssh-ed25519 AAAA... s"}`},
		{name: "pam", path: "/certs/pam", body: `{"public_key":"ssh-ed25519 AAAA... p","username":"alice"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gin.SetMode(gin.TestMode)
			svc := &fakeCertRequestService{createRequestID: "req-1"}

			r := gin.New()
			NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
			}
			if body := w.Body.String(); strings.Contains(body, "user_code") ||
				strings.Contains(body, "verification_url") {
				t.Errorf("a %s response carries console fields: %s", tt.name, body)
			}
		})
	}
}

// The PAM endpoint gained the same context fields, which is the half of
// this design that improves the shipped `sudo` flow on its own.
func TestCreatePAMRequestHandler_ShouldPassTheContextToTheService(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{createRequestID: "req-pam"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	body := `{"public_key":"ssh-ed25519 AAAA... sudo","username":"alice",` +
		`"hostname":"web01","pam_service":"sudo","tty":"pts/3"}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/pam", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}
	if svc.gotParams.Hostname != "web01" || svc.gotParams.PAMService != "sudo" || svc.gotParams.TTY != "pts/3" {
		t.Errorf("context did not reach the service: %+v", svc.gotParams)
	}
}

// withIdentity is the session middleware every resolve-code test needs: the
// route is session-authed, so the handler expects an identity in context.
func withIdentity(subject string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(middleware.IdentityContextKey, &service.Identity{Subject: subject, Username: "alice"})
		c.Next()
	}
}

func TestResolveCodeHandler_ShouldReturnTheApprovalURL(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{resolveRequestID: "req-console"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, withIdentity("sub-alice"), passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/resolve-code",
		strings.NewReader(`{"code":"K7M4-QP2X"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got struct {
		RequestID   string `json:"request_id"`
		ApprovalURL string `json:"approval_url"`
	}
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.RequestID != "req-console" {
		t.Errorf("got request_id %q, want %q", got.RequestID, "req-console")
	}
	if got.ApprovalURL != "/approve/req-console" {
		t.Errorf("got approval_url %q, want %q", got.ApprovalURL, "/approve/req-console")
	}
}

// The code reaches the service exactly as typed: normalization is the
// service's job, so the handler must not quietly do a different version of
// it.
func TestResolveCodeHandler_ShouldPassTheCodeThroughUnchanged(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{resolveRequestID: "req-console"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, withIdentity("sub-alice"), passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/resolve-code",
		strings.NewReader(`{"code":"  k7m4 - qp2x "}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if svc.gotResolveCode != "  k7m4 - qp2x " {
		t.Errorf("service saw %q, want the code exactly as submitted", svc.gotResolveCode)
	}
}

// A signed-out caller must never learn whether a code is live, so a route
// reached without an identity refuses before the service is consulted.
func TestResolveCodeHandler_ShouldRefuseWithoutAnIdentity(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{resolveRequestID: "req-console"}

	r := gin.New()
	var gotErrors []error
	r.Use(func(c *gin.Context) {
		c.Next()
		for _, e := range c.Errors {
			gotErrors = append(gotErrors, e.Err)
		}
	})
	// A passthrough that sets no identity, standing in for a session
	// middleware that let the request through without one.
	NewCertRequestController(&r.RouterGroup, svc, passthrough, passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/resolve-code",
		strings.NewReader(`{"code":"K7M4QP2X"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if len(gotErrors) != 1 {
		t.Fatalf("expected exactly one error, got %d", len(gotErrors))
	}
	var unauthorized *errorresponses.UnauthorizedError
	if !errors.As(gotErrors[0], &unauthorized) {
		t.Errorf("got error %v, want an UnauthorizedError", gotErrors[0])
	}
	if svc.gotResolveCode != "" {
		t.Errorf("the service was consulted with %q despite there being no identity", svc.gotResolveCode)
	}
}

// The three failure modes have to reach the browser distinguishable, since
// each sends the user somewhere different.
func TestResolveCodeHandler_ShouldPropagateTheServicesFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		serviceErr error
		wantStatus int
	}{
		{
			name:       "no such code",
			serviceErr: &errorresponses.NotFoundError{Resource: "console login request for that code"},
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "expired",
			serviceErr: &errorresponses.ExpiredError{Resource: "console login request"},
			wantStatus: http.StatusGone,
		},
		{
			name:       "claimed by another session",
			serviceErr: &errorresponses.ForbiddenError{Reason: "certificate request belongs to another user"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "not a well-formed code",
			serviceErr: &errorresponses.InvalidRequestError{Reason: "that is not a valid code"},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gin.SetMode(gin.TestMode)
			svc := &fakeCertRequestService{resolveErr: tt.serviceErr}

			r := gin.New()
			r.Use(middleware.NewErrorHandlerMiddleware().Add())
			NewCertRequestController(&r.RouterGroup, svc, withIdentity("sub-alice"), passthrough, nil)

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/certs/requests/resolve-code",
				strings.NewReader(`{"code":"K7M4QP2X"}`))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d, body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// resolve-code sits beside the ":id" wildcard on the same group, so this
// pins that gin resolves the literal segment rather than treating "resolve-code"
// as a request ID.
func TestResolveCodeHandler_ShouldNotBeShadowedByTheIDRoute(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertRequestService{resolveRequestID: "req-console"}

	r := gin.New()
	NewCertRequestController(&r.RouterGroup, svc, withIdentity("sub-alice"), passthrough, nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/certs/requests/resolve-code",
		strings.NewReader(`{"code":"K7M4QP2X"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d — the literal route did not win", w.Code, http.StatusOK)
	}
	if svc.gotResolveCode == "" {
		t.Error("the resolve handler did not run")
	}
}
