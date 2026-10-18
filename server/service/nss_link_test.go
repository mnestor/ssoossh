package service

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// nssLinkHarness is an NSS harness with the LDAP service a login goes
// through attached to the same fake directory.
type nssLinkHarness struct {
	*nssHarness
	ldap *LDAPService
}

// newNSSLinkHarness builds the LDAP service over the NSS harness's
// database and directory, with a user_filter the fake entries match.
func newNSSLinkHarness(t *testing.T, mutate func(*config.Config)) *nssLinkHarness {
	t.Helper()
	cfg := nssTestConfig()
	cfg.LDAP.UserFilter = "(uid={{.Username}})"
	if mutate != nil {
		mutate(cfg)
	}
	h := newNSSHarness(t, cfg)
	ldapSvc, err := NewLDAPService(cfg, h.db)
	if err != nil {
		t.Fatalf("NewLDAPService() error = %v", err)
	}
	ldapSvc.dial = h.dir.dialer()
	ldapSvc.SetNSSDirectory(h.svc)
	return &nssLinkHarness{nssHarness: h, ldap: ldapSvc}
}

// login runs the login-time enrichment for a person.
func (h *nssLinkHarness) login(t *testing.T, userID, username string) {
	t.Helper()
	identity := &Identity{Subject: "sub-" + userID, Username: username}
	h.ldap.Enrich(t.Context(), identity, userID)
}

// seedPerson writes a users row.
func seedPerson(t *testing.T, db *gorm.DB, id, username string) string {
	t.Helper()
	user := model.User{ID: id, Subject: "sub-" + id, Username: username, CreatedAt: nssTestStart(), UpdatedAt: nssTestStart()}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user %s: %v", id, err)
	}
	return id
}

func TestNSSLinkLogin_ShouldLinkTheAccountWhenAPersonLogsIn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		synced   bool
		wantSync bool
	}{
		{name: "should link the stored account when the sync has seen it", synced: true},
		{name: "should read and link the account when the sync has not seen it", synced: false, wantSync: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSLinkHarness(t, nil)
			h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
			h.dir.put(nssGroupEntry("ops", 700, "ada"))
			if tt.synced {
				h.fullSync(t)
			}
			userID := seedPerson(t, h.db, "u-ada", "ada")

			h.login(t, userID, "ada")

			row := h.accountByName(t, "ada")
			if row.UserID == nil || *row.UserID != userID {
				t.Fatalf("user_id = %v, want %s", row.UserID, userID)
			}
			if tt.wantSync && !slices.Contains(h.dir.searched(), "(&(objectClass=posixGroup)(memberUid=ada))") {
				t.Errorf("searches = %v, want the account read on demand with its groups", h.dir.searched())
			}
			if got := h.members(t, 700); len(got) != 1 {
				t.Errorf("members = %v, want ada in ops", got)
			}
		})
	}
}

func TestNSSLinkLogin_ShouldNotLinkAnotherEntryWhenOnlyTheUidMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		synced bool
	}{
		{name: "should leave the stored account unlinked when it is another entry with the same uid", synced: true},
		{name: "should leave an account read on demand unlinked when it is another entry with the same uid", synced: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSLinkHarness(t, nil)
			// The person's login entry carries uid 1001 but is not a POSIX
			// account in scope; bob's posixAccount carries the same number.
			h.dir.put(nssAccountEntry("uid=ada,ou=staff,dc=test", "ada", 1001, 1001,
				map[string][]string{"objectClass": {"top", "inetOrgPerson"}}))
			h.dir.put(nssAccountEntry("", "bob", 1001, 1001, nil))
			if tt.synced {
				h.fullSync(t)
			}
			userID := seedPerson(t, h.db, "u-ada", "ada")

			h.login(t, userID, "ada")

			row := h.accountByName(t, "bob")
			if row.UserID != nil {
				t.Fatalf("bob's account user_id = %s, want it unlinked", *row.UserID)
			}
		})
	}
}

func TestNSSLinkLogin_ShouldRequestTheMappedAttributesWhenNSSIsOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		enabled bool
		want    bool
	}{
		{name: "should add the name and uid attributes to the primary read when NSS is on", enabled: true, want: true},
		{name: "should leave the primary read alone when NSS is off", enabled: false, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := nssTestConfig()
			cfg.NSS.Enabled = tt.enabled
			cfg.NSS.Attributes.UID = "posixUid"
			svc, err := NewLDAPService(cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got := slices.Contains(svc.primaryAttrs, "posixUid") && slices.Contains(svc.primaryAttrs, "uid"); got != tt.want {
				t.Errorf("primaryAttrs = %v, want the NSS attributes present: %v", svc.primaryAttrs, tt.want)
			}
		})
	}
}

