package service

// Test methodology: the failure and race branches of the directory sync,
// on the same fakeDirectory seam ldap_test.go uses. The property every
// case guards is the sync's one rule — only a search that succeeds and
// finds nothing is evidence about a user — plus the two race rules: an
// operator's disable is never overwritten and never cleared.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/mnestor/ssoossh/server/model"
)

// A sync with nobody to sync must not even dial: with no user_ldap rows the
// configured-but-unused directory stays untouched.
func TestSync_ShouldDoNothingWithNoSyncedUsers(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{dialErr: errors.New("must not dial")}
	svc, _ := newLDAPTestService(t, ldapTestConfig(nil), dir)

	if err := svc.Sync(context.Background()); err != nil {
		t.Errorf("Sync() with no rows = %v, want nil without dialing", err)
	}
}

// A nil service is what a deployment without LDAP holds; the scheduler must
// be able to call it.
func TestSync_ShouldAcceptANilService(t *testing.T) {
	t.Parallel()

	var svc *LDAPService
	if err := svc.Sync(context.Background()); err != nil {
		t.Errorf("Sync() on a nil service = %v, want nil", err)
	}
}

func TestSync_ShouldReportADatabaseFailure(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{}
	svc, db := newLDAPTestService(t, ldapTestConfig(nil), dir)
	closeDB(t, db)

	if err := svc.Sync(context.Background()); err == nil {
		t.Error("Sync() on a closed database returned no error")
	}
}

// A bookkeeping row whose user is gone is skipped: nothing to sync, and not
// a miss — there is no account left to disable.
func TestSync_ShouldSkipABookkeepingRowWithNoUser(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{}
	svc, db := newLDAPTestService(t, ldapTestConfig(nil), dir)

	row := model.UserLDAP{UserID: "vanished", DN: "uid=ghost,ou=people,dc=test"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seeding the orphan row: %v", err)
	}

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	var after model.UserLDAP
	if err := db.First(&after, "user_id = ?", "vanished").Error; err != nil {
		t.Fatalf("load bookkeeping: %v", err)
	}
	if after.ConsecutiveMisses != 0 {
		t.Errorf("consecutive_misses = %d, want 0 for a row with no user", after.ConsecutiveMisses)
	}
}

// A DN read that fails with a real error (not "no such object") falls back
// to the filter search; when that finds the entry, the pass is a find.
func TestSync_ShouldFallBackToTheFilterWhenTheDNReadErrors(t *testing.T) {
	t.Parallel()

	moved := entry("uid=alice,ou=moved,dc=test", nil)
	dir := &fakeDirectory{entries: []*ldap.Entry{entry("uid=alice,ou=people,dc=test", nil)}}
	cfg := ldapTestConfig(nil)
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	dir.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
		if req.Scope == ldap.ScopeBaseObject {
			return nil, ldap.NewError(ldap.LDAPResultBusy, errors.New("try later"))
		}
		return &ldap.SearchResult{Entries: []*ldap.Entry{moved}}, nil
	}

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	var row model.UserLDAP
	if err := db.First(&row, "user_id = ?", userID).Error; err != nil {
		t.Fatalf("load bookkeeping: %v", err)
	}
	if row.ConsecutiveMisses != 0 {
		t.Errorf("consecutive_misses = %d, want 0: the filter found the entry", row.ConsecutiveMisses)
	}
	if row.DN != moved.DN {
		t.Errorf("dn = %q, want the re-anchored %q", row.DN, moved.DN)
	}
}

// When the DN read errors and the filter fallback errors too, nothing was
// learned about the user: no miss, no disable.
func TestSync_ShouldNotCountAMissWhenTheFallbackSearchFails(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{entry("uid=alice,ou=people,dc=test", nil)}}
	cfg := ldapTestConfig(nil)
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	dir.searchFn = func(req *ldap.SearchRequest) (*ldap.SearchResult, error) {
		return nil, ldap.NewError(ldap.LDAPResultUnavailable, errors.New("shedding load"))
	}

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	var row model.UserLDAP
	if err := db.First(&row, "user_id = ?", userID).Error; err != nil {
		t.Fatalf("load bookkeeping: %v", err)
	}
	if row.ConsecutiveMisses != 0 {
		t.Errorf("consecutive_misses = %d, want 0: a failed search is not evidence", row.ConsecutiveMisses)
	}
}

// disable_after = 0 records misses but never disables; the threshold is
// what arms containment, not the counter.
func TestSync_ShouldNeverDisableWithNoThreshold(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{entry("uid=alice,ou=people,dc=test", nil)}}
	cfg := ldapTestConfig(nil)
	cfg.LDAP.Sync.DisableAfter = 0
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	dir.entries = nil
	dir.searchFn = func(*ldap.SearchRequest) (*ldap.SearchResult, error) {
		return &ldap.SearchResult{}, nil
	}

	for range 5 {
		if err := svc.Sync(context.Background()); err != nil {
			t.Fatalf("Sync() error = %v", err)
		}
	}

	var user model.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.DisabledAt != nil {
		t.Error("a zero disable_after still disabled the user")
	}
	var row model.UserLDAP
	if err := db.First(&row, "user_id = ?", userID).Error; err != nil {
		t.Fatalf("load bookkeeping: %v", err)
	}
	if row.ConsecutiveMisses != 5 {
		t.Errorf("consecutive_misses = %d, want the misses still counted", row.ConsecutiveMisses)
	}
}

