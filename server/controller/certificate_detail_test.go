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
	"strconv"
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
		// A string, not a number: see
		// TestCertificateDetailHandler_ShouldWriteTheSerialAsAnExactString.
		{"serial_number", "4242"},
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

// The two option columns on the certificate row are what the certificate
// actually grants on the far side, and the detail page states them. They are
// stored JSON-encoded, so the endpoint has to decode them: handing the
// browser a string of JSON would put the parse in the wrong place.
func TestCertificateDetailHandler_ShouldDecodeTheIssuedOptions(t *testing.T) {
	t.Parallel()

	svc := &detailCertService{
		result: service.CertificateWithDecision{
			Certificate: model.Certificate{
				ID:              "cert-opts",
				Type:            model.CertificateTypeUser,
				Extensions:      `["permit-pty","permit-agent-forwarding"]`,
				CriticalOptions: `{"force-command":"/usr/bin/backup"}`,
			},
		},
	}

	r := certDetailRouter(t, svc, &service.Identity{Subject: "sub-alice"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/cert-opts", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /certs/cert-opts = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Extensions      []string          `json:"extensions"`
			CriticalOptions map[string]string `json:"critical_options"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v, body: %s", err, w.Body.String())
	}

	want := []string{"permit-pty", "permit-agent-forwarding"}
	if len(resp.Data.Extensions) != len(want) {
		t.Fatalf("extensions = %v, want %v", resp.Data.Extensions, want)
	}
	for i := range want {
		if resp.Data.Extensions[i] != want[i] {
			t.Errorf("extensions[%d] = %q, want %q", i, resp.Data.Extensions[i], want[i])
		}
	}

	if got := resp.Data.CriticalOptions["force-command"]; got != "/usr/bin/backup" {
		t.Errorf("critical_options[force-command] = %q, want %q", got, "/usr/bin/backup")
	}
}

// An empty column is the common case -- a certificate with no critical
// options -- and must not become a null or a zero value the page renders as
// a field it cannot explain.
func TestCertificateDetailHandler_ShouldOmitEmptyIssuedOptions(t *testing.T) {
	t.Parallel()

	svc := &detailCertService{
		result: service.CertificateWithDecision{
			Certificate: model.Certificate{ID: "cert-bare-opts", Type: model.CertificateTypeUser},
		},
	}

	r := certDetailRouter(t, svc, &service.Identity{Subject: "sub-alice"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/cert-bare-opts", nil))

	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	for _, field := range []string{"extensions", "critical_options"} {
		if v, present := resp.Data[field]; present {
			t.Errorf("%s = %v, want absent when the column is empty", field, v)
		}
	}
}

// A column that will not decode is a corrupt audit row. The rest of the
// record is still the answer someone came for, so the field drops out and
// the request succeeds rather than 500ing over it.
func TestCertificateDetailHandler_ShouldTolerateUndecodableIssuedOptions(t *testing.T) {
	t.Parallel()

	svc := &detailCertService{
		result: service.CertificateWithDecision{
			Certificate: model.Certificate{
				ID:              "cert-corrupt",
				Type:            model.CertificateTypeUser,
				KeyID:           "key-corrupt",
				Extensions:      `not json`,
				CriticalOptions: `also not json`,
			},
		},
	}

	r := certDetailRouter(t, svc, &service.Identity{Subject: "sub-alice"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/cert-corrupt", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /certs/cert-corrupt = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Data["key_id"] != "key-corrupt" {
		t.Errorf("key_id = %v, want the rest of the record to survive a corrupt option column", resp.Data["key_id"])
	}
	for _, field := range []string{"extensions", "critical_options"} {
		if v, present := resp.Data[field]; present {
			t.Errorf("%s = %v, want absent when the column will not decode", field, v)
		}
	}
}

// A serial is 63 bits of randomness, so nearly every real one is past
// JavaScript's Number.MAX_SAFE_INTEGER. It goes on the wire as a decimal
// string for that reason: parsed as a JSON number it rounds, and the web UI
// shows a serial matching no certificate anyone can find.
func TestCertificateDetailHandler_ShouldWriteTheSerialAsAnExactString(t *testing.T) {
	t.Parallel()

	// The serial from the report that found this: past 2^53, and it rounds
	// to ...958400 the moment a JSON number holds it.
	const serial = 3260700569889958163

	svc := &detailCertService{
		result: service.CertificateWithDecision{
			Certificate: model.Certificate{
				ID:           "cert-serial",
				Type:         model.CertificateTypeUser,
				SerialNumber: serial,
			},
		},
	}

	r := certDetailRouter(t, svc, &service.Identity{Subject: "sub-alice"})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/certs/cert-serial", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /certs/cert-serial = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	// Decoded into a string rather than a float64: a number here would come
	// back rounded, so asserting on it would assert the bug.
	var resp struct {
		Data struct {
			SerialNumber string `json:"serial_number"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("serial_number did not decode as a string: %v, body: %s", err, w.Body.String())
	}

	if want := strconv.FormatUint(serial, 10); resp.Data.SerialNumber != want {
		t.Errorf("serial_number = %q, want %q", resp.Data.SerialNumber, want)
	}
}
