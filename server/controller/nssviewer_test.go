package controller

// Test methodology: the viewer's handlers run against the real lookup
// service over a real database (testdb, so make test-postgres covers the
// queries on Postgres too), with rows seeded the way the sync leaves them.
// Authorization is the real auditor middleware. The service's own
// behaviour (states, search, served) is covered in
// server/service/nss_viewer_test.go; these tests cover the routes, the
// parameter handling and the wire mapping.

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
	"github.com/mnestor/ssoossh/test/testdb"
)

// seedNSSViewerRows writes the rows the viewer route tests read: ada
// (1001) in staff (500) since nssAdminTime, and a membership of staff for
// a uid no account has, stored by the first import (no added time).
func seedNSSViewerRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	seedNSSAccount(t, db, "ada", 1001, 500, func(r *model.PosixUser) { r.Gecos = "Ada Lovelace" })
	seedNSSGroup(t, db, "staff", 500, nil)
	for _, m := range []model.PosixGroupMember{{GID: 500, UID: 1001, AddedAt: ptrTime(nssAdminTime())}, {GID: 500, UID: 4242}} {
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestNSSViewerRoutes_ShouldAdmitAuditorsAndAboveWhenCalled(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/admin/nss/users",
		"/admin/nss/users/1001",
		"/admin/nss/groups",
		"/admin/nss/groups/500",
		"/admin/nss/groups/500/members",
		"/admin/nss/users/1001/audit",
		"/admin/nss/groups/500/audit",
	}
	callers := []struct {
		name   string
		groups []string
		want   int
	}{
		{name: "an admin", groups: []string{"ssh-admins"}, want: http.StatusOK},
		{name: "a SOC member", groups: []string{"soc"}, want: http.StatusOK},
		{name: "an auditor", groups: []string{"auditors"}, want: http.StatusOK},
		{name: "a caller with no role", groups: []string{"ssh-users"}, want: http.StatusForbidden},
	}
	for _, path := range paths {
		for _, caller := range callers {
			t.Run("should answer "+http.StatusText(caller.want)+" to "+caller.name+" when calling GET "+path, func(t *testing.T) {
				t.Parallel()

				db := testdb.New(t)
				seedNSSViewerRows(t, db)
				r := nssAdminRouter(t, db, nssAdminIdentity(caller.groups...), true)

				if w := doNSSRequest(r, http.MethodGet, path); w.Code != caller.want {
					t.Errorf("status = %d, want %d: %s", w.Code, caller.want, w.Body.String())
				}
			})
		}
	}
}

