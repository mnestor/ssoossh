package controller

// Test methodology: drive GET /admin/audit with the action and source_ip
// filters through the router-with-real-middleware harness, over events
// seeded through the real AuditService, so the source_ip column is the one
// the recorder writes. Each seeded event carries its index in detail.n,
// which is how a test names the rows it expects back.

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
)

// seedFilterableFeed records a mix of actions and observed sources, one
// minute apart, oldest first. The cert.approved row carries the filtered
// address as its approver_ip only, so a filter that matched the approver
// would return it.
func seedFilterableFeed(t *testing.T, db *gorm.DB) {
	t.Helper()

	rows := []struct {
		action service.AuditAction
		detail map[string]any
	}{
		{service.AuditCertRequestRefusedNetwork, map[string]any{"source_ip": "203.0.113.9"}},                   // 0
		{service.AuditCertRequested, map[string]any{"source_ip": "198.51.100.7"}},                              // 1
		{service.AuditCertRequestRefusedNetwork, map[string]any{"source_ip": "198.51.100.7"}},                  // 2
		{service.AuditCertApproved, map[string]any{"source_ip": "198.51.100.7", "approver_ip": "203.0.113.9"}}, // 3
		{service.AuditAuthLogin, map[string]any{}},                                                             // 4
		{service.AuditCertRequestRefusedNetwork, map[string]any{"source_ip": "203.0.113.9"}},                   // 5
		{service.AuditCertRequested, map[string]any{"source_ip": "203.0.113.9"}},                               // 6
	}
	base := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	for i, row := range rows {
		row.detail["n"] = i
		seedAuditEvent(t, db, service.AuditEvent{
			Action:     row.action,
			System:     true,
			OccurredAt: base.Add(time.Duration(i) * time.Minute),
			Detail:     row.detail,
		})
	}
}

// feedIDsByN maps each seeded event's detail.n to its id, read off the
// unfiltered feed.
func feedIDsByN(t *testing.T, r http.Handler) map[int]string {
	t.Helper()
	code, page := getAuditPage(t, r, "/admin/audit?limit=100")
	if code != http.StatusOK {
		t.Fatalf("GET /admin/audit: got %d, want %d", code, http.StatusOK)
	}
	out := make(map[int]string, len(page.Events))
	for _, e := range page.Events {
		n, _ := e.Detail["n"].(float64)
		out[int(n)] = e.ID
	}
	return out
}

// derefOr reads an optional cursor, "" when absent.
func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// pageNumbers reads detail.n off a response page, in page order.
func pageNumbers(page webtypes.AuditEventsResponse) []int {
	out := make([]int, 0, len(page.Events))
	for _, e := range page.Events {
		n, _ := e.Detail["n"].(float64)
		out = append(out, int(n))
	}
	return out
}

