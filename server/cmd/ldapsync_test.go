package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bep/simplecobra"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/server/webtypes"
	"github.com/mnestor/ssoossh/test/testdb"
)

// syncTestLDAPConfig is the smallest config with the directory on.
const syncTestLDAPConfig = `ssh_key: "test-key-material"
ldap:
  enabled: true
  url: ldaps://ldap.example.com
  base_dn: dc=example,dc=com
  user_filter: (uid={{.Username}})
`

// fakeLDAPPass stands in for the directory service's LDAP pass.
type fakeLDAPPass struct {
	run *model.LDAPSyncRun
	err error
}

// SyncWithOptions returns the canned run, stamped with the options' dry
// run as the real pass stamps it.
func (f *fakeLDAPPass) SyncWithOptions(_ context.Context, opts service.SyncOptions) (*model.LDAPSyncRun, error) {
	if f.run != nil {
		f.run.DryRun = opts.DryRun
	}
	return f.run, f.err
}

// fakeNSSPass stands in for the NSS directory's full sync.
type fakeNSSPass struct {
	result *service.NSSSyncResult
	err    error
	calls  int
}

// FullSync records the call and returns the canned outcome.
func (f *fakeNSSPass) FullSync(context.Context) (*service.NSSSyncResult, error) {
	f.calls++
	return f.result, f.err
}

// fakeSyncer runs the real manual pass over the fakes, auditing to a test
// database, as bootstrap.DirectorySync runs it over the real services.
type fakeSyncer struct {
	ldap   *fakeLDAPPass
	nss    *fakeNSSPass
	db     *gorm.DB
	got    service.ManualSyncRequest
	closed bool
}

// Run runs the manual pass, with the NSS fake only when one was given.
func (f *fakeSyncer) Run(ctx context.Context, req service.ManualSyncRequest) *service.ManualSyncResult {
	f.got = req
	var nss service.NSSDirectorySync
	if f.nss != nil {
		nss = f.nss
	}
	return service.RunManualSync(ctx, f.ldap, nss, service.NewAuditService(&config.Config{}, f.db), req)
}

// Close records the release.
func (f *fakeSyncer) Close(context.Context) error {
	f.closed = true
	return nil
}

// syncCommandResult is one run of `ssoosshd ldap sync` under test.
type syncCommandResult struct {
	stdout string
	err    error
	opened bool
}

