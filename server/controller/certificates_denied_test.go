package controller

// Test methodology: drive GET /decisions/denied through a gin router with a
// faked CertificateProvider, the same shape the certificate-list tests use.
// What is worth pinning is what the route exists for: it answers with
// denials rather than certificates, it is scoped by the session's identity
// and not by anything the caller sends, its cursor reaches the service
// unaltered, and it coexists with the /certs/:id wildcard registered beside
// it.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// aDenial is one denied decision as the service would hand it over.
func aDenial(id string, decidedAt time.Time) service.DeniedRequest {
	return service.DeniedRequest{
		Type: model.CertificateTypePAM,
		Decision: model.CertificateRequestDecision{
			ID:                   id,
			CertificateRequestID: "req-" + id,
			Outcome:              model.CertificateRequestDecisionDenied,
			Subject:              "sub-alice",
			Username:             "alice",
			SourceIP:             "198.51.100.7",
			ReportedUsername:     "deploy",
			ReportedHostname:     "rack07",
			PAMService:           "sudo",
			TTY:                  "pts/3",
			RemoteHost:           "10.1.2.9",
			Client:               "ssoossh/1.2.0",
			DecidedAt:            decidedAt,
		},
	}
}

func TestDeniedHandler_ShouldReturnTheCallersDenials(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	decidedAt := time.Now().Add(-time.Hour)
	svc := &fakeCertificateService{denials: []service.DeniedRequest{aDenial("dec-1", decidedAt)}}

	r := gin.New()
	NewCertificateController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/decisions/denied", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d, body: %s", w.Code, http.StatusOK, w.Body.String())
	}

	var got webtypes.DeniedRequestListResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if len(got.Denials) != 1 {
		t.Fatalf("got %d denials, want 1", len(got.Denials))
	}
	if got.Denials[0].ID != "dec-1" {
		t.Errorf("got id %q, want the decision's own id %q", got.Denials[0].ID, "dec-1")
	}
	if got.Denials[0].Type != model.CertificateTypePAM {
		t.Errorf("got type %q, want %q", got.Denials[0].Type, model.CertificateTypePAM)
	}
	// The pair that makes a denial identifiable a month later.
	if got.Denials[0].ReportedUsername != "deploy" || got.Denials[0].ReportedHostname != "rack07" {
		t.Errorf("got reported %q@%q, want deploy@rack07",
			got.Denials[0].ReportedUsername, got.Denials[0].ReportedHostname)
	}
}

// The host context is what a row uses to say what was refused, so it has to
// reach the wire rather than stop at the service.
func TestDeniedHandler_ShouldReturnTheHostContextTheRequestClaimed(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertificateService{denials: []service.DeniedRequest{aDenial("dec-1", time.Now())}}

	r := gin.New()
	NewCertificateController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/decisions/denied", nil))

	var got webtypes.DeniedRequestListResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if len(got.Denials) != 1 {
		t.Fatalf("got %d denials, want 1", len(got.Denials))
	}
	d := got.Denials[0]
	if d.PAMService != "sudo" || d.TTY != "pts/3" || d.RemoteHost != "10.1.2.9" || d.Client != "ssoossh/1.2.0" {
		t.Errorf("host context = %q/%q/%q/%q, want the seeded values",
			d.PAMService, d.TTY, d.RemoteHost, d.Client)
	}
}

// The scoping subject must come from the session, never from anything the
// caller can influence: the route has no parameter to widen it.
func TestDeniedHandler_ShouldScopeBySessionIdentity(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertificateService{}

	r := gin.New()
	NewCertificateController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/decisions/denied?subject=sub-bob", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if svc.deniedSubject != "sub-alice" {
		t.Errorf("got scoping subject %q, want the session's %q", svc.deniedSubject, "sub-alice")
	}
}

// Null would make the client handle two shapes for "nothing here".
func TestDeniedHandler_ShouldRenderNoDenialsAsAnEmptyArray(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	NewCertificateController(&r.RouterGroup, &fakeCertificateService{},
		identityMiddleware(&service.Identity{Subject: "sub-alice"}), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/decisions/denied", nil))

	var got webtypes.DeniedRequestListResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)

	if got.Denials == nil {
		t.Error("got a null denials array, want an empty one")
	}
}

func TestDeniedHandler_ShouldPassTheCursorToTheService(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertificateService{}

	r := gin.New()
	NewCertificateController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/decisions/denied?after=dec-9", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if svc.gotDeniedAfter == nil || *svc.gotDeniedAfter != "dec-9" {
		t.Errorf("got cursor %v, want dec-9", svc.gotDeniedAfter)
	}
}

// An absent "after" has to reach the service as nil rather than as a
// pointer to the empty string, which would be looked up as a cursor and
// refused.
func TestDeniedHandler_ShouldSendNoCursorWhenNoneWasAsked(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertificateService{}

	r := gin.New()
	NewCertificateController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/decisions/denied", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	if svc.gotDeniedAfter != nil {
		t.Errorf("got cursor %v, want nil", svc.gotDeniedAfter)
	}
}

