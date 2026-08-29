package controller

// Tests for the database access the admin handlers do on the way to a
// response: that a failed count is reported rather than rendered as zero,
// that the user directory does not issue a query per row, and that option
// sets reach the wire in the shape the wire type promises.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// adminIdentity is an identity in the configured admin group, which also
// satisfies the auditor checks these routes use.
func adminIdentity(adminGroup string) *service.Identity {
	return &service.Identity{
		Subject:  "sub-admin",
		Username: "admin",
		Groups:   []string{adminGroup},
	}
}

// recordSQL registers a query recorder on db and returns a function yielding
// every statement issued since. Used to tell one query from N.
func recordSQL(t *testing.T, db *gorm.DB) func() []string {
	t.Helper()

	var executed []string
	if err := db.Callback().Query().After("gorm:query").
		Register("test:record_sql", func(tx *gorm.DB) {
			executed = append(executed, tx.Statement.SQL.String())
		}); err != nil {
		t.Fatalf("failed to register the SQL recorder: %v", err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove("test:record_sql") })

	return func() []string { return executed }
}

// should report a failure to count a user's certificates, rather than
// answering 200 with a count of zero. Zero and "the database did not answer"
// are different facts about an account, and only one of them is safe to act
// on.
func TestGetUserHandler_ShouldReportACertificateCountFailure(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.Create(&model.User{ID: "u-alice", Subject: "sub-alice", Username: "alice"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.Migrator().DropTable(&model.Certificate{}); err != nil {
		t.Fatalf("drop certificates: %v", err)
	}

	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, adminIdentity(cfg.Admin.RequireGroup))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/u-alice", nil))

	if w.Code == http.StatusOK {
		t.Fatalf("GET /admin/users/u-alice = 200 with an unqueryable certificates table, "+
			"want an error rather than a fabricated count; body: %s", w.Body.String())
	}
}

// should likewise report a failure to count a user's active enrollments.
func TestGetUserHandler_ShouldReportAnEnrollmentCountFailure(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.Create(&model.User{ID: "u-alice", Subject: "sub-alice", Username: "alice"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.Migrator().DropTable(&model.Enrollment{}); err != nil {
		t.Fatalf("drop enrollments: %v", err)
	}

	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, adminIdentity(cfg.Admin.RequireGroup))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users/u-alice", nil))

	if w.Code == http.StatusOK {
		t.Fatalf("GET /admin/users/u-alice = 200 with an unqueryable enrollments table, "+
			"want an error rather than a fabricated count; body: %s", w.Body.String())
	}
}

// should still answer 200 when the post-disable enrollment count fails. The
// disable is already committed by the time that count runs, so failing the
// request would report an error for a write that succeeded — the count is
// advisory detail about a decision already taken. It is logged instead.
func TestDisableUserHandler_ShouldSucceedWhenTheAdvisoryCountFails(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.Create(&model.User{ID: "u-alice", Subject: "sub-alice", Username: "alice"}).Error; err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := db.Create(&model.User{ID: "u-admin", Subject: "sub-admin", Username: "admin"}).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if err := db.Migrator().DropTable(&model.Enrollment{}); err != nil {
		t.Fatalf("drop enrollments: %v", err)
	}

	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, adminIdentity(cfg.Admin.RequireGroup))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, jsonPatch(t, "/admin/users/u-alice/disable", `{"reason":"test reason"}`))

	if w.Code != http.StatusOK {
		t.Fatalf("PATCH /admin/users/u-alice/disable = %d, want 200: the disable committed before "+
			"the advisory count ran; body: %s", w.Code, w.Body.String())
	}

	var user model.User
	if err := db.First(&user, "id = ?", "u-alice").Error; err != nil {
		t.Fatalf("read back user: %v", err)
	}
	if user.DisabledAt == nil {
		t.Error("the user was not disabled, so the handler answered 200 for a write that did not happen")
	}
}

// should resolve who disabled each user without a query per row. The
// directory is paginated, so a lookup inside the loop turns one page into
// one query plus one per disabled user.
func TestListUsersHandler_ShouldNotQueryPerDisabledUser(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	disabledAt := time.Now()
	adminID := "u-admin"
	if err := db.Create(&model.User{ID: adminID, Subject: "sub-admin", Username: "admin"}).Error; err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	const disabledUsers = 8
	for i := range disabledUsers {
		u := model.User{
			ID:               "u-" + string(rune('a'+i)),
			Subject:          "sub-" + string(rune('a'+i)),
			Username:         "user" + string(rune('a'+i)),
			DisabledAt:       &disabledAt,
			DisabledByUserID: &adminID,
		}
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("seed user %d: %v", i, err)
		}
	}

	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, adminIdentity(cfg.Admin.RequireGroup))

	statements := recordSQL(t, db)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users?limit=100", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/users = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	// A count and a page fetch is the floor; anything approaching one per
	// disabled user is the pattern this pins against.
	if got := len(statements()); got > 4 {
		t.Errorf("the directory issued %d queries for %d disabled users, want a fixed handful:\n  %s",
			got, disabledUsers, strings.Join(statements(), "\n  "))
	}

	// The join must still answer the question the loop answered.
	var resp struct {
		Data struct {
			Users []webtypes.AdminUserSummary `json:"users"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v, body: %s", err, w.Body.String())
	}
	seen := 0
	for _, u := range resp.Data.Users {
		if u.DisabledAt == nil {
			continue
		}
		seen++
		if u.DisabledByUsername != "admin" {
			t.Errorf("user %s reports disabled_by_username %q, want %q", u.ID, u.DisabledByUsername, "admin")
		}
	}
	if seen != disabledUsers {
		t.Errorf("saw %d disabled users in the response, want %d", seen, disabledUsers)
	}
}

// fakeEnrollmentServiceWithOptions serves one admin enrollment whose option
// set carries no extensions, which is how a row with none decodes.
type fakeEnrollmentServiceWithOptions struct {
	stubEnrollmentProvider
}

func (f *fakeEnrollmentServiceWithOptions) ListForAdmin(_ context.Context, _ *service.Identity, _ service.AdminListParams) (service.AdminEnrollmentList, error) {
	return service.AdminEnrollmentList{
		Total: 1,
		Enrollments: []service.AdminEnrollmentRow{{
			Enrollment: model.Enrollment{ID: "e-1", CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)},
			Approver:   model.User{Username: "admin"},
			Options:    service.RequestedOptions{},
		}},
	}, nil
}

// should render an enrollment's empty extension set as [] rather than null.
// CertificateOptionsResponse.Extensions is validate:"required" and generates
// as a non-optional array, so null there is what stops a page rendering —
// the same failure the user detail endpoint already carries a comment about.
func TestAdminEnrollmentsHandler_ShouldRenderEmptyExtensionsAsAnArray(t *testing.T) {
	t.Parallel()

	cfg := newTestConfig(t)
	db := newTestDB(t)
	r := routerWithEnrollmentService(t, cfg, db, adminIdentity(cfg.Admin.RequireGroup),
		&fakeEnrollmentServiceWithOptions{})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/enrollments", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/enrollments = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Enrollments []struct {
				Options struct {
					Extensions *[]string `json:"extensions"`
				} `json:"options"`
			} `json:"enrollments"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v, body: %s", err, w.Body.String())
	}
	if len(resp.Data.Enrollments) != 1 {
		t.Fatalf("got %d enrollments, want 1; body: %s", len(resp.Data.Enrollments), w.Body.String())
	}
	if resp.Data.Enrollments[0].Options.Extensions == nil {
		t.Errorf("options.extensions serialized as null, want []; body: %s", w.Body.String())
	}
}
