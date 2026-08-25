package controller

// The certificate-detail endpoint and the response builders behind it.
// GET /certs/:id arrived with the cert-details work; its authorization and
// not-found paths were covered, but the mapping that fills the panel -- the
// decision record and, for a service certificate, the redemption that
// produced it -- was not. Both are audit data read by people asking "who
// approved this, and where was it used", so a dropped field is a wrong
// answer rather than a cosmetic one.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// detailCertService returns one canned CertificateWithDecision, so the
// decision and retrieval branches of the response builder can be driven
// independently of the database.
type detailCertService struct {
	result service.CertificateWithDecision
	err    error
	gotID  string
}

func (d *detailCertService) ListForIdentity(_ context.Context, _ *service.Identity, _ *string, _ int) ([]service.CertificateWithDecision, *string, error) {
	return nil, nil, nil
}

func (d *detailCertService) GetByID(_ context.Context, id string, _ *service.Identity, _ *config.Config) (service.CertificateWithDecision, error) {
	d.gotID = id
	return d.result, d.err
}

func certDetailRouter(t *testing.T, svc service.CertificateProvider, identity *service.Identity) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())

	// A nil identity means "no session at all", so the context key is left
	// unset -- identityMiddleware(nil) would store a typed nil, which
	// middleware.Identity reports as a present identity.
	auth := gin.HandlerFunc(func(c *gin.Context) { c.Next() })
	if identity != nil {
		auth = identityMiddleware(identity)
	}

	NewCertificateController(&r.RouterGroup, svc, auth, newTestConfig(t))
	return r
}