func TestDeniedHandler_ShouldRejectWithoutAnIdentityOnContext(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertificateController(&r.RouterGroup, &fakeCertificateService{}, passthrough, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/decisions/denied", nil))

	if gotErrors != 1 {
		t.Fatalf("expected exactly one error when no identity is on the context, got %d", gotErrors)
	}
}

func TestDeniedHandler_ShouldRegisterErrorWhenTheServiceFails(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	var gotErrors int
	r.Use(func(c *gin.Context) {
		c.Next()
		gotErrors = len(c.Errors)
	})
	NewCertificateController(&r.RouterGroup,
		&fakeCertificateService{deniedErr: errors.New("simulated failure")},
		identityMiddleware(&service.Identity{Subject: "sub-alice"}), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/decisions/denied", nil))

	if gotErrors != 1 {
		t.Errorf("expected exactly one error to be attached, got %d", gotErrors)
	}
}

// The reason the route is /decisions/denied and not /certs/denied: /certs/:id
// is registered on the same router, and a segment under it would be read as
// an id. This fails loudly if anyone moves it back.
func TestDeniedHandler_ShouldNotCollideWithTheCertificateDetailRoute(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertificateService{}

	r := gin.New()
	NewCertificateController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/decisions/denied", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d -- the denial route was shadowed", w.Code, http.StatusOK)
	}
	if svc.deniedSubject == "" {
		t.Error("the denial route did not reach ListDeniedForIdentity")
	}
}

func TestParsePageLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want int
	}{
		{name: "should default when the parameter is absent", raw: "", want: 25},
		{name: "should honour a limit within bounds", raw: "10", want: 10},
		{name: "should clamp a limit above the maximum", raw: "5000", want: 100},
		{name: "should default on a limit of zero", raw: "0", want: 25},
		{name: "should default on a negative limit", raw: "-3", want: 25},
		{name: "should default on an unparseable limit", raw: "many", want: 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := parsePageLimit(tt.raw); got != tt.want {
				t.Errorf("parsePageLimit(%q) = %d, want %d", tt.raw, got, tt.want)
			}
		})
	}
}

// The three filters both history endpoints take. What is pinned here is
// the parsing: which values reach the service, and which are dropped
// rather than refused.

func TestCertificateFilter_ShouldPassTheFiltersToTheService(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertificateService{}

	r := gin.New()
	NewCertificateController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs?q=buildbox&type=pam&status=expired", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	want := service.CertificateFilter{Query: "buildbox", Type: "pam", Status: "expired"}
	if svc.gotFilter != want {
		t.Errorf("filter = %+v, want %+v", svc.gotFilter, want)
	}
}

// The denial list takes the same parameters under the same names, so a
// reader who learns one URL knows the other.
func TestCertificateFilter_ShouldPassTheFiltersToTheDenialList(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	svc := &fakeCertificateService{}

	r := gin.New()
	NewCertificateController(&r.RouterGroup, svc, identityMiddleware(&service.Identity{Subject: "sub-alice"}), nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/decisions/denied?q=rack07&type=console", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("got status %d, want %d", w.Code, http.StatusOK)
	}
	want := service.CertificateFilter{Query: "rack07", Type: "console"}
	if svc.gotDeniedFilter != want {
		t.Errorf("filter = %+v, want %+v", svc.gotDeniedFilter, want)
	}
}

// A filter is a narrowing. Answering a typo with a 400 tells somebody
// their own history is broken; answering with an unfiltered list tells
// them their filter did nothing, which is what happened.
func TestCertificateFilter_ShouldDropUnrecognisedValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  service.CertificateFilter
	}{
		{
			name:  "should drop a type that is not a certificate type",
			query: "?type=banana",
			want:  service.CertificateFilter{},
		},
		{
			name:  "should drop a status that is neither live nor expired",
			query: "?status=pending",
			want:  service.CertificateFilter{},
		},
		{
			name:  "should keep the parameters it does recognise beside a bad one",
			query: "?type=banana&q=alice",
			want:  service.CertificateFilter{Query: "alice"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gin.SetMode(gin.TestMode)
			svc := &fakeCertificateService{}
			r := gin.New()
			NewCertificateController(&r.RouterGroup, svc,
				identityMiddleware(&service.Identity{Subject: "sub-alice"}), nil)

			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs"+tt.query, nil))

			if w.Code != http.StatusOK {
				t.Fatalf("got status %d, want %d -- a bad filter is not a refusal", w.Code, http.StatusOK)
			}
			if svc.gotFilter != tt.want {
				t.Errorf("filter = %+v, want %+v", svc.gotFilter, tt.want)
			}
		})
	}
}