// runSyncCommand runs `ssoosshd -c <config> ldap sync <args>` with syncer in
// place of the real wiring.
func runSyncCommand(t *testing.T, configYAML string, syncer directorySyncer, args ...string) syncCommandResult {
	t.Helper()

	path := filepath.Join(t.TempDir(), "ssoosshd.yaml")
	if err := os.WriteFile(path, []byte(configYAML), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	result := syncCommandResult{}
	syncCmd := &ldapSyncCommand{open: func(*config.Config, io.Writer) (directorySyncer, error) {
		result.opened = true
		return syncer, nil
	}}
	root := &RootCommand{commands: []simplecobra.Commander{&ldapCommand{commands: []simplecobra.Commander{syncCmd}}}}
	exec, err := simplecobra.New(root)
	if err != nil {
		t.Fatalf("build the command tree: %v", err)
	}
	var stdout, stderr bytes.Buffer
	root.CobraCommand().SetOut(&stdout)
	root.CobraCommand().SetErr(&stderr)

	_, result.err = exec.Execute(context.Background(), append([]string{"-c", path, "ldap", "sync"}, args...))
	result.stdout = stdout.String()
	return result
}

// completedRun is a pass that finished, with one of everything.
func completedRun() *model.LDAPSyncRun {
	finished := time.Date(2026, 10, 18, 9, 0, 5, 0, time.UTC)
	return &model.LDAPSyncRun{
		ID: "run-1", StartedAt: finished.Add(-5 * time.Second), FinishedAt: &finished,
		Trigger: model.LDAPSyncTriggerManual, Instance: "ssh-1",
		UsersSeen: 5, Found: 3, Missing: 1, Failed: 1, Disabled: 1, Reenabled: 1,
	}
}

// completedNSS is an NSS full sync that finished.
func completedNSS() *service.NSSSyncResult {
	return &service.NSSSyncResult{
		Full: true, StartedAt: time.Date(2026, 10, 18, 9, 0, 5, 0, time.UTC), FinishedAt: time.Date(2026, 10, 18, 9, 0, 9, 0, time.UTC),
		Accounts: service.NSSSyncCounts{Seen: 40, Created: 2, Updated: 3, Missing: 1},
		Groups:   service.NSSSyncCounts{Seen: 8, Updated: 1},
	}
}

func TestLDAPSyncCommand_ShouldRunThePassAndReportIt(t *testing.T) {
	t.Parallel()

	nssConfig := syncTestLDAPConfig + "nss:\n  enabled: true\n"
	tests := []struct {
		name      string
		config    string
		args      []string
		withNSS   bool
		wantNSS   int
		wantLines []string
	}{
		{
			name:    "should apply the pass and then the NSS sync when the pass is live",
			config:  nssConfig,
			withNSS: true,
			wantNSS: 1,
			wantLines: []string{
				"ldap sync      completed (run run-1 on ssh-1)",
				"dry run        no",
				"users          5",
				"disabled       1",
				"re-enabled     1",
				"nss sync       completed",
				"nss accounts   seen=40 created=2 updated=3 skipped=0 refused=0 failed=0 missing=1 tombstoned=0",
				"nss groups     seen=8 created=0 updated=1 skipped=0 refused=0 failed=0 missing=0 tombstoned=0",
			},
		},
		{
			name:    "should skip the NSS sync when the pass is a dry run",
			config:  nssConfig,
			args:    []string{"--dry-run"},
			withNSS: true,
			wantLines: []string{
				"dry run        yes: nothing was changed; disabled and re-enabled are what the pass would have done",
				"nss sync       not run: it has no dry run",
			},
		},
		{
			name:      "should name nss.enabled when NSS is off",
			config:    syncTestLDAPConfig,
			wantLines: []string{"ldap sync      completed (run run-1 on ssh-1)", "nss sync       not run: nss.enabled is false"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			syncer := &fakeSyncer{ldap: &fakeLDAPPass{run: completedRun()}, db: testdb.New(t)}
			if tt.withNSS {
				syncer.nss = &fakeNSSPass{result: completedNSS()}
			}

			got := runSyncCommand(t, tt.config, syncer, tt.args...)

			if got.err != nil {
				t.Fatalf("command error = %v, want none", got.err)
			}
			if syncer.nss != nil && syncer.nss.calls != tt.wantNSS {
				t.Errorf("NSS sync ran %d times, want %d", syncer.nss.calls, tt.wantNSS)
			}
			for _, want := range tt.wantLines {
				if !strings.Contains(got.stdout, want+"\n") {
					t.Errorf("report is missing %q:\n%s", want, got.stdout)
				}
			}
			if !syncer.closed {
				t.Error("the syncer was never closed")
			}
		})
	}
}

func TestLDAPSyncCommand_ShouldFailWhenThePassCannotRun(t *testing.T) {
	t.Parallel()

	refused := errors.New("directory sync could not connect: connection refused")
	tests := []struct {
		name       string
		config     string
		syncer     *fakeSyncer
		wantErr    string
		wantOpened bool
		wantLine   string
	}{
		{
			name:    "should name ldap.enabled when the directory is off",
			config:  "ssh_key: \"test-key-material\"\n",
			syncer:  &fakeSyncer{},
			wantErr: "ldap.enabled is false",
		},
		{
			name:   "should report the reason when the directory is unreachable",
			config: syncTestLDAPConfig,
			syncer: &fakeSyncer{ldap: &fakeLDAPPass{
				run: &model.LDAPSyncRun{ID: "run-2", Instance: "ssh-1", ErrorMessage: refused.Error()},
				err: refused,
			}},
			wantErr:    "the directory sync failed: directory sync could not connect: connection refused",
			wantOpened: true,
			wantLine:   "ldap error     directory sync could not connect: connection refused",
		},
		{
			name:   "should report the reason when the NSS sync fails",
			config: syncTestLDAPConfig + "nss:\n  enabled: true\n",
			syncer: &fakeSyncer{
				ldap: &fakeLDAPPass{run: completedRun()},
				nss:  &fakeNSSPass{result: &service.NSSSyncResult{Full: true}, err: errors.New("nss sync could not connect: refused")},
			},
			wantErr:    "the NSS directory sync failed: nss sync could not connect: refused",
			wantOpened: true,
			wantLine:   "nss error      nss sync could not connect: refused",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.syncer.ldap != nil {
				tt.syncer.db = testdb.New(t)
			}
			got := runSyncCommand(t, tt.config, tt.syncer)

			if got.err == nil || !strings.Contains(got.err.Error(), tt.wantErr) {
				t.Errorf("command error = %v, want one containing %q", got.err, tt.wantErr)
			}
			if got.opened != tt.wantOpened {
				t.Errorf("opened the database = %v, want %v", got.opened, tt.wantOpened)
			}
			if tt.wantLine != "" && !strings.Contains(got.stdout, tt.wantLine+"\n") {
				t.Errorf("report is missing %q:\n%s", tt.wantLine, got.stdout)
			}
		})
	}
}

