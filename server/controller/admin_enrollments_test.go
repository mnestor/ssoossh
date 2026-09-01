package controller

// The admin enrollment directory and detail endpoints. These arrived with
// the service-code admin work and were merged with their authorization
// paths covered but their response mapping not: every field below is copied
// by hand from a service struct into a webtypes struct, which is the shape
// of code where a wrong or dropped field is invisible until someone reads
// the panel and finds it empty.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// stubEnrollmentProvider serves canned admin list/detail results so the
// handlers' mapping can be asserted without standing up the enrollment
// service and its schema.
type stubEnrollmentProvider struct {
	list       service.AdminEnrollmentList
	listErr    error
	lastParams service.AdminListParams

	detail    service.AdminEnrollmentDetail
	detailErr error
	lastID    string

	setEmailErr    error
	lastSetEmailID string
	lastSetEmail   string
}

func (s *stubEnrollmentProvider) Retrieve(_ context.Context, _ string, _ string) (string, error) {
	return "", nil
}

func (s *stubEnrollmentProvider) ListRetrievals(_ context.Context, _ string, _ *service.Identity) (service.RetrievalLog, error) {
	return service.RetrievalLog{}, nil
}

func (s *stubEnrollmentProvider) ListForIdentity(_ context.Context, _ *service.Identity) ([]service.ServiceEnrollment, error) {
	return nil, nil
}

func (s *stubEnrollmentProvider) ListForAdmin(_ context.Context, _ *service.Identity, params service.AdminListParams) (service.AdminEnrollmentList, error) {
	s.lastParams = params
	return s.list, s.listErr
}

func (s *stubEnrollmentProvider) GetEnrollmentDetail(_ context.Context, id string, _ *service.Identity) (service.AdminEnrollmentDetail, error) {
	s.lastID = id
	return s.detail, s.detailErr
}

func (s *stubEnrollmentProvider) SetNotificationEmail(_ context.Context, id string, _ *service.Identity, address string) error {
	s.lastSetEmailID, s.lastSetEmail = id, address
	return s.setEmailErr
}

func newAuditorIdentity(cfg *config.Config) *service.Identity {
	return &service.Identity{
		Subject:  "sub-auditor",
		Username: "auditor",
		Groups:   []string{cfg.Admin.AuditorGroup},
	}
}

func TestListEnrollmentsHandler_ShouldMapEveryFieldOntoTheResponse(t *testing.T) {
	t.Parallel()

	redeemed := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Second)
	lastRetrieved := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	certSecs := int64(900)

	prov := &stubEnrollmentProvider{
		list: service.AdminEnrollmentList{
			Total: 7,
			Enrollments: []service.AdminEnrollmentRow{{
				Enrollment: model.Enrollment{
					ID:                         "enr-1",
					KeyID:                      "key-id-1",
					CreatedAt:                  time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second),
					ExpiresAt:                  time.Now().Add(24 * time.Hour).UTC().Truncate(time.Second),
					RedeemedAt:                 &redeemed,
					CertificateDurationSeconds: &certSecs,
				},
				Approver:        model.User{Username: "alice", Email: "alice@corp.example"},
				Principals:      []string{"svc-deploy"},
				Fingerprint:     "SHA256:abc",
				RetrievalCount:  4,
				LastRetrievedAt: &lastRetrieved,
				Options: service.RequestedOptions{
					Extensions:      []string{"permit-pty"},
					ForceCommand:    "/usr/bin/true",
					SourceAddresses: []string{"10.0.0.0/8"},
					NoTouchRequired: true,
				},
			}},
		},
	}

	cfg := newTestConfig(t)
	r := routerWithEnrollmentService(t, cfg, newTestDB(t), newAuditorIdentity(cfg), prov)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/enrollments?limit=2&offset=2&q=deploy", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/enrollments = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Enrollments []map[string]any `json:"enrollments"`
			Meta        map[string]any   `json:"meta"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v, body: %s", err, w.Body.String())
	}

	if len(resp.Data.Enrollments) != 1 {
		t.Fatalf("returned %d enrollments, want 1", len(resp.Data.Enrollments))
	}
	got := resp.Data.Enrollments[0]

	for _, tc := range []struct {
		field string
		want  any
	}{
		{"id", "enr-1"},
		{"approved_by_username", "alice"},
		{"approved_by_email", "alice@corp.example"},
		{"key_id", "key-id-1"},
		{"public_key_fingerprint", "SHA256:abc"},
		{"retrieval_count", float64(4)},
		{"certificate_valid_seconds", float64(900)},
	} {
		if got[tc.field] != tc.want {
			t.Errorf("%s = %v, want %v", tc.field, got[tc.field], tc.want)
		}
	}

	// The optional timestamps are the fields most easily dropped: they are
	// pointers behind a nil check, so forgetting the branch leaves them
	// absent rather than wrong.
	for _, field := range []string{"first_redeemed_at", "last_retrieved_at"} {
		if got[field] == nil {
			t.Errorf("%s is absent, want the seeded timestamp", field)
		}
	}

	// convertOptions has no other caller in a test, and a dropped option
	// here silently understates what a certificate is allowed to do.
	opts, ok := got["options"].(map[string]any)
	if !ok {
		t.Fatalf("options = %v, want an object", got["options"])
	}
	if opts["force_command"] != "/usr/bin/true" {
		t.Errorf("options.force_command = %v, want /usr/bin/true", opts["force_command"])
	}
	if opts["no_touch_required"] != true {
		t.Errorf("options.no_touch_required = %v, want true", opts["no_touch_required"])
	}

	// meta.total counts matches, not the page.
	if total, _ := resp.Data.Meta["total"].(float64); total != 7 {
		t.Errorf("meta.total = %v, want 7", total)
	}

	// The paging and search terms have to reach the service, or the panel
	// pages through a query the database never narrowed.
	if prov.lastParams.Limit != 2 || prov.lastParams.Offset != 2 || prov.lastParams.Query != "deploy" {
		t.Errorf("service saw %+v, want limit 2, offset 2, query \"deploy\"", prov.lastParams)
	}
}

// A row with no redemption yet must not invent one. These are the nil
// branches of the two pointer fields above.
func TestListEnrollmentsHandler_ShouldOmitTimestampsForAnUnredeemedEnrollment(t *testing.T) {
	t.Parallel()

	prov := &stubEnrollmentProvider{
		list: service.AdminEnrollmentList{
			Total: 1,
			Enrollments: []service.AdminEnrollmentRow{{
				Enrollment: model.Enrollment{ID: "enr-fresh"},
				Approver:   model.User{Username: "alice"},
			}},
		},
	}

	cfg := newTestConfig(t)
	r := routerWithEnrollmentService(t, cfg, newTestDB(t), newAuditorIdentity(cfg), prov)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/enrollments", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/enrollments = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Enrollments []map[string]any `json:"enrollments"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := resp.Data.Enrollments[0]
	if got["first_redeemed_at"] != nil {
		t.Errorf("first_redeemed_at = %v, want absent for an unredeemed enrollment", got["first_redeemed_at"])
	}
	if got["certificate_valid_seconds"] != nil {
		t.Errorf("certificate_valid_seconds = %v, want absent when the row stores none", got["certificate_valid_seconds"])
	}
}

