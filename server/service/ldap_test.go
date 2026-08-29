package service

// Test methodology: the directory is faked at the ldapConn seam, so the
// enrichment and sync logic is exercised end to end against a real
// database without a live LDAP server. The properties worth pinning are
// the ones a directory outage or a config mistake would otherwise break
// quietly: fail-open login, outage-never-disables, and the per-field
// override rule.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// fakeDirectory answers searches from a fixed set of entries.
type fakeDirectory struct {
	// entries answers by DN for base-scope reads and by any filter for
	// subtree reads; the tests that need finer control use searchFn.
	entries []*ldap.Entry
	// searchFn, when set, answers every search instead.
	searchFn func(*ldap.SearchRequest) (*ldap.SearchResult, error)
	// dialErr, when set, makes the dialer fail — a directory outage.
	dialErr error
	// searches records every filter the code issued.
	searches []string
	closed   bool
}

func (f *fakeDirectory) Search(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
	f.searches = append(f.searches, req.Filter)
	if f.searchFn != nil {
		return f.searchFn(req)
	}
	// A base-scope read answers by DN; a subtree search answers with
	// everything, which is enough for a filter that names one person.
	if req.Scope == ldap.ScopeBaseObject {
		for _, e := range f.entries {
			if e.DN == req.BaseDN {
				return &ldap.SearchResult{Entries: []*ldap.Entry{e}}, nil
			}
		}
		return nil, ldap.NewError(ldap.LDAPResultNoSuchObject, errors.New("no such object"))
	}
	return &ldap.SearchResult{Entries: f.entries}, nil
}

func (f *fakeDirectory) Close() error { f.closed = true; return nil }

// dialer returns a dialer serving this fake.
func (f *fakeDirectory) dialer() ldapDialer {
	return func(*config.LDAPConfig) (ldapConn, error) {
		if f.dialErr != nil {
			return nil, f.dialErr
		}
		return f, nil
	}
}

// entry builds a directory entry with the given attributes.
func entry(dn string, attrs map[string][]string) *ldap.Entry {
	e := &ldap.Entry{DN: dn}
	for name, values := range attrs {
		e.Attributes = append(e.Attributes, &ldap.EntryAttribute{Name: name, Values: values})
	}
	return e
}

// ldapTestConfig builds a config with LDAP enabled and the given fields.
func ldapTestConfig(fields map[string]config.LDAPField) *config.Config {
	c := &config.Config{}
	c.LDAP = config.LDAPConfig{
		Enabled:    true,
		URL:        "ldaps://directory.test",
		BaseDN:     "ou=people,dc=test",
		UserFilter: "(&(objectClass=person)(uid={{.Username}}))",
		Fields:     fields,
		Timeout:    5 * time.Second,
		Sync:       config.LDAPSync{Interval: time.Minute, DisableAfter: 3, Reenable: true},
		Limits: config.LDAPLimits{
			MaxValuesPerAttribute: 1000,
			MaxEntriesPerSearch:   1000,
			MaxAttributesBytes:    65536,
		},
	}
	c.Admin.RequireGroup = "ssh-admins"
	c.Admin.SOCGroup = "soc"
	return c
}

// newLDAPTestService builds a service over a migrated database and a fake
// directory.
func newLDAPTestService(t *testing.T, cfg *config.Config, dir *fakeDirectory) (*LDAPService, *gorm.DB) {
	t.Helper()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.UserLDAP{}, &model.UserGroup{}, &model.AuditEvent{}); err != nil {
		t.Fatalf("failed to migrate the LDAP test database: %v", err)
	}

	svc, err := NewLDAPService(cfg, db)
	if err != nil {
		t.Fatalf("NewLDAPService() error = %v", err)
	}
	if svc == nil {
		t.Fatal("NewLDAPService() returned nil for an enabled config")
	}
	svc.dial = dir.dialer()
	return svc, db
}

