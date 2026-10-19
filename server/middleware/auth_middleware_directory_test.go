package middleware

// Test methodology: unlike auth_middleware_test.go, the identity here comes
// through a real session cookie and the real refresh
// (service.IdentityService over a migrated database), because the property
// under test spans both: with ldap.authoritative_role_groups on, a role the
// directory no longer lists must be refused by the role middleware on the
// very next request, though the session cookie still carries the claim.

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/test/testdb"
)

// directoryRoleConfig is a config with every role group, LDAP on, groups
// read from memberOf, and ldap.authoritative_role_groups on.
func directoryRoleConfig() *config.Config {
	c := adminConfig("ssh-admins", "soc", "auditors")
	c.LDAP = config.LDAPConfig{
		Enabled:                 true,
		URL:                     "ldaps://directory.test",
		BaseDN:                  "ou=people,dc=test",
		UserFilter:              "(uid={{.Username}})",
		Fields:                  map[string]config.LDAPField{config.LDAPFieldGroups: {Attribute: "memberOf"}},
		Timeout:                 5 * time.Second,
		AuthoritativeRoleGroups: true,
	}
	return c
}

// seedLinkedUser writes alice's users row and a resolved directory entry
// whose latest read listed directoryGroups.
func seedLinkedUser(t *testing.T, db *gorm.DB, directoryGroups ...string) {
	t.Helper()

	now := time.Now()
	user := model.User{ID: "u-alice", Subject: "sub-alice", Username: "alice", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed the user: %v", err)
	}
	row := model.UserLDAP{UserID: user.ID, DN: "uid=alice,ou=people,dc=test", Attributes: "{}", LastSeenAt: &now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("failed to seed the user_ldap row: %v", err)
	}
	for _, g := range directoryGroups {
		group := model.UserGroup{ID: uuid.NewString(), UserID: user.ID, GroupName: g, Source: model.GroupSourceLDAP, FirstSeenAt: now, LastSeenAt: now}
		if err := db.Create(&group).Error; err != nil {
			t.Fatalf("failed to seed a group row: %v", err)
		}
	}
}

// TestRoleMiddleware_ShouldRefuseARoleTheDirectoryNoLongerLists drives a
// logged-in session whose claim carries the role group at each role's
// guard, with the latest directory read either listing it or not.
func TestRoleMiddleware_ShouldRefuseARoleTheDirectoryNoLongerLists(t *testing.T) {
	t.Parallel()

	cfg := directoryRoleConfig()
	guards := []struct {
		role  string
		group string
		guard gin.HandlerFunc
	}{
		{role: "admin", group: "ssh-admins", guard: NewAdminAuthMiddleware(cfg).Add()},
		{role: "SOC", group: "soc", guard: NewSOCAuthMiddleware(cfg).Add()},
		{role: "auditor", group: "auditors", guard: NewAuditorAuthMiddleware(cfg).Add()},
	}
	for _, g := range guards {
		for _, listed := range []bool{true, false} {
			name := "should refuse the " + g.role + " route when the directory no longer lists the group"
			want := http.StatusForbidden
			if listed {
				name = "should allow the " + g.role + " route when the directory still lists the group"
				want = http.StatusOK
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				db := testdb.New(t)
				if listed {
					seedLinkedUser(t, db, g.group)
				} else {
					seedLinkedUser(t, db)
				}
				directory, err := service.NewLDAPService(cfg, db)
				if err != nil {
					t.Fatalf("NewLDAPService() error = %v", err)
				}

				login := doSessionRequest(t, func(c *gin.Context) {
					if err := SetIdentitySession(c, &service.Identity{Subject: "sub-alice", Username: "alice", Groups: []string{g.group}}); err != nil {
						t.Fatalf("SetIdentitySession() error = %v", err)
					}
				}, nil)

				r := newSessionTestRouter()
				r.Use(NewErrorHandlerMiddleware().Add())
				sessionAuth := NewSessionAuthMiddleware(time.Hour, 2*time.Hour, service.NewIdentityService(db, directory)).Add()
				r.GET("/guarded", sessionAuth, g.guard, func(c *gin.Context) { c.String(http.StatusOK, "ok") })
				req := httptest.NewRequest(http.MethodGet, "/guarded", nil)
				for _, c := range login.Result().Cookies() {
					req.AddCookie(c)
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)

				if w.Code != want {
					t.Errorf("got status %d, want %d", w.Code, want)
				}
			})
		}
	}
}
