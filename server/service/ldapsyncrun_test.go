package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-ldap/ldap/v3"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// lastRun reads the most recent run row, failing when none was written.
func lastRun(t *testing.T, db *gorm.DB) model.LDAPSyncRun {
	t.Helper()

	var run model.LDAPSyncRun
	if err := db.Order("started_at DESC").First(&run).Error; err != nil {
		t.Fatalf("load the last sync run: %v", err)
	}
	return run
}

// TestSyncWithOptions_ShouldRecordEveryPass is what makes a sync reportable:
// before this, a pass that ran and one that never fired looked identical
// from outside the log.
func TestSyncWithOptions_ShouldRecordEveryPass(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{
		entry("uid=alice,ou=people,dc=test", nil),
	}}
	cfg := ldapTestConfig(nil)
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	run, err := svc.SyncWithOptions(context.Background(), SyncOptions{
		Trigger:     model.LDAPSyncTriggerManual,
		ActorUserID: userID,
	})
	if err != nil {
		t.Fatalf("SyncWithOptions() error = %v", err)
	}

	stored := lastRun(t, db)
	if stored.ID != run.ID {
		t.Errorf("returned run %q, stored %q", run.ID, stored.ID)
	}
	if stored.Trigger != model.LDAPSyncTriggerManual {
		t.Errorf("trigger = %q, want manual", stored.Trigger)
	}
	if stored.ActorUserID == nil || *stored.ActorUserID != userID {
		t.Errorf("actor = %v, want the triggering admin", stored.ActorUserID)
	}
	if stored.FinishedAt == nil {
		t.Error("the run was never closed out")
	}
	if stored.UsersSeen != 1 || stored.Found != 1 {
		t.Errorf("counts: users=%d found=%d, want 1 and 1", stored.UsersSeen, stored.Found)
	}
	if stored.ErrorMessage != "" {
		t.Errorf("error = %q, want empty for a completed pass", stored.ErrorMessage)
	}
	if stored.Instance == "" {
		t.Error("the run did not record which instance ran it")
	}
}

// TestSyncWithOptions_ShouldRecordAPassThatCouldNotConnect keeps an outage
// visible as a pass that ran and failed, rather than as one that never
// fired — which is the state an operator is usually trying to tell apart.
func TestSyncWithOptions_ShouldRecordAPassThatCouldNotConnect(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{entry("uid=alice,ou=people,dc=test", nil)}}
	cfg := ldapTestConfig(nil)
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	dir.dialErr = errors.New("connection refused")
	if _, err := svc.SyncWithOptions(context.Background(), SyncOptions{}); err == nil {
		t.Fatal("SyncWithOptions() returned no error for an unreachable directory")
	}

	stored := lastRun(t, db)
	if stored.ErrorMessage == "" {
		t.Error("a pass that could not connect recorded no error")
	}
	if stored.FinishedAt == nil {
		t.Error("a failed pass was never closed out")
	}
	if stored.Missing != 0 {
		t.Errorf("missing = %d, want 0: an outage is never a miss", stored.Missing)
	}
}

// TestSyncWithOptions_DryRunShouldChangeNothing is the guarantee the button
// makes: a dry run reports what the pass would do and writes nothing but its
// own run row.
func TestSyncWithOptions_DryRunShouldChangeNothing(t *testing.T) {
	t.Parallel()

	_, svc, db, userID := missingEntryFixture(t)
	svc.SetAuditor(NewAuditService(svc.config, db))

	// Open and age the window so a real pass would disable on the spot.
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	backdateFirstMissing(t, db, userID, 46*time.Minute)
	before := loadUserLDAP(t, db, userID)

	run, err := svc.SyncWithOptions(context.Background(), SyncOptions{
		Trigger: model.LDAPSyncTriggerManual,
		DryRun:  true,
	})
	if err != nil {
		t.Fatalf("SyncWithOptions() error = %v", err)
	}

	if run.Disabled != 1 {
		t.Errorf("dry run reported %d disables, want 1", run.Disabled)
	}
	if loadUser(t, db, userID).DisabledAt != nil {
		t.Error("the dry run disabled a user")
	}

	after := loadUserLDAP(t, db, userID)
	if after.ConsecutiveMisses != before.ConsecutiveMisses {
		t.Errorf("the dry run moved consecutive_misses from %d to %d",
			before.ConsecutiveMisses, after.ConsecutiveMisses)
	}
	if after.LastSyncedAt == nil || before.LastSyncedAt == nil || !after.LastSyncedAt.Equal(*before.LastSyncedAt) {
		t.Error("the dry run moved last_synced_at")
	}

	var events int64
	if err := db.Model(&model.AuditEvent{}).Where("target_user_id = ?", userID).Count(&events).Error; err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if events != 0 {
		t.Errorf("the dry run recorded %d containment events, want none", events)
	}
}

