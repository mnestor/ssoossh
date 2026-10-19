package service

// Test methodology: ldap.authoritative_role_groups end to end against a
// real database and the fake directory from ldap_test.go. The properties
// pinned are the ones the setting promises: it only narrows, only for a
// linked user, only by rows a successful read wrote, and the change is
// visible once in the audit log.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// aliceDN is the directory entry every test here resolves.
const aliceDN = "uid=alice,ou=people,dc=test"

// roleTestConfig is ldapTestConfig with every role group configured,
// groups read from memberOf, and ldap.authoritative_role_groups as given.
func roleTestConfig(authoritative bool) *config.Config {
	cfg := ldapTestConfig(map[string]config.LDAPField{config.LDAPFieldGroups: {Attribute: "memberOf"}})
	cfg.Admin.AuditorGroup = "auditors"
	cfg.Admin.NSSKeyGroup = "key-admins"
	cfg.LDAP.AuthoritativeRoleGroups = authoritative
	return cfg
}

// memberOf builds the memberOf attribute of an entry in the named groups.
func memberOf(groups ...string) map[string][]string {
	values := make([]string, 0, len(groups))
	for _, g := range groups {
		values = append(values, "cn="+g+",ou=groups,dc=test")
	}
	return map[string][]string{"memberOf": values}
}

// directoryLink says how a test user is tied to the directory.
type directoryLink int

const (
	linkResolved directoryLink = iota // a user_ldap row with a DN
	linkNone                          // no user_ldap row
	linkEmpty                         // a row with neither DN nor directory id
)

// TestNarrowRoleGroups covers the narrowing rule as the database sees it:
// which rows count, which users are linked, and that nothing is added.
func TestNarrowRoleGroups(t *testing.T) {
	t.Parallel()

	allRoles := []string{"ssh-admins", "soc", "auditors", "key-admins"}
	tests := []struct {
		name          string
		authoritative bool
		link          directoryLink
		directory     []string
		groups        []string
		want          []string
	}{
		{name: "should keep every role group when the directory lists each", authoritative: true, directory: allRoles, groups: []string{"ssh-admins", "soc", "auditors", "key-admins", "dev"}, want: []string{"ssh-admins", "soc", "auditors", "key-admins", "dev"}},
		{name: "should remove every role group the directory does not list", authoritative: true, groups: []string{"ssh-admins", "soc", "auditors", "key-admins", "dev"}, want: []string{"dev"}},
		{name: "should remove only the admin group when the directory lists the others", authoritative: true, directory: []string{"soc", "auditors", "key-admins"}, groups: allRoles, want: []string{"soc", "auditors", "key-admins"}},
		{name: "should keep a role group the directory does not list when the setting is off", groups: []string{"ssh-admins"}, want: []string{"ssh-admins"}},
		{name: "should never add a role group only the directory lists", authoritative: true, directory: allRoles, groups: []string{"dev"}, want: []string{"dev"}},
		{name: "should leave an unlinked user untouched", authoritative: true, link: linkNone, groups: allRoles, want: allRoles},
		{name: "should leave a user whose row has no DN or directory id untouched", authoritative: true, link: linkEmpty, groups: []string{"ssh-admins"}, want: []string{"ssh-admins"}},
		{name: "should leave non-role groups untouched", authoritative: true, groups: []string{"dev", "ops"}, want: []string{"dev", "ops"}},
		{name: "should compare names exactly when the directory differs in case", authoritative: true, directory: []string{"SSH-Admins"}, groups: []string{"ssh-admins"}, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, db := newLDAPTestService(t, roleTestConfig(tt.authoritative), &fakeDirectory{})
			userID := seedLDAPUser(t, db, "sub-alice", "alice")
			switch tt.link {
			case linkResolved:
				seedUserLDAP(t, db, userID, nil)
			case linkEmpty:
				now := time.Now()
				if err := db.Create(&model.UserLDAP{UserID: userID, Attributes: "{}", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
					t.Fatalf("failed to seed the user_ldap row: %v", err)
				}
			case linkNone:
			}
			for _, g := range tt.directory {
				addGroupRow(t, db, userID, g, model.GroupSourceLDAP)
			}

			got, err := svc.NarrowRoleGroups(context.Background(), userID, tt.groups)
			if err != nil || !equalStrings(got, tt.want) {
				t.Errorf("NarrowRoleGroups(%v) = %v, %v, want %v and no error", tt.groups, got, err, tt.want)
			}
		})
	}
}