func TestLDAPSyncCommand_ShouldAuditTheRunAsTheLocalCLI(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	syncer := &fakeSyncer{ldap: &fakeLDAPPass{run: completedRun()}, db: db}
	if got := runSyncCommand(t, syncTestLDAPConfig, syncer); got.err != nil {
		t.Fatalf("command error = %v", got.err)
	}

	var rows []model.AuditEvent
	if err := db.Where("action = ?", string(service.AuditLDAPSyncTriggered)).Find(&rows).Error; err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if len(rows) != 1 || rows[0].ActorUserID != nil {
		t.Fatalf("sync events = %+v, want one with no actor users row", rows)
	}
	var payload struct {
		Actor  service.AuditSubject `json:"actor"`
		Detail map[string]any       `json:"detail"`
	}
	if err := json.Unmarshal([]byte(rows[0].Payload), &payload); err != nil {
		t.Fatalf("decode audit: %v", err)
	}
	if payload.Actor.Subject != service.AuditCLIActor || payload.Detail["via"] != "cli" || payload.Detail["run_id"] != "run-1" {
		t.Errorf("payload = %+v, want the local-cli actor, via cli and the run", payload)
	}
}

func TestLDAPSyncCommand_ShouldPrintTheEndpointsShapeWhenAskedForJSON(t *testing.T) {
	t.Parallel()

	syncer := &fakeSyncer{ldap: &fakeLDAPPass{run: completedRun()}, nss: &fakeNSSPass{result: completedNSS()}, db: testdb.New(t)}
	got := runSyncCommand(t, syncTestLDAPConfig+"nss:\n  enabled: true\n", syncer, "--json")
	if got.err != nil {
		t.Fatalf("command error = %v", got.err)
	}

	var resp webtypes.LDAPSyncRunResponse
	if err := json.Unmarshal([]byte(got.stdout), &resp); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, got.stdout)
	}
	if resp.ID != "run-1" || resp.Disabled != 1 || resp.Trigger != "manual" || resp.NSS == nil || resp.NSS.Accounts.Seen != 40 {
		t.Errorf("response = %+v (nss %+v), want the run and the NSS counts", resp, resp.NSS)
	}
}

func TestLDAPSyncCommand_ShouldFailWhenTheDatabaseCannotBeOpened(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "ssoosshd.yaml")
	if err := os.WriteFile(path, []byte(syncTestLDAPConfig), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	syncCmd := &ldapSyncCommand{open: func(*config.Config, io.Writer) (directorySyncer, error) {
		return nil, errors.New("the database schema is at version 1 and this ssoosshd expects 2")
	}}
	exec, err := simplecobra.New(&RootCommand{commands: []simplecobra.Commander{&ldapCommand{commands: []simplecobra.Commander{syncCmd}}}})
	if err != nil {
		t.Fatalf("build the command tree: %v", err)
	}

	_, err = exec.Execute(context.Background(), []string{"-c", path, "ldap", "sync"})
	if err == nil || !strings.Contains(err.Error(), "schema is at version 1") {
		t.Errorf("command error = %v, want the open failure", err)
	}
}

func TestWriteSyncReport_ShouldSayWhenThePassNeverStarted(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeSyncReport(&out, &service.ManualSyncResult{Err: service.ErrSyncInProgress}, false, false); err != nil {
		t.Fatalf("writeSyncReport() error = %v", err)
	}
	if !strings.HasPrefix(out.String(), "ldap sync      not run\n") || strings.Contains(out.String(), "users") {
		t.Errorf("report = %q, want the pass reported as not run and no counts", out.String())
	}
}

func TestWriteSyncReport_ShouldNameAnUnknownInstance(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := writeSyncReport(&out, &service.ManualSyncResult{Run: &model.LDAPSyncRun{ID: "run-3"}}, false, false); err != nil {
		t.Fatalf("writeSyncReport() error = %v", err)
	}
	if !strings.HasPrefix(out.String(), "ldap sync      completed (run run-3 on an unknown host)\n") {
		t.Errorf("report = %q, want the run on an unknown host", out.String())
	}
}

func TestCLIAuditDetail_ShouldSayItCameFromTheCommandLine(t *testing.T) {
	t.Parallel()

	if got := cliAuditDetail(); got["via"] != "cli" || got["host"] == "" {
		t.Errorf("detail = %v, want via cli and the host", got)
	}
}

func TestLDAPSyncCommand_ShouldRefuseAnArgumentWhenOneIsGiven(t *testing.T) {
	t.Parallel()

	syncer := &fakeSyncer{ldap: &fakeLDAPPass{run: completedRun()}}
	got := runSyncCommand(t, syncTestLDAPConfig, syncer, "dry-run")

	if got.err == nil || got.opened {
		t.Errorf("command error = %v, opened = %v; want the argument refused before anything ran", got.err, got.opened)
	}
}
