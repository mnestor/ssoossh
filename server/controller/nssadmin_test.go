package controller

// Test methodology: the NSS admin handlers run against the real NSS
// directory service over a real database (testdb, so make test-postgres
// covers the accept transaction on Postgres too), with rows seeded the way
// the sync leaves them. The admin operations never reach the directory, so
// no fake LDAP is needed. Authorization is the real admin middleware.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/middleware"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
	"github.com/mnestor/ssoossh/test/testdb"
)

// nssAdminTestConfig is the LDAP admin test config with NSS on.
func nssAdminTestConfig() *config.Config {
	cfg := ldapAdminTestConfig()
	cfg.NSS = config.NSSConfig{
		Enabled:     true,
		UserFilter:  "(objectClass=posixAccount)",
		GroupFilter: "(objectClass=posixGroup)",
		Attributes: config.NSSAttributes{
			Name: "uid", UID: "uidNumber", GID: "gidNumber", Home: "homeDirectory", Shell: "loginShell",
			GroupName: "cn", GroupGID: "gidNumber", GroupMember: "memberUid", GecosAttributes: []string{"cn"},
		},
		GecosTemplate:      "{{.DisplayName}}",
		TombstoneRetention: 2160 * time.Hour,
		DisabledShell:      "/usr/sbin/nologin",
		MaxGroupMembers:    2000,
	}
	return cfg
}

// nssAdminTime is the fixed instant seeded rows are stamped with.
func nssAdminTime() time.Time { return time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC) }

// nssAdminRouter wires the NSS admin controller with the real admin and
// auditor middleware, a pass-through CSRF check and the given identity.
// When enabled, the viewer is the real lookup service with no directory
// refresher; otherwise both services are nil, which is NSS disabled.
func nssAdminRouter(t *testing.T, db *gorm.DB, identity *service.Identity, enabled bool) *gin.Engine {
	t.Helper()
	return nssAdminRouterWith(t, nssAdminTestConfig(), db, identity, enabled)
}

// nssAdminRouterWith is nssAdminRouter over cfg.
func nssAdminRouterWith(t *testing.T, cfg *config.Config, db *gorm.DB, identity *service.Identity, enabled bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	var admin service.NSSDirectoryAdmin
	var viewer service.NSSDirectoryViewer
	if enabled {
		svc, err := service.NewNSSDirectoryService(cfg, db)
		if err != nil {
			t.Fatalf("NewNSSDirectoryService() error = %v", err)
		}
		svc.SetAuditor(service.NewAuditService(cfg, db))
		admin = svc
		lookups := service.NewNSSLookupService(cfg, db, nil)
		lookups.SetAuditor(service.NewAuditService(cfg, db))
		viewer = lookups
	}

	r := gin.New()
	r.Use(middleware.NewErrorHandlerMiddleware().Add())
	NewNSSAdminController(&r.RouterGroup, db, admin, viewer,
		identityMiddleware(identity), middleware.NewAdminAuthMiddleware(cfg).Add(),
		middleware.NewAuditorAuthMiddleware(cfg).Add(), func(c *gin.Context) { c.Next() })
	return r
}

// nssAdminIdentity is a caller holding groups.
func nssAdminIdentity(groups ...string) *service.Identity {
	return &service.Identity{Subject: "sub-admin", Username: "admin", Groups: groups}
}

// seedNSSAccount writes one account row as the sync would leave it.
func seedNSSAccount(t *testing.T, db *gorm.DB, name string, uid, gid uint32, mutate func(*model.PosixUser)) model.PosixUser {
	t.Helper()
	row := model.PosixUser{
		ID: "pu-" + name, UID: uid, Name: name, DN: "uid=" + name + ",dc=test", GID: gid,
		CreatedAt: nssAdminTime(), UpdatedAt: nssAdminTime(), LastSyncedAt: nssAdminTime(),
	}
	if mutate != nil {
		mutate(&row)
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed account %s: %v", name, err)
	}
	return row
}

// seedNSSGroup writes one group row as the sync would leave it.
func seedNSSGroup(t *testing.T, db *gorm.DB, name string, gid uint32, mutate func(*model.PosixGroup)) model.PosixGroup {
	t.Helper()
	row := model.PosixGroup{
		ID: "pg-" + name, GID: gid, Name: name, DN: "cn=" + name + ",dc=test",
		CreatedAt: nssAdminTime(), UpdatedAt: nssAdminTime(), LastSyncedAt: nssAdminTime(),
	}
	if mutate != nil {
		mutate(&row)
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed group %s: %v", name, err)
	}
	return row
}

// doNSSRequest sends one request with no body.
func doNSSRequest(r *gin.Engine, method, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
	return w
}