// With LDAP off (a nil service) or no users row to key on, nothing can be
// linked, so nothing is narrowed.
func TestNarrowRoleGroups_ShouldNarrowNothingWithoutADirectoryOrAUser(t *testing.T) {
	t.Parallel()

	svc, _ := newLDAPTestService(t, roleTestConfig(true), &fakeDirectory{})
	tests := []struct {
		name   string
		svc    *LDAPService
		userID string
	}{
		{name: "should narrow nothing when LDAP is off", svc: nil, userID: "u-sub-alice"},
		{name: "should narrow nothing when there is no user id", svc: svc, userID: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.svc.NarrowRoleGroups(context.Background(), tt.userID, []string{"ssh-admins"})
			if err != nil || !equalStrings(got, []string{"ssh-admins"}) {
				t.Errorf("NarrowRoleGroups() = %v, %v, want [ssh-admins] and no error", got, err)
			}
		})
	}
}

// A role that cannot be checked is not granted on trust: a failed read of
// the directory rows drops every role group and reports why.
func TestNarrowRoleGroups_ShouldDropEveryRoleGroupWhenTheRowsCannotBeRead(t *testing.T) {
	t.Parallel()

	svc, db := newLDAPTestService(t, roleTestConfig(true), &fakeDirectory{})
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	seedUserLDAP(t, db, userID, nil)
	if err := db.Migrator().DropTable(&model.UserGroup{}); err != nil {
		t.Fatalf("failed to drop user_groups: %v", err)
	}

	got, err := svc.NarrowRoleGroups(context.Background(), userID, []string{"ssh-admins", "dev"})
	if err == nil || !equalStrings(got, []string{"dev"}) {
		t.Errorf("NarrowRoleGroups() = %v, %v, want [dev] and an error", got, err)
	}
}

// roleGrant names one role and the check that grants it.
type roleGrant struct {
	role   string
	group  string
	grants func(*config.AdminConfig, []string) bool
}

// everyRole is the four role groups ldap.authoritative_role_groups covers.
var everyRole = []roleGrant{
	{role: "admin", group: "ssh-admins", grants: (*config.AdminConfig).GrantsAdmin},
	{role: "SOC", group: "soc", grants: (*config.AdminConfig).GrantsSOC},
	{role: "auditor", group: "auditors", grants: (*config.AdminConfig).GrantsAuditor},
	{role: "fleet key admin", group: "key-admins", grants: (*config.AdminConfig).GrantsNSSKeyAdmin},
}

// loggedInLinkedUser logs alice in through enrichment with the directory
// listing groups, so she is linked and her LDAP rows hold them.
func loggedInLinkedUser(t *testing.T, cfg *config.Config, groups ...string) (*fakeDirectory, *LDAPService, *gorm.DB, string) {
	t.Helper()

	dir := &fakeDirectory{entries: []*ldap.Entry{entry(aliceDN, memberOf(groups...))}}
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice", Groups: groups}, userID)
	return dir, svc, db, userID
}

// refreshedGroups runs one request's refresh over a session whose claim
// carries groups, and returns what the request acts on.
func refreshedGroups(t *testing.T, db *gorm.DB, svc *LDAPService, groups ...string) []string {
	t.Helper()

	identity := &Identity{Subject: "sub-alice", Username: "alice", Groups: groups}
	if err := NewIdentityService(db, svc).RefreshIdentity(context.Background(), identity); err != nil {
		t.Fatalf("RefreshIdentity() = %v, want no error", err)
	}
	return identity.Groups
}

// The point of the setting: a live session loses a role on its next
// request once the sync stops seeing the directory group, for every role.
func TestRefreshIdentity_ShouldTakeARoleAwayAfterTheSyncDropsIt(t *testing.T) {
	t.Parallel()

	for _, r := range everyRole {
		t.Run("should take "+r.role+" away on the next request when the sync no longer finds the group", func(t *testing.T) {
			t.Parallel()

			cfg := roleTestConfig(true)
			dir, svc, db, _ := loggedInLinkedUser(t, cfg, r.group)
			if !r.grants(&cfg.Admin, refreshedGroups(t, db, svc, r.group)) {
				t.Fatalf("the session does not hold %s before the sync", r.role)
			}

			dir.entries = []*ldap.Entry{entry(aliceDN, memberOf("unrelated"))}
			if err := svc.Sync(context.Background()); err != nil {
				t.Fatalf("Sync() = %v", err)
			}

			if got := refreshedGroups(t, db, svc, r.group); r.grants(&cfg.Admin, got) {
				t.Errorf("after the sync the session groups are %v, which still grant %s", got, r.role)
			}
		})
	}
}

