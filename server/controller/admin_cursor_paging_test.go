package controller

// Tests for the cursor paging the five admin lists share: the audit feed,
// one user's audit timeline, the cross-user certificate history, the user
// directory and the service-code list. Each list is driven through its real
// handler and a real schema with the same cases: walking every page through
// next_cursor, a row written between two page requests, a filter combined
// with the cursor, and the refusals for a cursor that names no row and for
// the retired offset parameter. The cases are shared with the dbparity
// Postgres run, so both backends are held to the same answers.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
)

// cursorRow is one listed row: its id, which is what a cursor names, and the
// label the seed gave it, which is what a case asserts on.
type cursorRow struct {
	ID    string
	Label string
}

// cursorList describes one admin list for the shared cases.
type cursorList struct {
	name string
	// path is the endpoint, without a query string.
	path string
	// seed writes the list's rows; want is their labels in list order.
	seed func(t *testing.T, db *gorm.DB)
	want []string
	// insertFirst writes one row, labelled "new", that sorts ahead of every
	// seeded row.
	insertFirst func(t *testing.T, db *gorm.DB)
	// filter is a query fragment narrowing the list, and filterWant the
	// labels it keeps, in order. An empty filter skips the case: the user
	// timeline has no filters.
	filter     string
	filterWant []string
	// decode reads a response body into rows and the next cursor.
	decode func(t *testing.T, body []byte) ([]cursorRow, *string)
}

// cursorBase is the timestamp every seeded row is offset from: whole minutes,
// so the rows read back identically on SQLite and on Postgres's microsecond
// timestamps.
func cursorBase() time.Time {
	return time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
}

// seedCursorUser writes a users row; the other lists need an owner or an
// approver to join to.
func seedCursorUser(t *testing.T, db *gorm.DB, user model.User) {
	t.Helper()
	user.Subject = "sub-" + user.ID
	user.CreatedAt = cursorBase()
	user.UpdatedAt = cursorBase()
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user %s: %v", user.ID, err)
	}
}

// seedCursorAudit writes one audit row directly, with id "ae-<label>", so
// two rows sharing a timestamp have a known tiebreak; the recorder would
// give them random UUIDs. The payload is the stored shape the handlers
// decode, labelled in its detail.
func seedCursorAudit(t *testing.T, db *gorm.DB, label string, action service.AuditAction, actor, target string, at time.Time) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"action": string(action), "detail": map[string]any{"label": label}, "v": 1})
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	row := model.AuditEvent{ID: "ae-" + label, CreatedAt: at, Action: string(action), Payload: string(payload)}
	if actor != "" {
		row.ActorUserID = &actor
	}
	if target != "" {
		row.TargetUserID = &target
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed audit event %s: %v", label, err)
	}
}

// seedCursorCert writes one certificate owned by user-1, labelled by key ID.
func seedCursorCert(t *testing.T, db *gorm.DB, id, label string, certType model.CertificateType, serial uint64, at time.Time) {
	t.Helper()
	owner := "user-1"
	cert := model.Certificate{
		ID: id, Type: certType, UserID: &owner, SerialNumber: serial,
		KeyID: label, PublicKeyFingerprint: "SHA256:" + label,
		IssuedAt: at, ExpiresAt: at.Add(24 * time.Hour),
	}
	if err := db.Create(&cert).Error; err != nil {
		t.Fatalf("seed certificate %s: %v", label, err)
	}
}

// seedCursorEnrollment writes one enrollment approved by user-1, labelled by
// key ID.
func seedCursorEnrollment(t *testing.T, db *gorm.DB, id, label, account string, at time.Time) {
	t.Helper()
	enrollment := model.Enrollment{
		ID: id, Code: "code-" + id, PublicKey: "key", UserID: "user-1",
		KeyID: label, Principals: `["` + account + `"]`, ServiceAccount: account,
		CreatedAt: at, ExpiresAt: at.Add(24 * time.Hour),
	}
	if err := db.Create(&enrollment).Error; err != nil {
		t.Fatalf("seed enrollment %s: %v", label, err)
	}
}