// u32 returns a pointer to v.
func u32(v uint32) *uint32 { return &v }

func TestNSSAdminRoutes_ShouldAdmitOnlyAdminsWhenCalled(t *testing.T) {
	t.Parallel()

	routes := []struct {
		method string
		path   string
		admin  int
	}{
		{http.MethodGet, "/admin/nss/pending-id-changes", http.StatusOK},
		{http.MethodPost, "/admin/nss/users/1001/accept-id-change", http.StatusOK},
		{http.MethodPost, "/admin/nss/groups/500/accept-id-change", http.StatusOK},
		{http.MethodGet, "/admin/nss/collisions", http.StatusOK},
		{http.MethodDelete, "/admin/nss/users/2002/tombstone", http.StatusOK},
		{http.MethodDelete, "/admin/nss/groups/600/tombstone", http.StatusOK},
	}
	callers := []struct {
		name   string
		groups []string
		admin  bool
	}{
		{name: "an admin", groups: []string{"ssh-admins"}, admin: true},
		{name: "a SOC member", groups: []string{"soc"}},
		{name: "an auditor", groups: []string{"auditors"}},
		{name: "a caller with no role", groups: []string{"ssh-users"}},
	}
	for _, route := range routes {
		for _, caller := range callers {
			want := http.StatusForbidden
			if caller.admin {
				want = route.admin
			}
			t.Run("should answer "+http.StatusText(want)+" to "+caller.name+" when calling "+route.method+" "+route.path, func(t *testing.T) {
				t.Parallel()

				db := testdb.New(t)
				seedNSSAccount(t, db, "ada", 1001, 100, func(r *model.PosixUser) { r.PendingUID = u32(3001); r.PendingSince = ptrTime(nssAdminTime()) })
				seedNSSAccount(t, db, "gone", 2002, 100, func(r *model.PosixUser) { r.DeletedAt = ptrTime(nssAdminTime()) })
				seedNSSGroup(t, db, "staff", 500, func(r *model.PosixGroup) { r.PendingGID = u32(501); r.PendingSince = ptrTime(nssAdminTime()) })
				seedNSSGroup(t, db, "old", 600, func(r *model.PosixGroup) { r.DeletedAt = ptrTime(nssAdminTime()) })
				r := nssAdminRouter(t, db, nssAdminIdentity(caller.groups...), true)

				if w := doNSSRequest(r, route.method, route.path); w.Code != want {
					t.Errorf("status = %d, want %d: %s", w.Code, want, w.Body.String())
				}
			})
		}
	}
}

// ptrTime returns a pointer to v.
func ptrTime(v time.Time) *time.Time { return &v }

func TestNSSAdminRoutes_ShouldAnswerNotFoundWhenNSSIsDisabled(t *testing.T) {
	t.Parallel()

	r := nssAdminRouter(t, testdb.New(t), nssAdminIdentity("ssh-admins"), false)
	for _, path := range []string{"/admin/nss/pending-id-changes", "/admin/nss/collisions"} {
		if w := doNSSRequest(r, http.MethodGet, path); w.Code != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", path, w.Code)
		}
	}
}

func TestNSSAdminRoutes_ShouldRefuseANumberWhenItIsNotAUint32(t *testing.T) {
	t.Parallel()

	r := nssAdminRouter(t, testdb.New(t), nssAdminIdentity("ssh-admins"), true)
	for _, path := range []string{"/admin/nss/users/abc/accept-id-change", "/admin/nss/users/4294967296/accept-id-change", "/admin/nss/groups/-1/accept-id-change"} {
		if w := doNSSRequest(r, http.MethodPost, path); w.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", path, w.Code)
		}
	}
	for _, path := range []string{"/admin/nss/users/x/tombstone", "/admin/nss/groups/x/tombstone"} {
		if w := doNSSRequest(r, http.MethodDelete, path); w.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", path, w.Code)
		}
	}
}

func TestNSSAdminPending_ShouldListThePendingChangesWhenAsked(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSAccount(t, db, "ada", 1001, 100, func(r *model.PosixUser) { r.PendingUID = u32(3001); r.PendingSince = ptrTime(nssAdminTime()) })
	seedNSSAccount(t, db, "bob", 1002, 100, nil)
	seedNSSGroup(t, db, "staff", 500, func(r *model.PosixGroup) { r.PendingGID = u32(501); r.PendingSince = ptrTime(nssAdminTime()) })
	r := nssAdminRouter(t, db, nssAdminIdentity("ssh-admins"), true)

	w := doNSSRequest(r, http.MethodGet, "/admin/nss/pending-id-changes")
	var got webtypes.NSSPendingIDChangesResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if len(got.Users) != 1 || got.Users[0].Name != "ada" || got.Users[0].PendingUID == nil || *got.Users[0].PendingUID != 3001 || got.Users[0].PendingGID != nil {
		t.Errorf("users = %+v, want ada with only her uid pending", got.Users)
	}
	if len(got.Groups) != 1 || got.Groups[0].PendingGID != 501 || got.Groups[0].GID != 500 {
		t.Errorf("groups = %+v, want staff 500 pending 501", got.Groups)
	}
}