func TestCertificateDetailHandler_ShouldRenderTheFullDecisionRecord(t *testing.T) {
	t.Parallel()

	decidedAt := time.Now().Add(-6 * time.Hour).UTC().Truncate(time.Second)
	svc := &detailCertService{
		result: service.CertificateWithDecision{
			Certificate: model.Certificate{
				ID:                   "cert-1",
				Type:                 model.CertificateTypeUser,
				SerialNumber:         4242,
				KeyID:                "key-1",
				Principals:           `["alice"]`,
				PublicKeyFingerprint: "SHA256:xyz",
				IssuedAt:             decidedAt,
				ExpiresAt:            decidedAt.Add(8 * time.Hour),
			},
			Decision: &model.CertificateRequestDecision{
				Outcome:         model.CertificateRequestDecisionApproved,
				Subject:         "sub-alice",
				Username:        "alice",
				Email:           "alice@corp.example",
				SourceIP:        "10.9.8.7",
				UserAgent:       "Mozilla/5.0",
				AcceptLanguage:  "en-GB",
				ForwardedFor:    "203.0.113.4, 10.9.8.7",
				Groups:          `["ssh-users","ssh-admins"]`,
				OtherAccounts:   `["alice.admin"]`,
				ServiceAccounts: `["svc-deploy"]`,
				DecidedAt:       decidedAt,
			},
		},
	}

	r := certDetailRouter(t, svc, &service.Identity{Subject: "sub-alice", Username: "alice"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/cert-1", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /certs/cert-1 = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if svc.gotID != "cert-1" {
		t.Errorf("service asked for %q, want the path parameter %q", svc.gotID, "cert-1")
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v, body: %s", err, w.Body.String())
	}
	got := resp.Data

	for _, tc := range []struct {
		field string
		want  any
	}{
		{"id", "cert-1"},
		{"serial_number", float64(4242)},
		{"key_id", "key-1"},
		{"public_key_fingerprint", "SHA256:xyz"},
		{"decided_by_outcome", "approved"},
		{"decided_by_subject", "sub-alice"},
		{"decided_by_username", "alice"},
		{"decided_by_email", "alice@corp.example"},
		{"decided_source_ip", "10.9.8.7"},
		{"decided_user_agent", "Mozilla/5.0"},
		{"decided_accept_language", "en-GB"},
		{"decided_forwarded_for", "203.0.113.4, 10.9.8.7"},
	} {
		if got[tc.field] != tc.want {
			t.Errorf("%s = %v, want %v", tc.field, got[tc.field], tc.want)
		}
	}

	if got["decided_at"] == nil {
		t.Error("decided_at is absent, want the decision's timestamp")
	}

	// The three JSON-encoded columns are decoded rather than passed through
	// as strings; a consumer iterating them would break on either mistake.
	for field, want := range map[string][]string{
		"decided_by_groups":           {"ssh-users", "ssh-admins"},
		"decided_by_other_accounts":   {"alice.admin"},
		"decided_by_service_accounts": {"svc-deploy"},
	} {
		list, ok := got[field].([]any)
		if !ok {
			t.Errorf("%s = %v, want a decoded array", field, got[field])
			continue
		}
		if len(list) != len(want) {
			t.Errorf("%s has %d entries, want %d", field, len(list), len(want))
			continue
		}
		for i := range want {
			if list[i] != want[i] {
				t.Errorf("%s[%d] = %v, want %v", field, i, list[i], want[i])
			}
		}
	}
}

// A service certificate carries the redemption that produced it. That is a
// different question from the decision beside it -- the decision is the
// enrollment approval, possibly months earlier, while the retrieval names
// the machine that actually fetched this certificate.
func TestCertificateDetailHandler_ShouldRenderTheRetrievalForAServiceCertificate(t *testing.T) {
	t.Parallel()

	retrievedAt := time.Now().Add(-15 * time.Minute).UTC().Truncate(time.Second)
	svc := &detailCertService{
		result: service.CertificateWithDecision{
			Certificate: model.Certificate{ID: "cert-svc", Type: model.CertificateTypeService},
			Retrieval: &service.CertificateRetrieval{
				EnrollmentID: "enr-7",
				SourceIP:     "10.1.1.1",
				RetrievedAt:  retrievedAt,
			},
		},
	}

	r := certDetailRouter(t, svc, &service.Identity{Subject: "sub-alice"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/cert-svc", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /certs/cert-svc = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Data["enrollment_id"] != "enr-7" {
		t.Errorf("enrollment_id = %v, want enr-7", resp.Data["enrollment_id"])
	}
	if resp.Data["retrieved_source_ip"] != "10.1.1.1" {
		t.Errorf("retrieved_source_ip = %v, want 10.1.1.1", resp.Data["retrieved_source_ip"])
	}
	if resp.Data["retrieved_at"] == nil {
		t.Error("retrieved_at is absent, want the redemption's timestamp")
	}
}

// A user certificate has no retrieval and may have no decision (a request
// resolved before the decision record existed). Neither absence may be
// filled in with a zero value: a rendered "0001-01-01" decision date is
// worse than a blank one.
func TestCertificateDetailHandler_ShouldOmitAbsentDecisionAndRetrieval(t *testing.T) {
	t.Parallel()

	svc := &detailCertService{
		result: service.CertificateWithDecision{
			Certificate: model.Certificate{ID: "cert-bare", Type: model.CertificateTypeUser},
		},
	}

	r := certDetailRouter(t, svc, &service.Identity{Subject: "sub-alice"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/cert-bare", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /certs/cert-bare = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, field := range []string{"decided_at", "retrieved_at", "decided_by_outcome", "enrollment_id"} {
		if v, present := resp.Data[field]; present && v != nil && v != "" {
			t.Errorf("%s = %v, want absent when there is no decision or retrieval", field, v)
		}
	}
}

// Not-found and not-authorized are deliberately the same 404, so the
// endpoint cannot be used to probe which certificate IDs exist.
func TestCertificateDetailHandler_ShouldReturnNotFoundFromTheService(t *testing.T) {
	t.Parallel()

	svc := &detailCertService{err: &errorresponses.NotFoundError{Resource: "certificate"}}
	r := certDetailRouter(t, svc, &service.Identity{Subject: "sub-alice"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/cert-nope", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("GET /certs/cert-nope = %d, want 404, body: %s", w.Code, w.Body.String())
	}
}

// Without a session there is no identity to scope the lookup to, so the
// handler must refuse before it ever reaches the service.
func TestCertificateDetailHandler_ShouldRejectAnUnauthenticatedCaller(t *testing.T) {
	t.Parallel()

	svc := &detailCertService{}
	r := certDetailRouter(t, svc, nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/cert-1", nil))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("GET /certs/cert-1 with no identity = %d, want 401", w.Code)
	}
	if svc.gotID != "" {
		t.Errorf("service was called with %q, want no call at all", svc.gotID)
	}
}