// decodeAuditRows reads an audit page, labelling each event from its detail.
func decodeAuditRows(t *testing.T, body []byte) ([]cursorRow, *string) {
	t.Helper()
	var resp struct {
		Data struct {
			Events []struct {
				ID     string         `json:"id"`
				Detail map[string]any `json:"detail"`
			} `json:"events"`
			NextCursor *string `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v, body: %s", err, body)
	}
	rows := make([]cursorRow, 0, len(resp.Data.Events))
	for _, e := range resp.Data.Events {
		label, _ := e.Detail["label"].(string)
		rows = append(rows, cursorRow{ID: e.ID, Label: label})
	}
	return rows, resp.Data.NextCursor
}

// decodeRowsBy returns a decoder for a list whose rows sit under field and
// carry their label in labelField.
func decodeRowsBy(field, labelField string) func(t *testing.T, body []byte) ([]cursorRow, *string) {
	return func(t *testing.T, body []byte) ([]cursorRow, *string) {
		t.Helper()
		var resp struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("decode: %v, body: %s", err, body)
		}
		var items []map[string]any
		if err := json.Unmarshal(resp.Data[field], &items); err != nil {
			t.Fatalf("decode %s: %v, body: %s", field, err, body)
		}
		var next *string
		if raw, ok := resp.Data["next_cursor"]; ok {
			if err := json.Unmarshal(raw, &next); err != nil {
				t.Fatalf("decode next_cursor: %v, body: %s", err, body)
			}
		}
		rows := make([]cursorRow, 0, len(items))
		for _, item := range items {
			id, _ := item["id"].(string)
			label, _ := item[labelField].(string)
			rows = append(rows, cursorRow{ID: id, Label: label})
		}
		return rows, next
	}
}

// cursorLists returns the five lists under test.
func cursorLists() []cursorList {
	base := cursorBase()
	return []cursorList{
		{
			name: "audit feed",
			path: "/admin/audit",
			seed: func(t *testing.T, db *gorm.DB) {
				// a1 and a2 share a timestamp, so the id breaks their tie.
				seedCursorAudit(t, db, "a0", service.AuditAuthLogin, "user-1", "", base)
				seedCursorAudit(t, db, "a1", service.AuditCertApproved, "user-1", "", base.Add(time.Minute))
				seedCursorAudit(t, db, "a2", service.AuditAuthLogin, "user-2", "", base.Add(time.Minute))
				seedCursorAudit(t, db, "a3", service.AuditAuthLogin, "user-1", "", base.Add(2*time.Minute))
				seedCursorAudit(t, db, "a4", service.AuditCertApproved, "user-2", "", base.Add(3*time.Minute))
			},
			insertFirst: func(t *testing.T, db *gorm.DB) {
				seedCursorAudit(t, db, "new", service.AuditAuthLogin, "user-1", "", base.Add(time.Hour))
			},
			filter:     "action=auth.login",
			filterWant: []string{"a3", "a2", "a0"},
			decode:     decodeAuditRows,
		},
		{
			name: "user audit timeline",
			path: "/admin/users/user-1/audit",
			seed: func(t *testing.T, db *gorm.DB) {
				seedCursorAudit(t, db, "t0", service.AuditAuthLogin, "user-1", "", base)
				seedCursorAudit(t, db, "x0", service.AuditAuthLogin, "user-2", "", base.Add(time.Minute))
				seedCursorAudit(t, db, "t1", service.AuditUserDisabled, "user-2", "user-1", base.Add(2*time.Minute))
				seedCursorAudit(t, db, "t2", service.AuditAuthLogin, "user-1", "", base.Add(2*time.Minute))
				seedCursorAudit(t, db, "t3", service.AuditAuthLogin, "user-1", "", base.Add(3*time.Minute))
			},
			insertFirst: func(t *testing.T, db *gorm.DB) {
				seedCursorAudit(t, db, "new", service.AuditAuthLogin, "user-1", "", base.Add(time.Hour))
			},
			decode: decodeAuditRows,
		},
		{
			name: "certificate history",
			path: "/admin/certificates/history",
			seed: func(t *testing.T, db *gorm.DB) {
				seedCursorCert(t, db, "cert-0", "c0", model.CertificateTypeUser, 100, base)
				seedCursorCert(t, db, "cert-1", "c1", model.CertificateTypeService, 101, base.Add(time.Minute))
				seedCursorCert(t, db, "cert-2", "c2", model.CertificateTypeUser, 102, base.Add(time.Minute))
				seedCursorCert(t, db, "cert-3", "c3", model.CertificateTypeUser, 103, base.Add(2*time.Minute))
				seedCursorCert(t, db, "cert-4", "c4", model.CertificateTypeService, 104, base.Add(3*time.Minute))
			},
			insertFirst: func(t *testing.T, db *gorm.DB) {
				seedCursorCert(t, db, "cert-new", "new", model.CertificateTypeUser, 200, base.Add(time.Hour))
			},
			filter:     "type=user&q=c",
			filterWant: []string{"c3", "c2", "c0"},
			decode:     decodeRowsBy("certificates", "key_id"),
		},
		{
			name: "user directory",
			path: "/admin/users",
			seed: func(t *testing.T, db *gorm.DB) {
				// Named users first by name ignoring case, then the unnamed
				// by username; "Dana" and "dana" tie on the name.
				seedCursorUser(t, db, model.User{ID: "u-zed", Username: "zed"})
				seedCursorUser(t, db, model.User{ID: "u-dana2", Username: "dana2", DisplayName: "dana"})
				seedCursorUser(t, db, model.User{ID: "u-bea", Username: "bea", DisplayName: "Bea"})
				seedCursorUser(t, db, model.User{ID: "u-abe", Username: "abe"})
				seedCursorUser(t, db, model.User{ID: "u-dana", Username: "dana", DisplayName: "Dana"})
			},
			want: []string{"bea", "dana", "dana2", "abe", "zed"},
			insertFirst: func(t *testing.T, db *gorm.DB) {
				seedCursorUser(t, db, model.User{ID: "u-aaron", Username: "new", DisplayName: "Aaron"})
			},
			filter:     "q=a&status=active",
			filterWant: []string{"bea", "dana", "dana2", "abe"},
			decode:     decodeRowsBy("users", "username"),
		},
		{
			name: "service codes",
			path: "/admin/enrollments",
			seed: func(t *testing.T, db *gorm.DB) {
				seedCursorEnrollment(t, db, "enr-0", "e0", "svc-deploy", base)
				seedCursorEnrollment(t, db, "enr-1", "e1", "svc-backup", base.Add(time.Minute))
				seedCursorEnrollment(t, db, "enr-2", "e2", "svc-deploy", base.Add(time.Minute))
				seedCursorEnrollment(t, db, "enr-3", "e3", "svc-deploy", base.Add(2*time.Minute))
				seedCursorEnrollment(t, db, "enr-4", "e4", "svc-backup", base.Add(3*time.Minute))
			},
			insertFirst: func(t *testing.T, db *gorm.DB) {
				seedCursorEnrollment(t, db, "enr-new", "new", "svc-deploy", base.Add(time.Hour))
			},
			filter:     "q=deploy",
			filterWant: []string{"e3", "e2", "e0"},
			decode:     decodeRowsBy("enrollments", "key_id"),
		},
	}
}

// wantOrder is the list's full order: the user directory states its own,
// and every other list is newest first, which for these seeds is the reverse
// of the order they were written in, ties broken by the larger id.
func (l cursorList) wantOrder() []string {
	if l.want != nil {
		return l.want
	}
	switch l.name {
	case "audit feed":
		return []string{"a4", "a3", "a2", "a1", "a0"}
	case "user audit timeline":
		return []string{"t3", "t2", "t1", "t0"}
	case "certificate history":
		return []string{"c4", "c3", "c2", "c1", "c0"}
	default:
		return []string{"e4", "e3", "e2", "e1", "e0"}
	}
}

// resetCursorTables empties every table the cases write, children first, so
// one database can serve them all.
func resetCursorTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, table := range []string{"enrollment_retrievals", "enrollments", "certificates", "audit_events", "users"} {
		if err := db.Exec("DELETE FROM " + table).Error; err != nil {
			t.Fatalf("clear %s: %v", table, err)
		}
	}
}

// getCursorPage performs one GET and returns the status and body.
func getCursorPage(t *testing.T, r http.Handler, path string, query url.Values) (int, []byte) {
	t.Helper()
	target := path
	if encoded := query.Encode(); encoded != "" {
		target += "?" + encoded
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w.Code, w.Body.Bytes()
}

// walkCursorPages follows next_cursor from the first page to the last and
// returns every label in order, failing if the walk does not end.
func walkCursorPages(t *testing.T, r http.Handler, list cursorList, filter url.Values, limit int) []string {
	t.Helper()
	var labels []string
	query := url.Values{}
	for k, v := range filter {
		query[k] = v
	}
	query.Set("limit", fmt.Sprint(limit))
	for range 20 {
		code, body := getCursorPage(t, r, list.path, query)
		if code != http.StatusOK {
			t.Fatalf("GET %s?%s = %d, want 200; body: %s", list.path, query.Encode(), code, body)
		}
		rows, next := list.decode(t, body)
		if len(rows) > limit {
			t.Fatalf("GET %s?%s served %d rows, want at most %d", list.path, query.Encode(), len(rows), limit)
		}
		for _, row := range rows {
			labels = append(labels, row.Label)
		}
		if next == nil {
			return labels
		}
		if len(rows) == 0 || *next != rows[len(rows)-1].ID {
			t.Fatalf("GET %s?%s next_cursor = %q, want the last served row's id", list.path, query.Encode(), *next)
		}
		query.Set("after", *next)
	}
	t.Fatalf("paging %s did not end; got %v", list.path, labels)
	return nil
}

// checkCursorPaging runs the shared cases for every list against db.
func checkCursorPaging(t *testing.T, db *gorm.DB) {
	t.Helper()

	cfg := newTestConfig(t)
	enrollments, err := service.NewEnrollmentService(cfg, db, nil, nil)
	if err != nil {
		t.Fatalf("NewEnrollmentService: %v", err)
	}
	r := routerWithEnrollmentService(t, cfg, db, adminIdentity(cfg.Admin.RequireGroup), enrollments)

	// reset clears the tables and seeds the owners the lists join to, plus
	// the list's own rows. The directory list seeds its own users.
	reset := func(t *testing.T, list cursorList) {
		t.Helper()
		resetCursorTables(t, db)
		if list.name != "user directory" {
			seedCursorUser(t, db, model.User{ID: "user-1", Username: "alice"})
			seedCursorUser(t, db, model.User{ID: "user-2", Username: "bob"})
		}
		list.seed(t, db)
	}

	for _, list := range cursorLists() {
		for _, limit := range []int{1, 2, 3, 100} {
			t.Run(fmt.Sprintf("%s should list every row once in order when paged %d at a time", list.name, limit), func(t *testing.T) {
				reset(t, list)
				if got := walkCursorPages(t, r, list, nil, limit); !slices.Equal(got, list.wantOrder()) {
					t.Errorf("paged %v, want %v", got, list.wantOrder())
				}
			})
		}

		t.Run(list.name+" should return a null cursor when the first page holds everything", func(t *testing.T) {
			reset(t, list)
			code, body := getCursorPage(t, r, list.path, url.Values{"limit": {"100"}})
			if code != http.StatusOK {
				t.Fatalf("GET %s = %d, want 200; body: %s", list.path, code, body)
			}
			if _, next := list.decode(t, body); next != nil {
				t.Errorf("next_cursor = %q, want null", *next)
			}
		})

		t.Run(list.name+" should neither repeat nor skip a row when one is written between pages", func(t *testing.T) {
			reset(t, list)
			code, body := getCursorPage(t, r, list.path, url.Values{"limit": {"2"}})
			if code != http.StatusOK {
				t.Fatalf("first page = %d, want 200; body: %s", code, body)
			}
			first, next := list.decode(t, body)
			if next == nil {
				t.Fatalf("first page has no next_cursor")
			}

			list.insertFirst(t, db)

			code, body = getCursorPage(t, r, list.path, url.Values{"limit": {"2"}, "after": {*next}})
			if code != http.StatusOK {
				t.Fatalf("second page = %d, want 200; body: %s", code, body)
			}
			second, _ := list.decode(t, body)
			var got []string
			for _, row := range slices.Concat(first, second) {
				got = append(got, row.Label)
			}
			if want := list.wantOrder()[:4]; !slices.Equal(got, want) {
				t.Errorf("two pages = %v, want %v", got, want)
			}
		})

		if list.filter != "" {
			t.Run(list.name+" should keep the filter across pages when combined with the cursor", func(t *testing.T) {
				reset(t, list)
				filter, err := url.ParseQuery(list.filter)
				if err != nil {
					t.Fatalf("parse filter: %v", err)
				}
				if got := walkCursorPages(t, r, list, filter, 1); !slices.Equal(got, list.filterWant) {
					t.Errorf("paged %v, want %v", got, list.filterWant)
				}
			})
		}

		for _, bad := range []struct {
			name  string
			query url.Values
		}{
			{name: "should refuse a cursor that names no row", query: url.Values{"after": {"no-such-row"}}},
			{name: "should refuse an offset", query: url.Values{"offset": {"2"}}},
			{name: "should refuse an empty offset", query: url.Values{"offset": {""}}},
		} {
			t.Run(list.name+" "+bad.name, func(t *testing.T) {
				reset(t, list)
				if code, body := getCursorPage(t, r, list.path, bad.query); code != http.StatusBadRequest {
					t.Errorf("GET %s?%s = %d, want 400; body: %s", list.path, bad.query.Encode(), code, body)
				}
			})
		}
	}

	t.Run("user audit timeline should refuse a cursor from another user's timeline", func(t *testing.T) {
		list := cursorLists()[1]
		reset(t, list)
		// x0 is on user-2's timeline only.
		var otherOnly string
		code, body := getCursorPage(t, r, "/admin/users/user-2/audit", url.Values{"limit": {"100"}})
		if code != http.StatusOK {
			t.Fatalf("GET user-2 timeline = %d; body: %s", code, body)
		}
		all, _ := list.decode(t, body)
		for _, row := range all {
			if row.Label == "x0" {
				otherOnly = row.ID
			}
		}
		if otherOnly == "" {
			t.Fatalf("user-2's timeline has no x0: %v", all)
		}
		if code, body := getCursorPage(t, r, list.path, url.Values{"after": {otherOnly}}); code != http.StatusBadRequest {
			t.Errorf("GET %s with user-2's event as the cursor = %d, want 400; body: %s", list.path, code, body)
		}
	})
}

// should page all five admin lists by cursor, on SQLite.
func TestAdminLists_ShouldPageByCursorOnSQLite(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&model.EnrollmentRetrieval{}); err != nil {
		t.Fatalf("migrate enrollment_retrievals: %v", err)
	}
	checkCursorPaging(t, db)
}

// A database that fails the cursor lookup is a server error, not a refused
// cursor: the caller's cursor may be fine, and a 400 would tell them it was
// not. Each list's lookup is failed on its own by a callback that matches
// the columns only that lookup selects.
func TestAdminLists_ShouldReportAFailedCursorLookup(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		selects string
	}{
		{name: "should report a failed lookup on the audit feed", path: "/admin/audit", selects: "id,created_at"},
		{name: "should report a failed lookup on a user timeline", path: "/admin/users/user-1/audit", selects: "id,created_at"},
		{name: "should report a failed lookup on the certificate history", path: "/admin/certificates/history", selects: "id,issued_at"},
		{name: "should report a failed lookup on the user directory", path: "/admin/users", selects: "id, display_name, LOWER(display_name) AS name_key, LOWER(username) AS username_key"},
		{name: "should report a failed lookup on the service codes", path: "/admin/enrollments", selects: "id,created_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newTestDB(t)
			if err := db.AutoMigrate(&model.EnrollmentRetrieval{}); err != nil {
				t.Fatalf("migrate enrollment_retrievals: %v", err)
			}
			if err := db.Callback().Query().Before("gorm:query").Register("test:fail_cursor_lookup", func(tx *gorm.DB) {
				if strings.Join(tx.Statement.Selects, ",") == tt.selects {
					_ = tx.AddError(errors.New("database unavailable"))
				}
			}); err != nil {
				t.Fatalf("register callback: %v", err)
			}

			cfg := newTestConfig(t)
			enrollments, err := service.NewEnrollmentService(cfg, db, nil, nil)
			if err != nil {
				t.Fatalf("NewEnrollmentService: %v", err)
			}
			r := routerWithEnrollmentService(t, cfg, db, adminIdentity(cfg.Admin.RequireGroup), enrollments)

			code, body := getCursorPage(t, r, tt.path, url.Values{"after": {"any-row"}})
			if code != http.StatusInternalServerError {
				t.Errorf("GET %s with a failing lookup = %d, want 500; body: %s", tt.path, code, body)
			}
		})
	}
}