// seedLDAPUser creates a users row and returns its id.
func seedLDAPUser(t *testing.T, db *gorm.DB, subject, username string) string {
	t.Helper()

	user := model.User{
		ID: "u-" + subject, Subject: subject, Username: username,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	return user.ID
}

func TestNewLDAPService_ShouldReturnNilWhenDisabled(t *testing.T) {
	t.Parallel()

	svc, err := NewLDAPService(&config.Config{}, nil)
	if err != nil {
		t.Fatalf("NewLDAPService() error = %v", err)
	}
	if svc != nil {
		t.Error("expected nil for a disabled directory, so callers need no config branch")
	}
	// And every method tolerates the nil, which is what makes that work.
	svc.Enrich(context.Background(), &Identity{}, "u-1")
	if err := svc.Sync(context.Background()); err != nil {
		t.Errorf("Sync() on a nil service error = %v", err)
	}
}

func TestNewLDAPService_ShouldRejectABadFilterTemplate(t *testing.T) {
	t.Parallel()

	cfg := ldapTestConfig(nil)
	cfg.LDAP.UserFilter = "(uid={{.Username)"
	if _, err := NewLDAPService(cfg, nil); err == nil {
		t.Error("NewLDAPService() error = nil, want a startup failure for a bad filter")
	}
}

// The per-field rule: a configured LDAP field wins over the OIDC value, and
// an unconfigured one leaves it alone. Override rather than union, because
// union makes it impossible to retire a stale principal from one source.
func TestEnrich_ShouldOverrideConfiguredFieldsAndLeaveOthers(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{
		entry("uid=alice,ou=people,dc=test", map[string][]string{
			"sAMAccountName":   {"a.smith", "alice.smith"},
			"departmentNumber": {"eng"},
		}),
	}}
	cfg := ldapTestConfig(map[string]config.LDAPField{
		"other_accounts": {Attribute: "sAMAccountName"},
		"dept":           {Attribute: "departmentNumber"},
	})
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")

	identity := &Identity{
		Subject:  "sub-alice",
		Username: "alice",
		// Configured in LDAP, so it is replaced entirely.
		OtherAccounts: []string{"stale-account"},
		// Not configured in LDAP, so it survives untouched.
		ServiceAccounts: []string{"svc-deploy"},
	}
	svc.Enrich(context.Background(), identity, userID)

	if len(identity.OtherAccounts) != 2 || identity.OtherAccounts[0] != "a.smith" {
		t.Errorf("other_accounts = %v, want the directory values to replace the OIDC one", identity.OtherAccounts)
	}
	if len(identity.ServiceAccounts) != 1 || identity.ServiceAccounts[0] != "svc-deploy" {
		t.Errorf("service_accounts = %v, want the OIDC value untouched", identity.ServiceAccounts)
	}
	// A non-reserved name is an extra template field.
	if got, ok := identity.Extra["dept"]; !ok {
		t.Error("expected the dept field to be captured as an extra")
	} else if s, _ := got.Scalar(); s != "eng" {
		t.Errorf("dept = %q, want eng", s)
	}
}

// Login fails open: a directory the server cannot reach must not stop
// anyone signing in.
func TestEnrich_ShouldFailOpenWhenTheDirectoryIsUnreachable(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{dialErr: errors.New("connection refused")}
	cfg := ldapTestConfig(map[string]config.LDAPField{
		"other_accounts": {Attribute: "sAMAccountName"},
	})
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")

	identity := &Identity{Subject: "sub-alice", Username: "alice", OtherAccounts: []string{"from-oidc"}}
	svc.Enrich(context.Background(), identity, userID)

	if len(identity.OtherAccounts) != 1 || identity.OtherAccounts[0] != "from-oidc" {
		t.Errorf("other_accounts = %v, want the OIDC identity to survive an outage", identity.OtherAccounts)
	}
}

// An outage should cost freshness, not principals: the last successful read
// is the fallback.
func TestEnrich_ShouldFallBackToTheCachedAttributes(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{
		entry("uid=alice,ou=people,dc=test", map[string][]string{"sAMAccountName": {"a.smith"}}),
	}}
	cfg := ldapTestConfig(map[string]config.LDAPField{
		"other_accounts": {Attribute: "sAMAccountName"},
	})
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")

	// A first, successful login populates the cache.
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	// The directory then goes away.
	dir.dialErr = errors.New("connection refused")
	identity := &Identity{Subject: "sub-alice", Username: "alice"}
	svc.Enrich(context.Background(), identity, userID)

	if len(identity.OtherAccounts) != 1 || identity.OtherAccounts[0] != "a.smith" {
		t.Errorf("other_accounts = %v, want the cached directory value", identity.OtherAccounts)
	}
}