// With the setting off the directory has no say over roles, whatever the
// sync finds.
func TestRefreshIdentity_ShouldKeepARoleTheSyncDroppedWhenTheSettingIsOff(t *testing.T) {
	t.Parallel()

	cfg := roleTestConfig(false)
	dir, svc, db, _ := loggedInLinkedUser(t, cfg, "ssh-admins")
	dir.entries = []*ldap.Entry{entry(aliceDN, memberOf("unrelated"))}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() = %v", err)
	}

	if got := refreshedGroups(t, db, svc, "ssh-admins"); !cfg.Admin.GrantsAdmin(got) {
		t.Errorf("session groups = %v, want admin kept with the setting off", got)
	}
}

// A role the directory lists again counts again on the next request,
// without another login: the session keeps the claim and narrows afresh.
func TestRefreshIdentity_ShouldGiveARoleBackWhenTheDirectoryListsItAgain(t *testing.T) {
	t.Parallel()

	cfg := roleTestConfig(true)
	dir, svc, db, _ := loggedInLinkedUser(t, cfg, "ssh-admins")
	dir.entries = []*ldap.Entry{entry(aliceDN, memberOf("unrelated"))}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	dir.entries = []*ldap.Entry{entry(aliceDN, memberOf("ssh-admins"))}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() = %v", err)
	}

	if got := refreshedGroups(t, db, svc, "ssh-admins"); !cfg.Admin.GrantsAdmin(got) {
		t.Errorf("session groups = %v, want admin back once the directory lists it", got)
	}
}

// A failure never revokes: a read that fails, at login or in the sync, and
// a sync that finds the entry missing all leave the rows, so the role
// stays.
func TestRoleGroups_ShouldStayWhenADirectoryReadDoesNotSucceed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		fail func(*fakeDirectory)
		read func(*LDAPService, string)
	}{
		{
			name: "should keep the role when the sync cannot reach the directory",
			fail: func(d *fakeDirectory) { d.dialErr = errors.New("connection refused") },
			read: func(s *LDAPService, _ string) { _ = s.Sync(context.Background()) },
		},
		{
			name: "should keep the role when every sync search fails",
			fail: func(d *fakeDirectory) {
				d.searchFn = func(*ldap.SearchRequest) (*ldap.SearchResult, error) { return nil, errors.New("busy") }
			},
			read: func(s *LDAPService, _ string) { _ = s.Sync(context.Background()) },
		},
		{
			name: "should keep the role when the sync finds the entry missing",
			fail: func(d *fakeDirectory) { d.entries = nil },
			read: func(s *LDAPService, _ string) { _ = s.Sync(context.Background()) },
		},
		{
			name: "should keep the role when the login lookup cannot reach the directory",
			fail: func(d *fakeDirectory) { d.dialErr = errors.New("connection refused") },
			read: func(s *LDAPService, userID string) {
				s.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice", Groups: []string{"ssh-admins"}}, userID)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := roleTestConfig(true)
			dir, svc, db, userID := loggedInLinkedUser(t, cfg, "ssh-admins")
			tt.fail(dir)
			tt.read(svc, userID)

			if got := refreshedGroups(t, db, svc, "ssh-admins"); !cfg.Admin.GrantsAdmin(got) {
				t.Errorf("session groups = %v, want admin kept after a read that did not succeed", got)
			}
		})
	}
}

