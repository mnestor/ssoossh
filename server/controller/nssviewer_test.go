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
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/webtypes"
	"github.com/mnestor/ssoossh/test/testdb"
)

// seedNSSViewerRows writes the rows the viewer route tests read: ada
// (1001) in staff (500), and a membership of staff for a uid no account
// has.
func seedNSSViewerRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	seedNSSAccount(t, db, "ada", 1001, 500, func(r *model.PosixUser) { r.Gecos = "Ada Lovelace" })
	seedNSSGroup(t, db, "staff", 500, nil)
	for _, m := range []model.PosixGroupMember{{GID: 500, UID: 1001}, {GID: 500, UID: 4242}} {
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
		r.UserID, r.Home, r.Shell, r.ShellPref = &userID, ptrTo("/home/ada"), ptrTo("/bin/bash"), ptrTo("/bin/zsh")
		r.DirectoryID, r.Gecos, r.PendingGID, r.PendingSince = "dir-ada", "Ada Lovelace", u32(501), ptrTime(nssAdminTime())
	})
	seedNSSGroup(t, db, "staff", 500, nil)
	if err := db.Create(&model.PosixGroupMember{GID: 700, UID: 1001}).Error; err != nil {
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
	if len(got.Groups) != 1 || got.Groups[0].GID != 700 || got.Groups[0].Name != "" || got.Groups[0].State != "" {
		t.Errorf("groups = %+v, want the one membership of an unstored gid 700", got.Groups)
	}
	s := got.Served
	if s == nil || got.NotServed != "" || s.GID != 500 || len(s.Groups) != 1 || s.Groups[0].Name != "staff" ||
		s.Status != model.PosixStatusActive || s.ShellPref == nil || *s.ShellPref != "/bin/zsh" {
		t.Errorf("served = %+v (not served %q), want ada active in staff with her shell pref", s, got.NotServed)
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
	if got.Total != 2 || len(got.Members) != 1 || got.Members[0] != (webtypes.NSSGroupMemberResponse{UID: 1001, Name: "ada", State: model.NSSEntryStateActive}) ||
		got.NextCursor == nil || *got.NextCursor != "1001" {
		t.Fatalf("page = %+v, want ada of 2 with cursor 1001", got)
	}

	w = doNSSRequest(r, http.MethodGet, "/admin/nss/groups/500/members?after=1001")
	got = webtypes.NSSGroupMembersResponse{}
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if len(got.Members) != 1 || got.Members[0] != (webtypes.NSSGroupMemberResponse{UID: 4242}) || got.NextCursor != nil {
		t.Errorf("page = %+v, want only the unstored 4242 and no cursor", got)
	}
}