func TestNSSAdminAcceptUser_ShouldApplyTheChangeAndAuditItWhenNothingHoldsTheUid(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedPlainUser(t, db, "u-admin")
	if err := db.Model(&model.User{}).Where("id = ?", "u-admin").Update("subject", "sub-admin").Error; err != nil {
		t.Fatal(err)
	}
	seedNSSAccount(t, db, "ada", 1001, 100, func(r *model.PosixUser) {
		r.PendingUID, r.PendingGID, r.PendingSince = u32(3001), u32(300), ptrTime(nssAdminTime())
	})
	if err := db.Create(&model.PosixGroupMember{GID: 700, UID: 1001}).Error; err != nil {
		t.Fatal(err)
	}
	r := nssAdminRouter(t, db, nssAdminIdentity("ssh-admins"), true)

	w := doNSSRequest(r, http.MethodPost, "/admin/nss/users/1001/accept-id-change")
	var got webtypes.NSSAcceptedUserIDChangeResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.UID != 3001 || got.GID != 300 || got.Name != "ada" {
		t.Errorf("response = %+v, want ada with uid 3001 and gid 300", got)
	}
	var member model.PosixGroupMember
	if err := db.First(&member, "gid = ?", 700).Error; err != nil || member.UID != 3001 {
		t.Errorf("membership = %+v, %v; want it re-keyed to 3001", member, err)
	}
	events := auditEventsWithAction(t, db, string(service.AuditNSSIDChangeAccepted))
	if len(events) != 1 || !bytes.Contains([]byte(events[0].Payload), []byte(`"user_id":"u-admin"`)) {
		t.Errorf("events = %+v, want one naming the admin", events)
	}
}