// The role group the sync narrows by has to be stored at all: fleet key
// admin joined the allowlist with this setting.
func TestGroupAllowlist_ShouldStoreTheFleetKeyAdminGroup(t *testing.T) {
	t.Parallel()

	_, _, db, userID := loggedInLinkedUser(t, roleTestConfig(true), "key-admins")

	var count int64
	if err := db.Model(&model.UserGroup{}).
		Where("user_id = ? AND source = ? AND group_name = ?", userID, model.GroupSourceLDAP, "key-admins").
		Count(&count).Error; err != nil {
		t.Fatalf("failed to count group rows: %v", err)
	}
	if count != 1 {
		t.Errorf("got %d key-admins LDAP rows, want 1", count)
	}
}

// One audit event per directory read that takes away a role the user
// holds, never one per request or per unchanged pass.
func TestSync_ShouldAuditATakenRoleOncePerChange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		authoritative bool
		oidcHolds     bool
		after         []string
		want          int64
	}{
		{name: "should record one event when the sync takes a held role away", authoritative: true, oidcHolds: true, after: []string{"unrelated"}, want: 1},
		{name: "should record nothing when the setting is off", oidcHolds: true, after: []string{"unrelated"}},
		{name: "should record nothing when the user never held the role through OIDC", authoritative: true, after: []string{"unrelated"}},
		{name: "should record nothing when the directory still lists the role", authoritative: true, oidcHolds: true, after: []string{"ssh-admins"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := roleTestConfig(tt.authoritative)
			dir, svc, db, userID := loggedInLinkedUser(t, cfg, "ssh-admins")
			svc.SetAuditor(NewAuditService(cfg, db))
			if tt.oidcHolds {
				addGroupRow(t, db, userID, "ssh-admins", model.GroupSourceOIDC)
			}

			dir.entries = []*ldap.Entry{entry(aliceDN, memberOf(tt.after...))}
			for range 3 {
				if err := svc.Sync(context.Background()); err != nil {
					t.Fatalf("Sync() = %v", err)
				}
			}

			var count int64
			if err := db.Model(&model.AuditEvent{}).
				Where("action = ? AND target_user_id = ?", string(AuditUserRoleNarrowedByDirectory), userID).
				Count(&count).Error; err != nil {
				t.Fatalf("failed to count audit events: %v", err)
			}
			if count != tt.want {
				t.Errorf("got %d %s events, want %d", count, AuditUserRoleNarrowedByDirectory, tt.want)
			}
		})
	}
}

// The auto-disable email goes to whoever counts as an admin, narrowed the
// way a session is: an admin the directory had stopped listing is not
// emailed with the setting on.
func TestIsSyncAdmin_ShouldNarrowByTheDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		authoritative bool
		directory     []string
		want          bool
	}{
		{name: "should count an admin the directory still lists", authoritative: true, directory: []string{"ssh-admins"}, want: true},
		{name: "should not count an admin the directory no longer lists", authoritative: true, directory: []string{"soc"}},
		{name: "should count an admin the directory no longer lists when the setting is off", directory: []string{"soc"}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, db := newLDAPTestService(t, roleTestConfig(tt.authoritative), &fakeDirectory{})
			userID := seedLDAPUser(t, db, "sub-alice", "alice")
			seedUserLDAP(t, db, userID, nil)
			for _, g := range tt.directory {
				addGroupRow(t, db, userID, g, model.GroupSourceLDAP)
			}
			admins := &syncAdmins{loaded: true, groups: map[string][]string{userID: {"ssh-admins"}}}

			if got := svc.isSyncAdmin(context.Background(), admins, userID); got != tt.want {
				t.Errorf("isSyncAdmin() = %v, want %v", got, tt.want)
			}
		})
	}
}

// A request whose users row cannot be read cannot be checked against the
// directory, so with the setting on it goes without its role groups for
// that request, and the error is still reported for the log.
func TestRefreshIdentity_ShouldDropRoleGroupsWhenTheUserCannotBeRead(t *testing.T) {
	t.Parallel()

	svc, db := newLDAPTestService(t, roleTestConfig(true), &fakeDirectory{})
	seedLDAPUser(t, db, "sub-alice", "alice")
	// A cancelled context fails the users read on either backend.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	identity := &Identity{Subject: "sub-alice", Groups: []string{"ssh-admins", "dev"}}
	err := NewIdentityService(db, svc).RefreshIdentity(ctx, identity)
	if err == nil || !equalStrings(identity.Groups, []string{"dev"}) {
		t.Errorf("RefreshIdentity() = %v with groups %v, want an error and [dev]", err, identity.Groups)
	}
}
