package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/test/testdb"
)

// fakeManualLDAP stands in for the directory service's pass, recording the
// options it was given.
type fakeManualLDAP struct {
	run  *model.LDAPSyncRun
	err  error
	got  SyncOptions
	runs int
}

// SyncWithOptions records the call and returns the canned outcome.
func (f *fakeManualLDAP) SyncWithOptions(_ context.Context, opts SyncOptions) (*model.LDAPSyncRun, error) {
	f.got = opts
	f.runs++
	return f.run, f.err
}

// fakeManualNSS stands in for the NSS directory's full sync.
type fakeManualNSS struct {
	result *NSSSyncResult
	err    error
	calls  int
}

// FullSync records the call and returns the canned outcome.
func (f *fakeManualNSS) FullSync(context.Context) (*NSSSyncResult, error) {
	f.calls++
	return f.result, f.err
}

// syncTriggeredEvent is the part of an ldap.sync_triggered payload the
// tests read.
type syncTriggeredEvent struct {
	Actor  *AuditSubject  `json:"actor"`
	System bool           `json:"system"`
	Detail map[string]any `json:"detail"`
}

// syncTriggeredPayloads decodes every ldap.sync_triggered event.
func syncTriggeredPayloads(t *testing.T, db *gorm.DB) []syncTriggeredEvent {
	t.Helper()

	var rows []model.AuditEvent
	if err := db.Where("action = ?", string(AuditLDAPSyncTriggered)).Find(&rows).Error; err != nil {
		t.Fatalf("read audit: %v", err)
	}
	out := make([]syncTriggeredEvent, len(rows))
	for i, row := range rows {
		if err := json.Unmarshal([]byte(row.Payload), &out[i]); err != nil {
			t.Fatalf("decode audit: %v", err)
		}
	}
	return out
}

func TestRunManualSync_ShouldRunTheNSSSyncOnlyWhenItApplies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		dryRun    bool
		nssOn     bool
		wantCalls int
	}{
		{name: "should run the NSS sync after the pass when NSS is on", nssOn: true, wantCalls: 1},
		{name: "should skip the NSS sync when the pass is a dry run", nssOn: true, dryRun: true},
		{name: "should skip the NSS sync when NSS is off"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ldap := &fakeManualLDAP{run: &model.LDAPSyncRun{ID: "run-1", DryRun: tt.dryRun}}
			fake := &fakeManualNSS{result: &NSSSyncResult{Full: true}}
			var nss NSSDirectorySync
			if tt.nssOn {
				nss = fake
			}

			result := RunManualSync(context.Background(), ldap, nss, nil, ManualSyncRequest{DryRun: tt.dryRun})

			if fake.calls != tt.wantCalls || result.NSSRan != (tt.wantCalls == 1) {
				t.Errorf("NSS calls = %d, NSSRan = %v; want %d calls", fake.calls, result.NSSRan, tt.wantCalls)
			}
		})
	}
}

func TestRunManualSync_ShouldPassTheRequestToTheLDAPPass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		req       ManualSyncRequest
		wantActor string
	}{
		{name: "should name the admin as the run's actor when a person ran it", req: ManualSyncRequest{DryRun: true, Actor: &AuditSubject{UserID: "u-alice"}}, wantActor: "u-alice"},
		{name: "should leave the run's actor empty when the command line ran it", req: ManualSyncRequest{DryRun: true, Actor: AuditSubjectForCLI()}},
		{name: "should leave the run's actor empty when there is no actor", req: ManualSyncRequest{DryRun: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ldap := &fakeManualLDAP{run: &model.LDAPSyncRun{ID: "run-1"}}
			RunManualSync(context.Background(), ldap, nil, nil, tt.req)

			want := SyncOptions{Trigger: model.LDAPSyncTriggerManual, DryRun: true, ActorUserID: tt.wantActor}
			if ldap.got != want {
				t.Errorf("options = %+v, want %+v", ldap.got, want)
			}
		})
	}
}

func TestRunManualSync_ShouldStopWithoutRecordingWhenAPassIsRunning(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	ldap := &fakeManualLDAP{err: ErrSyncInProgress}
	nss := &fakeManualNSS{}

	result := RunManualSync(context.Background(), ldap, nss, NewAuditService(ldapTestConfig(nil), db), ManualSyncRequest{})

	if !errors.Is(result.Err, ErrSyncInProgress) || nss.calls != 0 || len(syncTriggeredPayloads(t, db)) != 0 {
		t.Errorf("err = %v, NSS calls = %d, events = %d; want the refusal alone", result.Err, nss.calls, len(syncTriggeredPayloads(t, db)))
	}
}