// Groups are the exception to the override rule: they persist alongside the
// OIDC ones and never reach the session identity.
func TestEnrich_ShouldPersistAllowedGroupsWithoutTouchingTheIdentity(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{
		entry("uid=alice,ou=people,dc=test", map[string][]string{
			"memberOf": {
				"cn=soc,ou=groups,dc=test",             // allowlisted, reduced from a DN
				"cn=some-other-team,ou=groups,dc=test", // not referenced anywhere
			},
		}),
	}}
	cfg := ldapTestConfig(map[string]config.LDAPField{"groups": {Attribute: "memberOf"}})
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")

	identity := &Identity{Subject: "sub-alice", Username: "alice", Groups: []string{"from-oidc"}}
	svc.Enrich(context.Background(), identity, userID)

	if len(identity.Groups) != 1 || identity.Groups[0] != "from-oidc" {
		t.Errorf("identity groups = %v, want the OIDC claim untouched", identity.Groups)
	}

	var rows []model.UserGroup
	if err := db.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		t.Fatalf("load group rows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d group rows, want only the allowlisted one: %+v", len(rows), rows)
	}
	if rows[0].GroupName != "soc" {
		t.Errorf("group name = %q, want the DN reduced to soc", rows[0].GroupName)
	}
	if rows[0].Source != model.GroupSourceLDAP {
		t.Errorf("source = %q, want ldap", rows[0].Source)
	}
}

// The rule the whole sync design rests on: an unreachable directory must
// never disable anyone. Only a successful search finding nothing counts.
func TestSync_ShouldNotCountAMissWhenTheDirectoryIsUnreachable(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{
		entry("uid=alice,ou=people,dc=test", nil),
	}}
	cfg := ldapTestConfig(nil)
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	dir.dialErr = errors.New("connection refused")
	for range 5 {
		if err := svc.Sync(context.Background()); err == nil {
			t.Fatal("Sync() error = nil, want the connection failure surfaced")
		}
	}

	var row model.UserLDAP
	if err := db.First(&row, "user_id = ?", userID).Error; err != nil {
		t.Fatalf("load bookkeeping: %v", err)
	}
	if row.ConsecutiveMisses != 0 {
		t.Errorf("consecutive_misses = %d, want 0: an outage is not evidence about a user", row.ConsecutiveMisses)
	}

	var user model.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.DisabledAt != nil {
		t.Error("the user was disabled by a directory outage")
	}
}

// A search that succeeds and finds nothing is a miss, and enough of them
// disable the account.
func TestSync_ShouldDisableAfterTheConfiguredMisses(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{
		entry("uid=alice,ou=people,dc=test", nil),
	}}
	cfg := ldapTestConfig(nil)
	svc, db := newLDAPTestService(t, cfg, dir)
	svc.SetAuditor(NewAuditService(cfg, db))
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	// The entry disappears: every search now succeeds with no results.
	dir.entries = nil
	dir.searchFn = func(*ldap.SearchRequest) (*ldap.SearchResult, error) {
		return &ldap.SearchResult{}, nil
	}

	for i := 1; i <= cfg.LDAP.Sync.DisableAfter; i++ {
		if err := svc.Sync(context.Background()); err != nil {
			t.Fatalf("Sync() error = %v", err)
		}

		var user model.User
		if err := db.First(&user, "id = ?", userID).Error; err != nil {
			t.Fatalf("load user: %v", err)
		}
		disabled := user.DisabledAt != nil
		wantDisabled := i >= cfg.LDAP.Sync.DisableAfter
		if disabled != wantDisabled {
			t.Fatalf("after %d misses disabled = %v, want %v", i, disabled, wantDisabled)
		}
	}

	var user model.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.DisabledSource == nil || *user.DisabledSource != model.DisabledSourceLDAPSync {
		t.Errorf("disabled_source = %v, want ldap_sync so the sync may clear it later", user.DisabledSource)
	}
	if user.DisabledReason == "" {
		t.Error("an automatic disable recorded no reason")
	}

	// The containment action is audited like any other.
	var events []model.AuditEvent
	if err := db.Where("target_user_id = ?", userID).Find(&events).Error; err != nil {
		t.Fatalf("load audit events: %v", err)
	}
	if len(events) == 0 {
		t.Error("the auto-disable was not audited")
	}
}