// The auto-disable read the user before writing; if an operator disabled
// them in between, the guarded update matches nothing and the operator's
// disable stands unrecorded-over.
func TestAutoDisable_ShouldYieldToAConcurrentDisable(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{}
	svc, db := newLDAPTestService(t, ldapTestConfig(nil), dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")

	// The operator wins the race: the row is disabled after the sync's
	// read (the in-memory user still shows enabled).
	now := time.Now()
	source := model.DisabledSourceSOC
	if err := db.Model(&model.User{}).Where("id = ?", userID).
		Updates(map[string]any{
			"disabled_at":     now,
			"disabled_source": source,
			"disabled_reason": "operator disable",
		}).Error; err != nil {
		t.Fatalf("disable the user: %v", err)
	}

	stale := model.User{ID: userID, Username: "alice"}
	svc.autoDisable(context.Background(), &stale, 3, now)

	var user model.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.DisabledSource == nil || *user.DisabledSource != model.DisabledSourceSOC {
		t.Errorf("disabled_source = %v, want the operator's soc disable intact", user.DisabledSource)
	}
	if user.DisabledReason != "operator disable" {
		t.Errorf("disabled_reason = %q, want the operator's reason intact", user.DisabledReason)
	}
}

// A re-enable whose guarded update matches nothing means an operator took
// over the disable; it must stay.
func TestMaybeReenable_ShouldYieldWhenTheSourceChanged(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{}
	svc, db := newLDAPTestService(t, ldapTestConfig(nil), dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")

	// On disk: an admin disable. In the sync's stale read: an ldap_sync one.
	now := time.Now()
	admin := model.DisabledSourceAdmin
	if err := db.Model(&model.User{}).Where("id = ?", userID).
		Updates(map[string]any{
			"disabled_at":     now,
			"disabled_source": admin,
			"disabled_reason": "admin takeover",
		}).Error; err != nil {
		t.Fatalf("disable the user: %v", err)
	}

	ldapSync := model.DisabledSourceLDAPSync
	stale := model.User{ID: userID, Username: "alice", DisabledAt: &now, DisabledSource: &ldapSync}
	svc.maybeReenable(context.Background(), &stale)

	var user model.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.DisabledAt == nil {
		t.Error("the admin's disable was cleared by a stale sync read")
	}
}

// When the audit row cannot be written, the whole re-enable rolls back: an
// unaudited containment change must not happen.
func TestMaybeReenable_ShouldRollBackWhenTheAuditRowFails(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{}
	cfg := ldapTestConfig(nil)
	svc, db := newLDAPTestService(t, cfg, dir)
	svc.SetAuditor(NewAuditService(cfg, db))
	userID := seedLDAPUser(t, db, "sub-alice", "alice")

	now := time.Now()
	ldapSync := model.DisabledSourceLDAPSync
	if err := db.Model(&model.User{}).Where("id = ?", userID).
		Updates(map[string]any{
			"disabled_at":     now,
			"disabled_source": ldapSync,
			"disabled_reason": "directory entry not found",
		}).Error; err != nil {
		t.Fatalf("disable the user: %v", err)
	}
	if err := db.Migrator().DropTable(&model.AuditEvent{}); err != nil {
		t.Fatalf("dropping the audit table: %v", err)
	}

	stale := model.User{ID: userID, Username: "alice", DisabledAt: &now, DisabledSource: &ldapSync}
	svc.maybeReenable(context.Background(), &stale)

	var user model.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.DisabledAt == nil {
		t.Error("the re-enable committed without its audit row")
	}
}

// The mirror image for containment: an auto-disable that cannot write its
// audit row must not disable anyone.
func TestAutoDisable_ShouldRollBackWhenTheAuditRowFails(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{}
	cfg := ldapTestConfig(nil)
	svc, db := newLDAPTestService(t, cfg, dir)
	svc.SetAuditor(NewAuditService(cfg, db))
	userID := seedLDAPUser(t, db, "sub-alice", "alice")

	if err := db.Migrator().DropTable(&model.AuditEvent{}); err != nil {
		t.Fatalf("dropping the audit table: %v", err)
	}

	svc.autoDisable(context.Background(), &model.User{ID: userID, Username: "alice"}, 3, time.Now())

	var user model.User
	if err := db.First(&user, "id = ?", userID).Error; err != nil {
		t.Fatalf("load user: %v", err)
	}
	if user.DisabledAt != nil {
		t.Error("the auto-disable committed without its audit row")
	}
}