func TestNSSViewerRoutes_ShouldAnswerNotFoundWhenNSSIsDisabled(t *testing.T) {
	t.Parallel()

	paths := []string{
		"/admin/nss/users", "/admin/nss/users/1001", "/admin/nss/groups",
		"/admin/nss/groups/500", "/admin/nss/groups/500/members",
		"/admin/nss/users/1001/audit", "/admin/nss/groups/500/audit",
	}
	for _, path := range paths {
		t.Run("should answer not found for GET "+path+" when NSS is disabled", func(t *testing.T) {
			t.Parallel()

			r := nssAdminRouter(t, testdb.New(t), nssAdminIdentity("auditors"), false)
			if w := doNSSRequest(r, http.MethodGet, path); w.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestNSSViewerRoutes_ShouldAnswerTheErrorWhenAParameterIsWrong(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want int
	}{
		{name: "should refuse a uid when it is not a number", path: "/admin/nss/users/abc", want: http.StatusBadRequest},
		{name: "should refuse a uid when it is beyond uint32", path: "/admin/nss/users/4294967296", want: http.StatusBadRequest},
		{name: "should refuse a gid when it is negative", path: "/admin/nss/groups/-1", want: http.StatusBadRequest},
		{name: "should refuse a members gid when it is not a number", path: "/admin/nss/groups/x/members", want: http.StatusBadRequest},
		{name: "should refuse an accounts cursor when it names no row", path: "/admin/nss/users?after=nope", want: http.StatusBadRequest},
		{name: "should refuse a groups cursor when it names no row", path: "/admin/nss/groups?after=nope", want: http.StatusBadRequest},
		{name: "should refuse a members cursor when it is not a uid", path: "/admin/nss/groups/500/members?after=nope", want: http.StatusBadRequest},
		{name: "should refuse an account state when it is unknown", path: "/admin/nss/users?state=gone", want: http.StatusBadRequest},
		{name: "should refuse a group state when it is disabled", path: "/admin/nss/groups?state=disabled", want: http.StatusBadRequest},
		{name: "should refuse a limit when it is not a number", path: "/admin/nss/users?limit=abc", want: http.StatusBadRequest},
		{name: "should refuse offset when it is sent", path: "/admin/nss/groups?offset=0", want: http.StatusBadRequest},
		{name: "should answer not found when no account has the uid", path: "/admin/nss/users/9999", want: http.StatusNotFound},
		{name: "should answer not found when no group has the gid", path: "/admin/nss/groups/9999", want: http.StatusNotFound},
		{name: "should answer not found for members when no group has the gid", path: "/admin/nss/groups/9999/members", want: http.StatusNotFound},
		{name: "should answer not found for history when no account has the uid", path: "/admin/nss/users/9999/audit", want: http.StatusNotFound},
		{name: "should answer not found for history when no group has the gid", path: "/admin/nss/groups/9999/audit", want: http.StatusNotFound},
		{name: "should refuse a history uid when it is not a number", path: "/admin/nss/users/abc/audit", want: http.StatusBadRequest},
		{name: "should refuse a history cursor when it names no event", path: "/admin/nss/groups/500/audit?after=nope", want: http.StatusBadRequest},
		{name: "should refuse a history limit when it is not a number", path: "/admin/nss/users/1001/audit?limit=abc", want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSViewerRows(t, db)
			r := nssAdminRouter(t, db, nssAdminIdentity("auditors"), true)

			if w := doNSSRequest(r, http.MethodGet, tt.path); w.Code != tt.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestNSSViewerAccounts_ShouldListTheStoredAccountsWhenAsked(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSViewerRows(t, db)
	seedNSSAccount(t, db, "bob", 1002, 500, func(r *model.PosixUser) { r.DirDisabled = true })
	r := nssAdminRouter(t, db, nssAdminIdentity("auditors"), true)

	w := doNSSRequest(r, http.MethodGet, "/admin/nss/users?limit=1")
	var got webtypes.NSSAccountsResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	want := webtypes.NSSAccountSummaryResponse{
		ID: "pu-ada", Name: "ada", UID: 1001, GID: new(uint32(500)), Gecos: "Ada Lovelace", DN: "uid=ada,dc=test",
		State: model.NSSEntryStateActive, LastSyncedAt: nssAdminTime(),
	}
	if got.Total != 2 || len(got.Accounts) != 1 || got.NextCursor == nil || *got.NextCursor != "pu-ada" {
		t.Fatalf("page = %+v, want ada of 2 with a cursor", got)
	}
	if a := got.Accounts[0]; a.ID != want.ID || a.Name != want.Name || a.UID != want.UID || a.GID == nil || *a.GID != *want.GID ||
		a.Gecos != want.Gecos || a.DN != want.DN || a.State != want.State || !a.LastSyncedAt.Equal(want.LastSyncedAt) {
		t.Errorf("account = %+v, want %+v", a, want)
	}

	// A fresh value per decode: omitted fields would keep the last page's.
	w = doNSSRequest(r, http.MethodGet, "/admin/nss/users?after=pu-ada&state=disabled")
	got = webtypes.NSSAccountsResponse{}
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Total != 1 || len(got.Accounts) != 1 || got.Accounts[0].Name != "bob" || got.NextCursor != nil {
		t.Errorf("page = %+v, want only the disabled bob and no cursor", got)
	}
}

func TestNSSViewerAccount_ShouldMapTheAccountAndWhatIsServedWhenAsked(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedPlainUser(t, db, "u-ada")
	userID := "u-ada"
	seedNSSAccount(t, db, "ada", 1001, 500, func(r *model.PosixUser) {
		r.UserID, r.Home, r.Shell = &userID, ptrTo("/home/ada"), ptrTo("/bin/bash")
		r.DirectoryID, r.Gecos, r.PendingGID, r.PendingSince = "dir-ada", "Ada Lovelace", u32(501), ptrTime(nssAdminTime())
	})
	seedNSSGroup(t, db, "staff", 500, nil)
	if err := db.Create(&model.PosixGroupMember{GID: 700, UID: 1001, AddedAt: ptrTime(nssAdminTime())}).Error; err != nil {
		t.Fatal(err)
	}
	r := nssAdminRouter(t, db, nssAdminIdentity("soc"), true)

	w := doNSSRequest(r, http.MethodGet, "/admin/nss/users/1001")
	var got webtypes.NSSAccountDetailResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.ID != "pu-ada" || got.DirectoryID != "dir-ada" || got.State != model.NSSEntryStatePending ||
		got.PendingGID == nil || *got.PendingGID != 501 || got.Home == nil || *got.Home != "/home/ada" {
		t.Errorf("account = %+v, want ada's stored row with her pending gid", got)
	}
	if got.Person == nil || got.Person.UserID != "u-ada" || got.Person.Username != "u-ada" {
		t.Errorf("person = %+v, want the linked u-ada", got.Person)
	}
	if len(got.Groups) != 1 || got.Groups[0].GID != 700 || got.Groups[0].Name != "" || got.Groups[0].State != "" ||
		got.Groups[0].AddedAt == nil || !got.Groups[0].AddedAt.Equal(nssAdminTime()) {
		t.Errorf("groups = %+v, want the one membership of an unstored gid 700, added at %v", got.Groups, nssAdminTime())
	}
	s := got.Served
	if s == nil || got.NotServed != "" || s.GID != 500 || len(s.Groups) != 1 || s.Groups[0].Name != "staff" ||
		s.Status != model.PosixStatusActive || s.Shell == nil || *s.Shell != "/bin/bash" {
		t.Errorf("served = %+v (not served %q), want ada active in staff with her directory shell", s, got.NotServed)
	}
}

func TestNSSViewerAccount_ShouldSayWhyWhenTheAccountIsNotServed(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSAccount(t, db, "ada", 1001, 500, func(r *model.PosixUser) {
		r.WithheldAt, r.WithheldReason = ptrTime(nssAdminTime()), "uid 1001 is held twice"
	})
	r := nssAdminRouter(t, db, nssAdminIdentity("auditors"), true)

	w := doNSSRequest(r, http.MethodGet, "/admin/nss/users/1001")
	var got webtypes.NSSAccountDetailResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Served != nil || got.NotServed != model.NSSNotServedWithheld || got.State != model.NSSEntryStateWithheld ||
		got.WithheldReason != "uid 1001 is held twice" {
		t.Errorf("account = %+v, want it withheld and not served", got)
	}
}

func TestNSSViewerGroups_ShouldListAndDescribeTheGroupsWhenAsked(t *testing.T) {
	t.Parallel()

	cfg := nssAdminTestConfig()
	cfg.NSS.MaxGroupMembers = 1
	db := testdb.New(t)
	seedNSSViewerRows(t, db)
	seedNSSAccount(t, db, "bob", 1002, 500, nil)
	if err := db.Create(&model.PosixGroupMember{GID: 500, UID: 1002}).Error; err != nil {
		t.Fatal(err)
	}
	seedNSSGroup(t, db, "old", 600, func(r *model.PosixGroup) { r.DeletedAt = ptrTime(time.Now().Add(-24 * time.Hour)) })
	r := nssAdminRouterWith(t, cfg, db, nssAdminIdentity("auditors"), true)

	w := doNSSRequest(r, http.MethodGet, "/admin/nss/groups?q=STAFF")
	var list webtypes.NSSGroupsResponse
	decodeEnvelope(t, w.Body.Bytes(), &list)
	if list.Total != 1 || len(list.Groups) != 1 || list.Groups[0].ID != "pg-staff" || list.Groups[0].MemberCount != 3 ||
		list.Groups[0].State != model.NSSEntryStateActive || list.NextCursor != nil {
		t.Errorf("list = %+v, want staff with its 3 membership rows", list)
	}

	w = doNSSRequest(r, http.MethodGet, "/admin/nss/groups/500")
	var group webtypes.NSSGroupDetailResponse
	decodeEnvelope(t, w.Body.Bytes(), &group)
	if group.MemberCount != 3 || group.Served == nil || group.Served.MemberCount != 1 || !group.Served.MembersTruncated ||
		group.Served.Name != "staff" || group.Served.GID != 500 {
		t.Errorf("group = %+v (served %+v), want 3 rows served as 1 truncated member", group, group.Served)
	}

	w = doNSSRequest(r, http.MethodGet, "/admin/nss/groups/600")
	group = webtypes.NSSGroupDetailResponse{}
	decodeEnvelope(t, w.Body.Bytes(), &group)
	if group.State != model.NSSEntryStateTombstone || group.Served == nil || group.Served.MemberCount != 0 || group.NotServed != "" {
		t.Errorf("group = %+v, want the tombstone served with no members", group)
	}
}

func TestNSSViewerMembers_ShouldPageTheMembershipRowsWhenAsked(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSViewerRows(t, db)
	r := nssAdminRouter(t, db, nssAdminIdentity("auditors"), true)

	w := doNSSRequest(r, http.MethodGet, "/admin/nss/groups/500/members?limit=1")
	var got webtypes.NSSGroupMembersResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.Total != 2 || len(got.Members) != 1 || got.NextCursor == nil || *got.NextCursor != "1001" {
		t.Fatalf("page = %+v, want ada of 2 with cursor 1001", got)
	}
	ada := got.Members[0]
	if ada.UID != 1001 || ada.Name != "ada" || ada.State != model.NSSEntryStateActive || ada.AddedAt == nil || !ada.AddedAt.Equal(nssAdminTime()) {
		t.Errorf("member = %+v, want ada active, added at %v", ada, nssAdminTime())
	}

	w = doNSSRequest(r, http.MethodGet, "/admin/nss/groups/500/members?after=1001")
	got = webtypes.NSSGroupMembersResponse{}
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if len(got.Members) != 1 || got.Members[0] != (webtypes.NSSGroupMemberResponse{UID: 4242}) || got.NextCursor != nil {
		t.Errorf("page = %+v, want only the unstored 4242 and no cursor", got)
	}
	// A membership the first import stored has no added time on the wire.
	if strings.Contains(w.Body.String(), "added_at") {
		t.Errorf("body = %s, want no added_at for a membership the first import stored", w.Body.String())
	}
}

// seedNSSHistory records n events about ada's row (pu-ada) a minute
// apart, and one about the staff group, through the real recorder.
func seedNSSHistory(t *testing.T, db *gorm.DB, n int) {
	t.Helper()
	audit := service.NewAuditService(nssAdminTestConfig(), db)
	for i := range n {
		audit.Record(t.Context(), service.AuditEvent{
			Action: service.AuditNSSGroupMembersChanged, System: true,
			OccurredAt: nssAdminTime().Add(time.Duration(i) * time.Minute),
			Detail:     map[string]any{"n": i},
			NSSRefs:    []service.NSSAuditRef{{Kind: model.NSSAuditRefAccount, RowID: "pu-ada"}},
		})
	}
	audit.Record(t.Context(), service.AuditEvent{
		Action: service.AuditNSSGroupCreated, System: true, OccurredAt: nssAdminTime(),
		NSSRefs: []service.NSSAuditRef{{Kind: model.NSSAuditRefGroup, RowID: "pg-staff"}},
	})
}

func TestNSSViewerHistory_ShouldPageTheRowsEventsNewestFirstWhenAsked(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSViewerRows(t, db)
	seedNSSHistory(t, db, 3)
	r := nssAdminRouter(t, db, nssAdminIdentity("auditors"), true)

	var got []float64
	path := "/admin/nss/users/1001/audit?limit=2"
	for range 5 {
		w := doNSSRequest(r, http.MethodGet, path)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", w.Code, w.Body.String())
		}
		var page webtypes.AuditEventsResponse
		decodeEnvelope(t, w.Body.Bytes(), &page)
		if page.Total != 3 {
			t.Errorf("total = %d, want 3", page.Total)
		}
		for _, e := range page.Events {
			n, _ := e.Detail["n"].(float64)
			got = append(got, n)
		}
		if page.NextCursor == nil {
			break
		}
		path = "/admin/nss/users/1001/audit?limit=2&after=" + *page.NextCursor
	}
	if !slices.Equal(got, []float64{2, 1, 0}) {
		t.Errorf("events = %v, want n 2, 1, 0", got)
	}
}

func TestNSSViewerHistory_ShouldAnswerOnlyTheGroupsEventsWhenTheGroupIsAsked(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSViewerRows(t, db)
	seedNSSHistory(t, db, 2)
	r := nssAdminRouter(t, db, nssAdminIdentity("soc"), true)

	w := doNSSRequest(r, http.MethodGet, "/admin/nss/groups/500/audit")
	var page webtypes.AuditEventsResponse
	decodeEnvelope(t, w.Body.Bytes(), &page)
	if w.Code != http.StatusOK || len(page.Events) != 1 || page.Events[0].Action != string(service.AuditNSSGroupCreated) || !page.Events[0].System {
		t.Errorf("status %d, page %+v; want staff's one system event", w.Code, page)
	}
}