func TestRunManualSync_ShouldRecordTheCLIActorAndWhatThePassConcluded(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	finished := time.Date(2026, 10, 18, 9, 1, 0, 0, time.UTC)
	ldap := &fakeManualLDAP{run: &model.LDAPSyncRun{ID: "run-1", UsersSeen: 4, Found: 2, Missing: 1, Failed: 1, Disabled: 1}}
	nss := &fakeManualNSS{result: &NSSSyncResult{
		Full: true, StartedAt: finished.Add(-time.Minute), FinishedAt: finished,
		Accounts: NSSSyncCounts{Seen: 3, Created: 2},
	}}

	RunManualSync(context.Background(), ldap, nss, NewAuditService(ldapTestConfig(nil), db), ManualSyncRequest{
		Actor:  AuditSubjectForCLI(),
		Detail: map[string]any{"via": "cli", "os_user": "root", "disabled": "overridden?"},
	})

	events := syncTriggeredPayloads(t, db)
	if len(events) != 1 {
		t.Fatalf("recorded %d sync events, want 1", len(events))
	}
	got := events[0]
	if got.Actor == nil || got.Actor.Subject != AuditCLIActor || got.Actor.Username != AuditCLIActor || got.Actor.UserID != "" || got.System {
		t.Errorf("actor = %+v (system %v), want the local-cli actor with no users row", got.Actor, got.System)
	}
	wantDetail := map[string]any{
		"via": "cli", "os_user": "root", "dry_run": false, "run_id": "run-1",
		"users": 4.0, "found": 2.0, "missing": 1.0, "failed": 1.0, "disabled": 1.0, "reenabled": 0.0,
	}
	for key, want := range wantDetail {
		if got.Detail[key] != want {
			t.Errorf("detail[%q] = %v, want %v", key, got.Detail[key], want)
		}
	}
	nssDetail, ok := got.Detail["nss"].(map[string]any)
	if !ok || nssDetail["full"] != true || nssDetail["finished_at"] != finished.Format(time.RFC3339) {
		t.Errorf("detail[nss] = %v, want the NSS run with its finish time", got.Detail["nss"])
	}
	if accounts, _ := nssDetail["accounts"].(map[string]any); accounts["seen"] != 3.0 || accounts["created"] != 2.0 {
		t.Errorf("detail[nss][accounts] = %v, want seen 3 and created 2", nssDetail["accounts"])
	}
}

func TestRunManualSync_ShouldRecordTheErrorsWhenThePassesFail(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	ldap := &fakeManualLDAP{
		run: &model.LDAPSyncRun{ID: "run-1", ErrorMessage: "directory sync could not connect: refused"},
		err: errors.New("directory sync could not connect: refused"),
	}
	nss := &fakeManualNSS{result: &NSSSyncResult{Full: true}, err: errors.New("nss sync could not connect: refused")}

	result := RunManualSync(context.Background(), ldap, nss, NewAuditService(ldapTestConfig(nil), db), ManualSyncRequest{})

	if result.Run == nil || result.Err == nil || result.NSSErr == nil {
		t.Fatalf("result = %+v, want the run with both errors", result)
	}
	events := syncTriggeredPayloads(t, db)
	if len(events) != 1 {
		t.Fatalf("recorded %d sync events, want 1", len(events))
	}
	nssDetail, _ := events[0].Detail["nss"].(map[string]any)
	if events[0].Detail["error"] != result.Err.Error() || nssDetail["error"] != result.NSSErr.Error() || nssDetail["finished_at"] != nil {
		t.Errorf("detail = %v, want both errors and no NSS finish time", events[0].Detail)
	}
}

// TestRunManualSync_ShouldApplyAndReportADisableWhenThePassIsLive runs the
// real directory pass over a user whose entry has been gone past
// disable_after, live and as a dry run.
func TestRunManualSync_ShouldApplyAndReportADisableWhenThePassIsLive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		dryRun       bool
		wantDisabled bool
	}{
		{name: "should disable the user when the pass is live", wantDisabled: true},
		{name: "should only report the disable when the pass is a dry run", dryRun: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, svc, db, userID := missingEntryFixture(t)
			audit := NewAuditService(svc.config, db)
			svc.SetAuditor(audit)
			if err := svc.Sync(context.Background()); err != nil {
				t.Fatalf("Sync() error = %v", err)
			}
			backdateFirstMissing(t, db, userID, 46*time.Minute)

			result := RunManualSync(context.Background(), svc, nil, audit, ManualSyncRequest{DryRun: tt.dryRun, Actor: AuditSubjectForCLI()})

			if result.Err != nil || result.Run == nil || result.Run.Disabled != 1 {
				t.Fatalf("result = %+v, want a completed pass reporting one disable", result)
			}
			if disabled := loadUser(t, db, userID).DisabledAt != nil; disabled != tt.wantDisabled {
				t.Errorf("user disabled = %v, want %v", disabled, tt.wantDisabled)
			}
			if events := syncTriggeredPayloads(t, db); len(events) != 1 || events[0].Detail["dry_run"] != tt.dryRun {
				t.Errorf("sync events = %+v, want one recording dry_run %v", events, tt.dryRun)
			}
		})
	}
}