// The sync clears only its own disables. An operator's disable is never
// undone automatically, in either direction.
func TestSync_ShouldOnlyClearItsOwnDisables(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		source       *model.DisabledSource
		wantReenable bool
	}{
		{name: "its own disable is cleared", source: ptr(model.DisabledSourceLDAPSync), wantReenable: true},
		{name: "an admin disable is left alone", source: ptr(model.DisabledSourceAdmin), wantReenable: false},
		{name: "a SOC disable is left alone", source: ptr(model.DisabledSourceSOC), wantReenable: false},
		// A row predating the column can never match the exact-match rule,
		// which is the safe direction and why the migration backfills
		// nothing.
		{name: "a disable with no recorded source is left alone", source: nil, wantReenable: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := &fakeDirectory{entries: []*ldap.Entry{
				entry("uid=alice,ou=people,dc=test", nil),
			}}
			cfg := ldapTestConfig(nil)
			svc, db := newLDAPTestService(t, cfg, dir)
			userID := seedLDAPUser(t, db, "sub-alice", "alice")
			svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

			disabledAt := time.Now()
			if err := db.Model(&model.User{}).Where("id = ?", userID).
				Updates(map[string]any{
					"disabled_at":     disabledAt,
					"disabled_source": tt.source,
					"disabled_reason": "prior disable",
				}).Error; err != nil {
				t.Fatalf("disable the user: %v", err)
			}

			// The entry is present, so the sync finds them.
			if err := svc.Sync(context.Background()); err != nil {
				t.Fatalf("Sync() error = %v", err)
			}

			var user model.User
			if err := db.First(&user, "id = ?", userID).Error; err != nil {
				t.Fatalf("load user: %v", err)
			}
			reenabled := user.DisabledAt == nil
			if reenabled != tt.wantReenable {
				t.Errorf("re-enabled = %v, want %v", reenabled, tt.wantReenable)
			}
		})
	}
}

// reenable: false means the sync never restores an account, even one it
// disabled itself.
func TestSync_ShouldNotReenableWhenTheOptionIsOff(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{entry("uid=alice,ou=people,dc=test", nil)}}
	cfg := ldapTestConfig(nil)
	cfg.LDAP.Sync.Reenable = false
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	now := time.Now()
	source := model.DisabledSourceLDAPSync
	if err := db.Model(&model.User{}).Where("id = ?", userID).
		Updates(map[string]any{"disabled_at": now, "disabled_source": source}).Error; err != nil {
		t.Fatalf("disable the user: %v", err)
	}

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	var user model.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.DisabledAt == nil {
		t.Error("the user was re-enabled despite reenable being off")
	}
}

// A found entry clears the counter, so intermittent misses never
// accumulate into a disable.
func TestSync_ShouldResetTheMissCounterWhenTheEntryReappears(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{entry("uid=alice,ou=people,dc=test", nil)}}
	cfg := ldapTestConfig(nil)
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	// One miss.
	dir.entries = nil
	dir.searchFn = func(*ldap.SearchRequest) (*ldap.SearchResult, error) {
		return &ldap.SearchResult{}, nil
	}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	var row model.UserLDAP
	if err := db.First(&row, "user_id = ?", userID).Error; err != nil {
		t.Fatalf("load bookkeeping: %v", err)
	}
	if row.ConsecutiveMisses != 1 {
		t.Fatalf("consecutive_misses = %d, want 1", row.ConsecutiveMisses)
	}

	// Then it comes back.
	dir.searchFn = nil
	dir.entries = []*ldap.Entry{entry("uid=alice,ou=people,dc=test", nil)}
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	if err := db.First(&row, "user_id = ?", userID).Error; err != nil {
		t.Fatalf("reload bookkeeping: %v", err)
	}
	if row.ConsecutiveMisses != 0 {
		t.Errorf("consecutive_misses = %d, want the counter reset", row.ConsecutiveMisses)
	}
}

// A moved entry must re-anchor rather than be treated as gone: the DN read
// fails, the filter search finds it, and the stored DN is updated.
func TestSync_ShouldReanchorAMovedEntry(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{entry("uid=alice,ou=people,dc=test", nil)}}
	cfg := ldapTestConfig(nil)
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	// The entry moves to a new DN. The base read by the old DN now fails,
	// but the filter still finds it.
	dir.entries = []*ldap.Entry{entry("uid=alice,ou=contractors,dc=test", nil)}

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	var row model.UserLDAP
	if err := db.First(&row, "user_id = ?", userID).Error; err != nil {
		t.Fatalf("load bookkeeping: %v", err)
	}
	if row.DN != "uid=alice,ou=contractors,dc=test" {
		t.Errorf("dn = %q, want the moved entry's new DN", row.DN)
	}
	if row.ConsecutiveMisses != 0 {
		t.Errorf("consecutive_misses = %d, want 0: a move is not a disappearance", row.ConsecutiveMisses)
	}
}

// ptr is a small helper for the table above.
func ptr[T any](v T) *T { return &v }