func TestNSSLinkLogin_ShouldNeverMoveALinkWhenOneExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(*testing.T, *nssLinkHarness)
		wantLink string
	}{
		{
			name: "should leave the account alone when it is linked to someone else",
			setup: func(t *testing.T, h *nssLinkHarness) {
				linkAccount(t, h, "ada", seedPerson(t, h.db, "u-other", "other"))
			},
			wantLink: "u-other",
		},
		{
			name: "should not link a second account when the person already has one",
			setup: func(t *testing.T, h *nssLinkHarness) {
				h.dir.put(nssAccountEntry("", "ada-admin", 1002, 1002, nil))
				h.fullSync(t)
				linkAccount(t, h, "ada-admin", "u-ada")
			},
			wantLink: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSLinkHarness(t, nil)
			h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
			h.fullSync(t)
			seedPerson(t, h.db, "u-ada", "ada")
			tt.setup(t, h)

			h.login(t, "u-ada", "ada")

			got := ""
			if row := h.accountByName(t, "ada"); row.UserID != nil {
				got = *row.UserID
			}
			if got != tt.wantLink {
				t.Errorf("ada linked to %q, want %q", got, tt.wantLink)
			}
		})
	}
}

// linkAccount links the stored account named name to userID directly.
func linkAccount(t *testing.T, h *nssLinkHarness, name, userID string) {
	t.Helper()
	if err := h.db.Model(&model.PosixUser{}).Where("name = ?", name).Update("user_id", userID).Error; err != nil {
		t.Fatalf("link %s: %v", name, err)
	}
}

func TestNSSLinkLogin_ShouldRaiseANameMismatchOnceADayWhenTheUsernameDiffers(t *testing.T) {
	t.Parallel()

	h := newNSSLinkHarness(t, nil)
	h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
	h.fullSync(t)
	userID := seedPerson(t, h.db, "u-ada", "Ada")

	steps := []struct {
		name       string
		advance    time.Duration
		wantAlerts int64
	}{
		{name: "should alert when the first login links an account named differently", wantAlerts: 1},
		{name: "should not alert again on a second login the same day", advance: time.Hour, wantAlerts: 1},
		{name: "should alert again when a day has passed", advance: 24 * time.Hour, wantAlerts: 2},
	}
	for _, step := range steps {
		h.clock.advance(step.advance)
		// The fake directory matches uid case-insensitively, as most do.
		h.login(t, userID, "Ada")
		if got := h.audits(t, AuditNSSNameMismatch); got != step.wantAlerts {
			t.Errorf("%s: nss.name_mismatch events = %d, want %d", step.name, got, step.wantAlerts)
		}
	}
	if row := h.accountByName(t, "ada"); row.UserID == nil || *row.UserID != userID {
		t.Errorf("ada user_id = %v, want the person linked", row.UserID)
	}
}

func TestNSSLinkLogin_ShouldLetTheLoginGoOnWhenTheNSSReadFails(t *testing.T) {
	t.Parallel()

	h := newNSSLinkHarness(t, func(c *config.Config) {
		c.LDAP.Fields = map[string]config.LDAPField{config.LDAPFieldName: {Attribute: "cn"}}
	})
	h.dir.put(nssAccountEntry("", "ada", 1001, 1001, map[string][]string{"cn": {"Ada Lovelace"}}))
	h.dir.fail = func(req *ldap.SearchRequest) error {
		if strings.Contains(req.Filter, "uidNumber=") {
			return ldap.NewError(ldap.LDAPResultBusy, errors.New("busy"))
		}
		return nil
	}
	userID := seedPerson(t, h.db, "u-ada", "ada")

	identity := &Identity{Subject: "sub-u-ada", Username: "ada"}
	h.ldap.Enrich(t.Context(), identity, userID)

	if identity.DisplayName != "Ada Lovelace" {
		t.Errorf("display name = %q, want the enrichment applied despite the NSS failure", identity.DisplayName)
	}
	if n := h.count(t, &model.PosixUser{}, ""); n != 0 {
		t.Errorf("stored accounts = %d, want none after the failed read", n)
	}
}

func TestNSSLinkLogin_ShouldLinkNothingWhenTheEntryHasNoUsableUid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		extra map[string][]string
	}{
		{name: "should link nothing when the entry has no uid attribute", extra: map[string][]string{"uidNumber": nil}},
		{name: "should link nothing when the uid is not a number", extra: map[string][]string{"uidNumber": {"x"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newNSSLinkHarness(t, nil)
			h.dir.put(nssAccountEntry("", "ada", 1001, 1001, tt.extra))
			h.login(t, seedPerson(t, h.db, "u-ada", "ada"), "ada")
			if n := h.count(t, &model.PosixUser{}, "user_id IS NOT NULL"); n != 0 {
				t.Errorf("linked accounts = %d, want none", n)
			}
		})
	}
}