func TestNSSAdminAccept_ShouldAnswerConflictOrNotFoundWhenTheChangeCannotApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "should answer conflict when a tombstone holds the new uid", path: "/admin/nss/users/1001/accept-id-change", wantStatus: http.StatusConflict},
		{name: "should answer not found when the account has nothing pending", path: "/admin/nss/users/3001/accept-id-change", wantStatus: http.StatusNotFound},
		{name: "should answer conflict when another group holds the new gid", path: "/admin/nss/groups/500/accept-id-change", wantStatus: http.StatusConflict},
		{name: "should answer not found when no group has the gid", path: "/admin/nss/groups/999/accept-id-change", wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSAccount(t, db, "ada", 1001, 100, func(r *model.PosixUser) { r.PendingUID = u32(3001); r.PendingSince = ptrTime(nssAdminTime()) })
			seedNSSAccount(t, db, "gone", 3001, 100, func(r *model.PosixUser) { r.DeletedAt = ptrTime(nssAdminTime()) })
			seedNSSGroup(t, db, "staff", 500, func(r *model.PosixGroup) { r.PendingGID = u32(501); r.PendingSince = ptrTime(nssAdminTime()) })
			seedNSSGroup(t, db, "other", 501, nil)
			r := nssAdminRouter(t, db, nssAdminIdentity("ssh-admins"), true)

			if w := doNSSRequest(r, http.MethodPost, tt.path); w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

func TestNSSAdminAcceptGroup_ShouldMoveTheGroupAndItsAccountsWhenAccepted(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSGroup(t, db, "staff", 500, func(r *model.PosixGroup) { r.PendingGID = u32(501); r.PendingSince = ptrTime(nssAdminTime()) })
	seedNSSAccount(t, db, "ada", 1001, 500, nil)
	r := nssAdminRouter(t, db, nssAdminIdentity("ssh-admins"), true)

	w := doNSSRequest(r, http.MethodPost, "/admin/nss/groups/500/accept-id-change")
	var got webtypes.NSSAcceptedGroupIDChangeResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if got.GID != 501 {
		t.Errorf("response = %+v, want gid 501", got)
	}
	var ada model.PosixUser
	if err := db.First(&ada, "name = ?", "ada").Error; err != nil || ada.GID != 501 {
		t.Errorf("ada = %+v, %v; want her primary gid moved to 501", ada, err)
	}
}

func TestNSSAdminCollisions_ShouldReportBothSidesWhenAPairExists(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	holder := seedNSSAccount(t, db, "ada", 3000, 3000, func(r *model.PosixUser) {
		r.DeletedAt, r.WithheldAt = ptrTime(nssAdminTime()), ptrTime(nssAdminTime())
	})
	if err := db.Create(&model.NSSCollision{
		Kind: model.NSSCollisionKindUID, HolderID: holder.ID, OtherDN: "uid=eve,dc=test", Value: "3000",
		OtherName: "eve", OtherNumber: 3000, FirstSeenAt: nssAdminTime(), LastSeenAt: nssAdminTime(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	r := nssAdminRouter(t, db, nssAdminIdentity("ssh-admins"), true)

	w := doNSSRequest(r, http.MethodGet, "/admin/nss/collisions")
	var got webtypes.NSSCollisionsResponse
	decodeEnvelope(t, w.Body.Bytes(), &got)
	if len(got.Collisions) != 1 {
		t.Fatalf("collisions = %+v, want one", got.Collisions)
	}
	c := got.Collisions[0]
	if c.Kind != "uid" || c.Value != "3000" || !c.Holder.Tombstone || !c.Holder.Stored || c.Holder.Name != "ada" || c.Other.Stored || c.Other.Name != "eve" {
		t.Errorf("collision = %+v, want the tombstone ada holding 3000 against the refused eve", c)
	}
}

func TestNSSAdminPurge_ShouldPurgeOnlyATombstoneWhenAsked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantName   string
	}{
		{name: "should purge the account when it is a tombstone", path: "/admin/nss/users/2002/tombstone", wantStatus: http.StatusOK, wantName: "gone"},
		{name: "should answer not found when the account is live", path: "/admin/nss/users/1001/tombstone", wantStatus: http.StatusNotFound},
		{name: "should purge the group when it is a tombstone", path: "/admin/nss/groups/600/tombstone", wantStatus: http.StatusOK, wantName: "old"},
		{name: "should answer not found when the group is live", path: "/admin/nss/groups/500/tombstone", wantStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			seedNSSAccount(t, db, "ada", 1001, 100, nil)
			seedNSSAccount(t, db, "gone", 2002, 100, func(r *model.PosixUser) { r.DeletedAt = ptrTime(nssAdminTime()) })
			seedNSSGroup(t, db, "staff", 500, nil)
			seedNSSGroup(t, db, "old", 600, func(r *model.PosixGroup) { r.DeletedAt = ptrTime(nssAdminTime()) })
			r := nssAdminRouter(t, db, nssAdminIdentity("ssh-admins"), true)

			w := doNSSRequest(r, http.MethodDelete, tt.path)
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantName == "" {
				return
			}
			var got webtypes.NSSPurgedTombstoneResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			if got.Name != tt.wantName {
				t.Errorf("purged = %+v, want %s", got, tt.wantName)
			}
			if events := auditEventsWithAction(t, db, string(service.AuditNSSTombstonePurged)); len(events) != 1 {
				t.Errorf("purge events = %d, want 1", len(events))
			}
		})
	}
}

// fakeNSSSync stands in for the NSS directory's full sync.
type fakeNSSSync struct {
	result *service.NSSSyncResult
	err    error
	calls  int
}

// FullSync records the call and returns the canned outcome.
func (f *fakeNSSSync) FullSync(context.Context) (*service.NSSSyncResult, error) {
	f.calls++
	return f.result, f.err
}

func TestLDAPSyncHandler_ShouldRunTheNSSSyncWhenNSSIsOn(t *testing.T) {
	t.Parallel()

	finished := nssAdminTime().Add(time.Minute)
	tests := []struct {
		name      string
		dryRun    bool
		nss       *fakeNSSSync
		wantCalls int
		wantNSS   bool
		wantError string
	}{
		{
			name:      "should report the NSS counts when the pass is not a dry run",
			nss:       &fakeNSSSync{result: &service.NSSSyncResult{Full: true, StartedAt: nssAdminTime(), FinishedAt: finished, Accounts: service.NSSSyncCounts{Seen: 3, Created: 2}}},
			wantCalls: 1, wantNSS: true,
		},
		{
			name:      "should report the NSS error when another run is in progress",
			nss:       &fakeNSSSync{err: service.ErrNSSSyncInProgress},
			wantCalls: 1, wantNSS: true, wantError: service.ErrNSSSyncInProgress.Error(),
		},
		{name: "should not run the NSS sync when the pass is a dry run", dryRun: true, nss: &fakeNSSSync{}, wantCalls: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			fake := &fakeLDAPDiagnostics{run: &model.LDAPSyncRun{ID: "run-1", StartedAt: time.Now(), DryRun: tt.dryRun}}
			identity := &service.Identity{Subject: "sub-alice", Username: "alice", Groups: []string{"ssh-admins"}}
			r := ldapRouterWithNSS(t, ldapAdminTestConfig(), db, identity, fake, tt.nss)

			w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/sync", webtypes.LDAPSyncRequestBody{DryRun: tt.dryRun})
			var got webtypes.LDAPSyncRunResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			if tt.nss.calls != tt.wantCalls || (got.NSS != nil) != tt.wantNSS {
				t.Fatalf("calls = %d, nss = %+v; want %d calls and nss present %v", tt.nss.calls, got.NSS, tt.wantCalls, tt.wantNSS)
			}
			if !tt.wantNSS {
				return
			}
			if got.NSS.Error != tt.wantError {
				t.Errorf("nss error = %q, want %q", got.NSS.Error, tt.wantError)
			}
			if tt.wantError == "" && (got.NSS.Accounts.Seen != 3 || got.NSS.Accounts.Created != 2 || got.NSS.FinishedAt == nil) {
				t.Errorf("nss = %+v, want the counts and the finish time", got.NSS)
			}
			events := auditEventsWithAction(t, db, string(service.AuditLDAPSyncTriggered))
			var payload struct {
				Detail map[string]json.RawMessage `json:"detail"`
			}
			if len(events) != 1 || json.Unmarshal([]byte(events[0].Payload), &payload) != nil || payload.Detail["nss"] == nil {
				t.Errorf("sync events = %+v, want the NSS outcome in the detail", events)
			}
		})
	}
}