// The four account-linking topologies the config has to express. Only the
// first is an attribute on the person's own entry; the rest are searches
// over entries that link back, which is why searches exist at all.
func TestEnrich_ShouldResolveEveryAccountLinkingTopology(t *testing.T) {
	t.Parallel()

	primary := entry("uid=alice,ou=people,dc=test", map[string][]string{
		"sAMAccountName": {"a.smith"}, // topology 1: forward list
		"employeeNumber": {"E-40921"}, // the key topology 3 links by
	})
	adminAccount := entry("uid=alice-adm,ou=admin,dc=test", map[string][]string{"uid": {"alice-adm"}})
	byNumber := entry("uid=alice-ops,ou=admin,dc=test", map[string][]string{"uid": {"alice-ops"}})
	owned := entry("uid=svc-deploy,ou=services,dc=test", map[string][]string{"uid": {"svc-deploy"}})

	dir := &fakeDirectory{}
	dir.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
		switch {
		case strings.Contains(req.Filter, "objectClass=person") && strings.Contains(req.Filter, "uid=alice)"):
			return &ldap.SearchResult{Entries: []*ldap.Entry{primary}}, nil
		case strings.Contains(req.Filter, "authorizedUser=alice"):
			// Topology 2: reverse link by username.
			return &ldap.SearchResult{Entries: []*ldap.Entry{adminAccount}}, nil
		case strings.Contains(req.Filter, "linkedEmployee=E-40921"):
			// Topology 3: reverse link by another identifier, whose
			// attribute the primary lookup fetched automatically.
			return &ldap.SearchResult{Entries: []*ldap.Entry{byNumber}}, nil
		case strings.Contains(req.Filter, "owner=uid=alice,ou=people,dc=test"):
			// Topology 4: an ownership link, placed under a different field
			// from the authorized-user link over the same entries.
			return &ldap.SearchResult{Entries: []*ldap.Entry{owned}}, nil
		}
		return &ldap.SearchResult{}, nil
	}

	cfg := ldapTestConfig(map[string]config.LDAPField{
		"other_accounts": {
			Attribute: "sAMAccountName",
			Searches: []config.LDAPFieldSearch{
				{Name: "admin accounts", Filter: "(authorizedUser={{.Username}})", Value: "uid"},
				{Name: "by employee number", Filter: "(linkedEmployee={{.Attr.employeeNumber}})", Value: "uid"},
			},
		},
		"service_accounts": {
			Searches: []config.LDAPFieldSearch{
				{Name: "owned services", Filter: "(owner={{.DN}})", Value: "uid"},
			},
		},
	})
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")

	identity := &Identity{Subject: "sub-alice", Username: "alice"}
	svc.Enrich(context.Background(), identity, userID)

	// Everything under one field unions and dedupes.
	want := []string{"a.smith", "alice-adm", "alice-ops"}
	if len(identity.OtherAccounts) != len(want) {
		t.Fatalf("other_accounts = %v, want %v", identity.OtherAccounts, want)
	}
	for i, w := range want {
		if identity.OtherAccounts[i] != w {
			t.Errorf("other_accounts = %v, want %v", identity.OtherAccounts, want)
			break
		}
	}

	// The ownership link feeds a different field, which is how the operator
	// says which link means which.
	if len(identity.ServiceAccounts) != 1 || identity.ServiceAccounts[0] != "svc-deploy" {
		t.Errorf("service_accounts = %v, want [svc-deploy]", identity.ServiceAccounts)
	}
}

// A value cap protects the database from a pathological directory, and the
// truncation is logged rather than silent.
func TestEnrich_ShouldCapAMultiValuedAttribute(t *testing.T) {
	t.Parallel()

	many := make([]string, 50)
	for i := range many {
		many[i] = fmt.Sprintf("acct-%02d", i)
	}
	dir := &fakeDirectory{entries: []*ldap.Entry{
		entry("uid=alice,ou=people,dc=test", map[string][]string{"sAMAccountName": many}),
	}}
	cfg := ldapTestConfig(map[string]config.LDAPField{
		"other_accounts": {Attribute: "sAMAccountName"},
	})
	cfg.LDAP.Limits.MaxValuesPerAttribute = 10
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")

	identity := &Identity{Subject: "sub-alice", Username: "alice"}
	svc.Enrich(context.Background(), identity, userID)

	if len(identity.OtherAccounts) != 10 {
		t.Errorf("other_accounts has %d values, want the cap of 10", len(identity.OtherAccounts))
	}
}