func TestAuditFeed_ShouldFilterByActionAndSource(t *testing.T) {
	t.Parallel()

	// after and wantNext name rows by detail.n; 0 in wantNext is read with
	// hasNext, so the oldest row can still be named.
	tests := []struct {
		name      string
		path      string
		after     *int
		want      []int
		wantTotal int64
		wantNext  *int
	}{
		{name: "should return every event when unfiltered", path: "/admin/audit", want: []int{6, 5, 4, 3, 2, 1, 0}, wantTotal: 7},
		{name: "should keep one action when filtered by action", path: "/admin/audit?action=cert.request_refused_network", want: []int{5, 2, 0}, wantTotal: 3},
		{name: "should keep one source when filtered by source", path: "/admin/audit?source_ip=203.0.113.9", want: []int{6, 5, 0}, wantTotal: 3},
		{name: "should not match the approver's address when filtered by source", path: "/admin/audit?source_ip=198.51.100.7", want: []int{3, 2, 1}, wantTotal: 3},
		{name: "should keep the intersection when filtered by both", path: "/admin/audit?action=cert.request_refused_network&source_ip=203.0.113.9", want: []int{5, 0}, wantTotal: 2},
		{name: "should match an IPv4-mapped spelling of a stored address", path: "/admin/audit?source_ip=::ffff:203.0.113.9", want: []int{6, 5, 0}, wantTotal: 3},
		{name: "should treat an empty filter as no filter", path: "/admin/audit?action=&source_ip=", want: []int{6, 5, 4, 3, 2, 1, 0}, wantTotal: 7},
		{name: "should return an empty page when nothing matches", path: "/admin/audit?source_ip=192.0.2.1", want: []int{}, wantTotal: 0},
		{name: "should page the first window of a filtered feed", path: "/admin/audit?action=cert.request_refused_network&limit=2", want: []int{5, 2}, wantTotal: 3, wantNext: ptrTo(2)},
		{name: "should page the last window of a filtered feed", path: "/admin/audit?action=cert.request_refused_network&limit=2", after: ptrTo(2), want: []int{0}, wantTotal: 3},
		{name: "should page both filters together", path: "/admin/audit?action=cert.request_refused_network&source_ip=203.0.113.9&limit=1", want: []int{5}, wantTotal: 2, wantNext: ptrTo(5)},
		{name: "should resume both filters together from the cursor", path: "/admin/audit?action=cert.request_refused_network&source_ip=203.0.113.9&limit=1", after: ptrTo(5), want: []int{0}, wantTotal: 2, wantNext: nil},
	}

	db := newTestDB(t)
	seedFilterableFeed(t, db)
	r := routerWithAuth(t, newTestConfig(t), db, auditorIdentity())
	ids := feedIDsByN(t, r)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path
			if tt.after != nil {
				path += "&after=" + ids[*tt.after]
			}
			code, page := getAuditPage(t, r, path)
			if code != http.StatusOK {
				t.Fatalf("GET %s: got %d, want %d", tt.path, code, http.StatusOK)
			}
			if got := pageNumbers(page); !slices.Equal(got, tt.want) {
				t.Errorf("GET %s events = %v, want %v", tt.path, got, tt.want)
			}
			if page.Total != tt.wantTotal {
				t.Errorf("GET %s total = %d, want %d", tt.path, page.Total, tt.wantTotal)
			}
			wantNext := ""
			if tt.wantNext != nil {
				wantNext = ids[*tt.wantNext]
			}
			if got := derefOr(page.NextCursor); got != wantNext {
				t.Errorf("GET %s next cursor = %q, want %q", path, got, wantNext)
			}
		})
	}
}

func TestAuditFeed_ShouldRejectAnInvalidFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
	}{
		{name: "should reject an action outside the taxonomy", path: "/admin/audit?action=cert.nonsense"},
		{name: "should reject a source that is a hostname", path: "/admin/audit?source_ip=example.com"},
		{name: "should reject a source that is a network", path: "/admin/audit?source_ip=203.0.113.0%2F24"},
		{name: "should reject a source with a port", path: "/admin/audit?source_ip=203.0.113.9:22"},
		{name: "should reject a good action beside a bad source", path: "/admin/audit?action=auth.login&source_ip=nope"},
	}

	r := routerWithAuth(t, newTestConfig(t), newTestDB(t), auditorIdentity())

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tt.path, nil))
			if w.Code != http.StatusBadRequest {
				t.Errorf("GET %s: got %d, want %d, body: %s", tt.path, w.Code, http.StatusBadRequest, w.Body.String())
			}
		})
	}
}

// The filters do not change who may read the feed.
func TestAuditFeed_ShouldDenyAPlainUserAFilteredFeed(t *testing.T) {
	t.Parallel()

	identity := &service.Identity{Subject: "sub-user", Username: "user", Groups: []string{"ssh-users"}}
	r := routerWithAuth(t, newTestConfig(t), newTestDB(t), identity)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/audit?action=auth.login&source_ip=203.0.113.9", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("filtered feed as a plain user: got %d, want %d", w.Code, http.StatusForbidden)
	}
}