// An empty directory has to serialize as [] rather than null: the panel
// iterates it, and null is a different type to every consumer.
func TestListEnrollmentsHandler_ShouldReturnAnArrayWhenThereAreNone(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	r := routerWithEnrollmentService(t, cfg, newTestDB(t), newAuditorIdentity(cfg), &stubEnrollmentProvider{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/enrollments", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/enrollments = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Enrollments []map[string]any `json:"enrollments"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Enrollments == nil {
		t.Error("enrollments serialized as null, want []")
	}
}

// A service failure has to reach the client as the service's own status,
// not as a 200 with an empty list.
func TestListEnrollmentsHandler_ShouldSurfaceAServiceError(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	prov := &stubEnrollmentProvider{listErr: &errorresponses.ForbiddenError{Reason: "not an auditor"}}
	r := routerWithEnrollmentService(t, cfg, newTestDB(t), newAuditorIdentity(cfg), prov)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/enrollments", nil))

	if w.Code != http.StatusForbidden {
		t.Errorf("GET /admin/enrollments with a forbidden service = %d, want 403", w.Code)
	}
}

// Bad paging is the caller's mistake, so it is a 400 -- not a 500, and not
// silently clamped to the default page.
func TestListEnrollmentsHandler_ShouldRejectUnparseablePaging(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	r := routerWithEnrollmentService(t, cfg, newTestDB(t), newAuditorIdentity(cfg), &stubEnrollmentProvider{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/enrollments?limit=not-a-number", nil))

	if w.Code != http.StatusBadRequest {
		t.Errorf("GET /admin/enrollments?limit=not-a-number = %d, want 400, body: %s", w.Code, w.Body.String())
	}
}

func TestGetEnrollmentDetailHandler_ShouldReturnTheEnrollmentAndItsRetrievalLog(t *testing.T) {
	t.Parallel()

	firstRetrieved := time.Now().Add(-30 * time.Minute).UTC().Truncate(time.Second)
	redeemed := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Second)
	certSecs := int64(600)

	prov := &stubEnrollmentProvider{
		detail: service.AdminEnrollmentDetail{
			Enrollment: model.Enrollment{
				ID:                         "enr-42",
				KeyID:                      "key-42",
				RedeemedAt:                 &redeemed,
				CertificateDurationSeconds: &certSecs,
			},
			Approver:    model.User{Username: "alice", Email: "alice@corp.example"},
			Principals:  []string{"svc-deploy"},
			Fingerprint: "SHA256:def",
			Options:     service.RequestedOptions{Extensions: []string{"permit-pty"}},
			Retrievals: service.RetrievalLog{
				Total: 2,
				Retrievals: []model.EnrollmentRetrieval{
					{RetrievedAt: firstRetrieved, SourceIP: "10.1.2.3", CertificateSerial: 99, Succeeded: true},
					{RetrievedAt: firstRetrieved.Add(-time.Hour), SourceIP: "10.1.2.4", Succeeded: false},
				},
			},
		},
	}

	cfg := newTestConfig(t)
	r := routerWithEnrollmentService(t, cfg, newTestDB(t), newAuditorIdentity(cfg), prov)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/enrollments/enr-42", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/enrollments/enr-42 = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Enrollment     map[string]any   `json:"enrollment"`
			Retrievals     []map[string]any `json:"retrievals"`
			RetrievalTotal int              `json:"retrieval_total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v, body: %s", err, w.Body.String())
	}

	if prov.lastID != "enr-42" {
		t.Errorf("service asked for %q, want the path parameter %q", prov.lastID, "enr-42")
	}
	if resp.Data.Enrollment["id"] != "enr-42" {
		t.Errorf("enrollment.id = %v, want enr-42", resp.Data.Enrollment["id"])
	}
	if resp.Data.Enrollment["public_key_fingerprint"] != "SHA256:def" {
		t.Errorf("enrollment.public_key_fingerprint = %v, want SHA256:def", resp.Data.Enrollment["public_key_fingerprint"])
	}

	// retrieval_count on the detail response is the log's Total, not the
	// length of the page -- the log is paged (RetrievalPageSize), so those
	// two differ for any well-used code.
	if got, _ := resp.Data.Enrollment["retrieval_count"].(float64); got != 2 {
		t.Errorf("enrollment.retrieval_count = %v, want 2", got)
	}
	if resp.Data.RetrievalTotal != 2 {
		t.Errorf("retrieval_total = %d, want 2", resp.Data.RetrievalTotal)
	}

	if len(resp.Data.Retrievals) != 2 {
		t.Fatalf("returned %d retrievals, want 2", len(resp.Data.Retrievals))
	}
	if resp.Data.Retrievals[0]["source_ip"] != "10.1.2.3" {
		t.Errorf("retrievals[0].source_ip = %v, want 10.1.2.3", resp.Data.Retrievals[0]["source_ip"])
	}
	// A failed redemption is the row an auditor is looking for; it must not
	// be filtered out or reported as a success.
	if resp.Data.Retrievals[1]["succeeded"] != false {
		t.Errorf("retrievals[1].succeeded = %v, want false", resp.Data.Retrievals[1]["succeeded"])
	}

	// last_retrieved_at comes from the newest row of the log rather than
	// from the enrollment, so an empty log has to leave it absent.
	if resp.Data.Enrollment["last_retrieved_at"] == nil {
		t.Error("enrollment.last_retrieved_at is absent, want the newest retrieval's timestamp")
	}
}

func TestGetEnrollmentDetailHandler_ShouldReturnAnArrayForANeverRedeemedCode(t *testing.T) {
	t.Parallel()

	prov := &stubEnrollmentProvider{
		detail: service.AdminEnrollmentDetail{
			Enrollment: model.Enrollment{ID: "enr-unused"},
			Approver:   model.User{Username: "alice"},
		},
	}

	cfg := newTestConfig(t)
	r := routerWithEnrollmentService(t, cfg, newTestDB(t), newAuditorIdentity(cfg), prov)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/enrollments/enr-unused", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/enrollments/enr-unused = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Enrollment map[string]any   `json:"enrollment"`
			Retrievals []map[string]any `json:"retrievals"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.Data.Retrievals == nil {
		t.Error("retrievals serialized as null, want []")
	}
	if resp.Data.Enrollment["last_retrieved_at"] != nil {
		t.Errorf("last_retrieved_at = %v, want absent with an empty log", resp.Data.Enrollment["last_retrieved_at"])
	}
}

func TestGetEnrollmentDetailHandler_ShouldSurfaceNotFound(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	prov := &stubEnrollmentProvider{detailErr: &errorresponses.NotFoundError{Resource: `enrollment "enr-nope"`}}
	r := routerWithEnrollmentService(t, cfg, newTestDB(t), newAuditorIdentity(cfg), prov)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/enrollments/enr-nope", nil))

	if w.Code != http.StatusNotFound {
		t.Errorf("GET /admin/enrollments/enr-nope = %d, want 404, body: %s", w.Code, w.Body.String())
	}
}

// Both endpoints are auditor-scoped: a plain user must not read the
// directory or any one code's redemption history.
func TestAdminEnrollmentEndpoints_ShouldDenyAPlainUser(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	plain := &service.Identity{Subject: "sub-bob", Username: "bob", Groups: []string{"ssh-users"}}
	r := routerWithEnrollmentService(t, cfg, newTestDB(t), plain, &stubEnrollmentProvider{})

	for _, path := range []string{"/admin/enrollments", "/admin/enrollments/enr-1"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusForbidden {
			t.Errorf("GET %s as a plain user = %d, want 403", path, w.Code)
		}
	}
}