// TestSyncWithOptions_DryRunShouldNotRefreshAttributes covers the other half:
// refreshing a found entry is a write too, and a dry run performs none.
func TestSyncWithOptions_DryRunShouldNotRefreshAttributes(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{
		entry("uid=alice,ou=people,dc=test", map[string][]string{"altSecurityIdentities": {"alice.adm"}}),
	}}
	cfg := ldapTestConfig(map[string]config.LDAPField{
		"other_accounts": {Attribute: "altSecurityIdentities"},
	})
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)
	before := loadUserLDAP(t, db, userID)

	// The directory now says something different.
	dir.entries = []*ldap.Entry{
		entry("uid=alice,ou=people,dc=test", map[string][]string{"altSecurityIdentities": {"alice.new"}}),
	}
	if _, err := svc.SyncWithOptions(context.Background(), SyncOptions{DryRun: true}); err != nil {
		t.Fatalf("SyncWithOptions() error = %v", err)
	}

	after := loadUserLDAP(t, db, userID)
	if after.Attributes != before.Attributes {
		t.Errorf("the dry run rewrote the stored attributes: %q -> %q", before.Attributes, after.Attributes)
	}
}

// TestSyncWithOptions_ShouldRunOneAtATime keeps an impatient operator from
// stacking passes over the same users.
func TestSyncWithOptions_ShouldRunOneAtATime(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	entered := make(chan struct{})
	dir := &fakeDirectory{entries: []*ldap.Entry{entry("uid=alice,ou=people,dc=test", nil)}}
	cfg := ldapTestConfig(nil)
	svc, db := newLDAPTestService(t, cfg, dir)
	userID := seedLDAPUser(t, db, "sub-alice", "alice")
	svc.Enrich(context.Background(), &Identity{Subject: "sub-alice", Username: "alice"}, userID)

	var once sync.Once
	dir.searchFn = func(*ldap.SearchRequest) (*ldap.SearchResult, error) {
		once.Do(func() {
			close(entered)
			<-release
		})
		return &ldap.SearchResult{Entries: dir.entries}, nil
	}

	done := make(chan error, 1)
	go func() {
		_, err := svc.SyncWithOptions(context.Background(), SyncOptions{})
		done <- err
	}()

	<-entered
	_, err := svc.SyncWithOptions(context.Background(), SyncOptions{Trigger: model.LDAPSyncTriggerManual})
	if !errors.Is(err, ErrSyncInProgress) {
		t.Errorf("second pass returned %v, want ErrSyncInProgress", err)
	}
	if !svc.SyncRunning() {
		t.Error("SyncRunning() = false while a pass is in progress")
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("first pass returned %v", err)
	}
	if svc.SyncRunning() {
		t.Error("SyncRunning() = true after the pass finished")
	}

	var runs int64
	if err := db.Model(&model.LDAPSyncRun{}).Count(&runs).Error; err != nil {
		t.Fatalf("count runs: %v", err)
	}
	if runs != 1 {
		t.Errorf("recorded %d runs, want 1: the rejected pass never started", runs)
	}
}

// TestLastSyncRun_ShouldAnswerWhetherTheSyncIsRunningAtAll feeds the status
// panel, whose whole job is telling "ran and found nothing" from "never
// fired".
func TestLastSyncRun_ShouldAnswerWhetherTheSyncIsRunningAtAll(t *testing.T) {
	t.Parallel()

	dir := &fakeDirectory{entries: []*ldap.Entry{entry("uid=alice,ou=people,dc=test", nil)}}
	svc, _ := newLDAPTestService(t, ldapTestConfig(nil), dir)

	got, err := svc.LastSyncRun(context.Background())
	if err != nil {
		t.Fatalf("LastSyncRun() error = %v", err)
	}
	if got != nil {
		t.Errorf("LastSyncRun() = %+v before any pass, want nil", got)
	}

	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}

	got, err = svc.LastSyncRun(context.Background())
	if err != nil {
		t.Fatalf("LastSyncRun() error = %v", err)
	}
	if got == nil {
		t.Fatal("LastSyncRun() = nil after a pass ran")
	}
	if got.Trigger != model.LDAPSyncTriggerSchedule {
		t.Errorf("trigger = %q, want schedule for Sync()", got.Trigger)
	}
}

// TestSyncWithOptions_ShouldBeANoOpWithoutADirectory covers the nil receiver
// every caller relies on instead of branching on config.
func TestSyncWithOptions_ShouldBeANoOpWithoutADirectory(t *testing.T) {
	t.Parallel()

	var svc *LDAPService
	run, err := svc.SyncWithOptions(context.Background(), SyncOptions{})
	if err != nil || run != nil {
		t.Errorf("SyncWithOptions() on a disabled service = (%v, %v), want (nil, nil)", run, err)
	}
	if svc.SyncRunning() {
		t.Error("SyncRunning() = true on a disabled service")
	}
}