func TestNSSSyncRunResponse_ShouldCarryTheErrorWhenTheRunFailed(t *testing.T) {
	t.Parallel()

	got := newNSSSyncRunResponse(&service.NSSSyncResult{Full: true, StartedAt: nssAdminTime()}, errors.New("test error"))
	if got.Error != "test error" || got.FinishedAt != nil || !got.Full {
		t.Errorf("response = %+v, want the error, no finish time and the run's start", got)
	}
}

// TestLDAPSyncHandler_ShouldAuditTheNSSOutcomeInTheResponseShape keeps the
// ldap.sync_triggered event's nss detail, which the service now builds,
// identical to the nss object the endpoint returns, as it was when the
// endpoint recorded it from the response.
func TestLDAPSyncHandler_ShouldAuditTheNSSOutcomeInTheResponseShape(t *testing.T) {
	t.Parallel()

	finished := nssAdminTime().Add(time.Minute)
	tests := []struct {
		name string
		nss  *fakeNSSSync
	}{
		{name: "should match when the NSS sync completed", nss: &fakeNSSSync{result: &service.NSSSyncResult{
			Full: true, StartedAt: nssAdminTime(), FinishedAt: finished,
			Accounts: service.NSSSyncCounts{Seen: 3, Created: 2, Tombstoned: 1}, Groups: service.NSSSyncCounts{Seen: 2, Refused: 1},
		}}},
		{name: "should match when the NSS sync failed", nss: &fakeNSSSync{
			result: &service.NSSSyncResult{Full: true, StartedAt: nssAdminTime()}, err: errors.New("nss sync could not connect"),
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			db := testdb.New(t)
			fake := &fakeLDAPDiagnostics{run: &model.LDAPSyncRun{ID: "run-1", StartedAt: time.Now()}}
			identity := &service.Identity{Subject: "sub-alice", Username: "alice", Groups: []string{"ssh-admins"}}
			r := ldapRouterWithNSS(t, ldapAdminTestConfig(), db, identity, fake, tt.nss)

			w := doLDAPRequest(r, http.MethodPost, "/admin/ldap/sync", webtypes.LDAPSyncRequestBody{})
			var got webtypes.LDAPSyncRunResponse
			decodeEnvelope(t, w.Body.Bytes(), &got)
			wantJSON, err := json.Marshal(got.NSS)
			if err != nil {
				t.Fatalf("encode the response's nss: %v", err)
			}

			events := auditEventsWithAction(t, db, string(service.AuditLDAPSyncTriggered))
			var payload struct {
				Detail map[string]json.RawMessage `json:"detail"`
			}
			if len(events) != 1 || json.Unmarshal([]byte(events[0].Payload), &payload) != nil {
				t.Fatalf("sync events = %+v, want one", events)
			}
			if string(payload.Detail["nss"]) != string(wantJSON) {
				t.Errorf("audit nss = %s, want the response's %s", payload.Detail["nss"], wantJSON)
			}
		})
	}
}
