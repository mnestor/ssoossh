package service

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
)

// syncLog is a goroutine-safe buffer for the sync's log output.
type syncLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// captureSyncLog points the service's logger at a buffer, at debug level so
// nothing the sync says is filtered out before the test reads it.
func captureSyncLog(svc *LDAPService) *syncLog {
	out := &syncLog{}
	svc.log = slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return out
}

// recordLogin writes an auth.login event through the real recorder, so the
// payload has exactly the encoding a production login produces.
func recordLogin(t *testing.T, db *gorm.DB, userID string, groups []string, at time.Time) {
	t.Helper()
	recordAudit(t, db, AuditAuthLogin, userID, groups, at)
}

// recordAudit writes one event naming userID as actor and target.
func recordAudit(t *testing.T, db *gorm.DB, action AuditAction, userID string, groups []string, at time.Time) {
	t.Helper()

	subject := &AuditSubject{UserID: userID, Subject: "sub-" + userID, Username: userID, Groups: groups}
	NewAuditService(&config.Config{}, db).Record(context.Background(), AuditEvent{
		Action:     action,
		Actor:      subject,
		Target:     subject,
		OccurredAt: at,
	})
}

// disabledAdminFixture is a user whose directory entry has been gone long
// enough that the next live pass disables them, with mail on and every
// notification captured.
type disabledAdminFixture struct {
	svc      *LDAPService
	db       *gorm.DB
	cfg      *config.Config
	userID   string
	notifier *capturingNotifier
	log      *syncLog
}

func newDisabledAdminFixture(t *testing.T) *disabledAdminFixture {
	t.Helper()

	_, svc, db, userID := missingEntryFixture(t)
	svc.SetAuditor(NewAuditService(svc.config, db))
	svc.config.Mail.Enabled = true
	notifier := &capturingNotifier{}
	svc.SetNotifier(notifier)
	if err := db.Model(&model.User{}).Where("id = ?", userID).Update("email", "alice@example.com").Error; err != nil {
		t.Fatalf("set the user's email: %v", err)
	}

	// Open the window and age it past disable_after, so the pass under test
	// is the one that disables.
	if err := svc.Sync(context.Background()); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	backdateFirstMissing(t, db, userID, 46*time.Minute)

	return &disabledAdminFixture{
		svc: svc, db: db, cfg: svc.config, userID: userID,
		notifier: notifier, log: captureSyncLog(svc),
	}
}

// adminEmails returns the captured admin auto-disable notifications.
func (f *disabledAdminFixture) adminEmails() []capturedNotification {
	var out []capturedNotification
	for _, n := range f.notifier.captured() {
		if n.Kind == notify.KindAdminAutoDisabled {
			out = append(out, n)
		}
	}
	return out
}

// The decision table from issue #128: who the sync emails when it disables
// them, and who it does not.
func TestSyncWithOptions_shouldEmailOnlyTheAdminsItDisables(t *testing.T) {
	t.Parallel()

	day := 24 * time.Hour
	adminSource, socSource := model.DisabledSourceAdmin, model.DisabledSourceSOC

	tests := []struct {
		name string
		// logins are written oldest first: groups and how long ago.
		logins []loginAt
		// adminGroup replaces the configured admin group before the pass.
		adminGroup string
		// disabledBy pre-disables the user by hand before the pass.
		disabledBy   *model.DisabledSource
		mailDisabled bool
		dryRun       bool

		wantDisabledBySync bool
		wantEmails         int
		wantLog            string
	}{
		{
			name:               "should disable and email once when the latest login inside 60 days carried the admin group",
			logins:             logins(login([]string{"ssh-admins"}, day)),
			wantDisabledBySync: true,
			wantEmails:         1,
		},
		{
			name:               "should disable without emailing when the user is not an admin",
			logins:             logins(login([]string{"developers"}, day)),
			wantDisabledBySync: true,
			wantEmails:         0,
		},
		{
			name:               "should disable without emailing when the admin login is older than 60 days",
			logins:             logins(login([]string{"ssh-admins"}, 61*day)),
			wantDisabledBySync: true,
			wantEmails:         0,
		},
		{
			name:               "should disable without emailing when the user has never logged in",
			wantDisabledBySync: true,
			wantEmails:         0,
		},
		{
			name:               "should decide on the latest login when an earlier one carried the admin group",
			logins:             logins(login([]string{"ssh-admins"}, 10*day), login([]string{"developers"}, day)),
			wantDisabledBySync: true,
			wantEmails:         0,
		},
		{
			name:               "should email when the latest login carried the admin group after an earlier one did not",
			logins:             logins(login([]string{"developers"}, 10*day), login([]string{"ssh-admins"}, day)),
			wantDisabledBySync: true,
			wantEmails:         1,
		},
		{
			name:               "should not email when the login's group no longer maps to admin in the current config",
			logins:             logins(login([]string{"ssh-admins"}, day)),
			adminGroup:         "platform-admins",
			wantDisabledBySync: true,
			wantEmails:         0,
		},
		{
			name:               "should email when the current config maps the login's group to admin",
			logins:             logins(login([]string{"platform-admins"}, day)),
			adminGroup:         "platform-admins",
			wantDisabledBySync: true,
			wantEmails:         1,
		},
		{
			name:               "should not email an admin an admin disabled by hand",
			logins:             logins(login([]string{"ssh-admins"}, day)),
			disabledBy:         &adminSource,
			wantDisabledBySync: false,
			wantEmails:         0,
		},
		{
			name:               "should not email an admin a SOC user disabled by hand",
			logins:             logins(login([]string{"ssh-admins"}, day)),
			disabledBy:         &socSource,
			wantDisabledBySync: false,
			wantEmails:         0,
		},
		{
			name:               "should disable, send nothing and log it when mail is disabled",
			logins:             logins(login([]string{"ssh-admins"}, day)),
			mailDisabled:       true,
			wantDisabledBySync: true,
			wantEmails:         0,
			wantLog:            "mail is disabled (mail.enabled is false)",
		},
		{
			name:               "should report would_email=admin and change nothing on a dry run",
			logins:             logins(login([]string{"ssh-admins"}, day)),
			dryRun:             true,
			wantDisabledBySync: false,
			wantEmails:         0,
			wantLog:            "would_email=admin",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newDisabledAdminFixture(t)
			now := time.Now()
			for _, l := range tc.logins {
				recordLogin(t, f.db, f.userID, l.groups, now.Add(-l.ago))
			}
			if tc.adminGroup != "" {
				f.cfg.Admin.RequireGroup = tc.adminGroup
			}
			f.cfg.Mail.Enabled = !tc.mailDisabled
			if tc.disabledBy != nil {
				operatorID := seedLDAPUser(t, f.db, "sub-operator", "operator")
				if err := f.db.Model(&model.User{}).Where("id = ?", f.userID).Updates(map[string]any{
					"disabled_at":         now,
					"disabled_by_user_id": operatorID,
					"disabled_source":     *tc.disabledBy,
					"disabled_reason":     "contained by hand",
				}).Error; err != nil {
					t.Fatalf("disable the user by hand: %v", err)
				}
			}

			if _, err := f.svc.SyncWithOptions(context.Background(), SyncOptions{DryRun: tc.dryRun}); err != nil {
				t.Fatalf("SyncWithOptions() error = %v", err)
			}

			user := loadUser(t, f.db, f.userID)
			bySync := user.DisabledAt != nil && user.DisabledSource != nil && *user.DisabledSource == model.DisabledSourceLDAPSync
			if bySync != tc.wantDisabledBySync {
				t.Errorf("disabled by the sync = %v, want %v", bySync, tc.wantDisabledBySync)
			}
			if got := len(f.adminEmails()); got != tc.wantEmails {
				t.Errorf("sent %d admin auto-disable emails, want %d", got, tc.wantEmails)
			}
			if tc.wantLog != "" && !strings.Contains(f.log.String(), tc.wantLog) {
				t.Errorf("log does not say %q:\n%s", tc.wantLog, f.log.String())
			}
		})
	}
}

// loginAt is one row of a case's login history: the groups the login
// carried and how long ago it happened.
type loginAt struct {
	groups []string
	ago    time.Duration
}

// login builds one login row.
func login(groups []string, ago time.Duration) loginAt {
	return loginAt{groups: groups, ago: ago}
}

// logins collects login rows, oldest first.
func logins(rows ...loginAt) []loginAt {
	return rows
}

// The email has to reach the disabled admin by users.id and say what the
// message promises: whose account, how long the entry was gone, where to
// look, and whether the sync will undo it.
func TestSyncWithOptions_shouldAddressTheAdminEmailToTheDisabledAdmin(t *testing.T) {
	t.Parallel()

	f := newDisabledAdminFixture(t)
	recordLogin(t, f.db, f.userID, []string{"ssh-admins"}, time.Now().Add(-time.Hour))
	f.cfg.Branding.SupportEmail = "it-help@example.com"

	if _, err := f.svc.SyncWithOptions(context.Background(), SyncOptions{}); err != nil {
		t.Fatalf("SyncWithOptions() error = %v", err)
	}

	emails := f.adminEmails()
	if len(emails) != 1 {
		t.Fatalf("sent %d admin auto-disable emails, want 1", len(emails))
	}
	payload, ok := emails[0].Payload.(*notify.AdminAutoDisabled)
	if !ok {
		t.Fatalf("payload is %T, want *notify.AdminAutoDisabled", emails[0].Payload)
	}

	row := loadUserLDAP(t, f.db, f.userID)
	tests := []struct {
		name string
		got  any
		want any
	}{
		{name: "should address the disabled user", got: emails[0].UserID, want: f.userID},
		{name: "should name the account", got: payload.Username, want: "alice"},
		{name: "should carry the configured threshold", got: payload.DisableAfter, want: f.cfg.LDAP.Sync.DisableAfter},
		{name: "should carry the entry's last known DN", got: payload.DirectoryDN, want: row.DN},
		{name: "should carry the searched base", got: payload.BaseDN, want: f.cfg.LDAP.BaseDN},
		{name: "should say whether the sync clears it", got: payload.Reenable, want: f.cfg.LDAP.Sync.Reenable},
		{name: "should carry the support contact", got: payload.SupportEmail, want: "it-help@example.com"},
		{name: "should report the entry missing for longer than the threshold", got: payload.MissingFor >= f.cfg.LDAP.Sync.DisableAfter, want: true},
		{name: "should date the disable", got: payload.DisabledAt.IsZero(), want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

// An admin with no stored address cannot be emailed; that is logged, and
// the disable stands.
func TestSyncWithOptions_shouldLogAnAdminWithNoAddressInsteadOfEmailing(t *testing.T) {
	t.Parallel()

	f := newDisabledAdminFixture(t)
	recordLogin(t, f.db, f.userID, []string{"ssh-admins"}, time.Now().Add(-time.Hour))
	if err := f.db.Model(&model.User{}).Where("id = ?", f.userID).Update("email", "").Error; err != nil {
		t.Fatalf("clear the email: %v", err)
	}

	if _, err := f.svc.SyncWithOptions(context.Background(), SyncOptions{}); err != nil {
		t.Fatalf("SyncWithOptions() error = %v", err)
	}

	if got := len(f.adminEmails()); got != 0 {
		t.Errorf("sent %d emails to an admin with no address, want 0", got)
	}
	if !strings.Contains(f.log.String(), "has no email address") {
		t.Errorf("log does not explain the missing email:\n%s", f.log.String())
	}
}

// The effective look-back is shorter than 60 days whenever the audit log
// cannot reach that far, and the operator is told so.
func TestSyncWithOptions_shouldLogAnAdminWindowShorterThan60Days(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		retention time.Duration
		// oldest is how old the oldest audit row is.
		oldest time.Duration
		// noLogin leaves the audit log empty.
		noLogin bool
		wantLog string
	}{
		{
			name:      "should log the retention when audit.retention is below 60 days",
			retention: 30 * 24 * time.Hour,
			oldest:    40 * 24 * time.Hour,
			wantLog:   "limited_by=audit.retention",
		},
		{
			name:      "should log the log's reach when its oldest row is younger than 60 days",
			retention: 0,
			oldest:    10 * 24 * time.Hour,
			wantLog:   "audit.max_rows",
		},
		{
			name:    "should log an empty window when the audit log holds nothing",
			noLogin: true,
			wantLog: "the audit log is empty",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newDisabledAdminFixture(t)
			f.cfg.Audit.Retention = tc.retention
			if err := f.db.Where("1 = 1").Delete(&model.AuditEvent{}).Error; err != nil {
				t.Fatalf("clear the audit log: %v", err)
			}
			if tc.noLogin {
				// The disable's own audit row would otherwise be the log's
				// one entry.
				f.svc.SetAuditor(nil)
			} else {
				recordLogin(t, f.db, f.userID, []string{"ssh-admins"}, time.Now().Add(-tc.oldest))
			}

			if _, err := f.svc.SyncWithOptions(context.Background(), SyncOptions{}); err != nil {
				t.Fatalf("SyncWithOptions() error = %v", err)
			}

			out := f.log.String()
			if !strings.Contains(out, "effective_window=") || !strings.Contains(out, tc.wantLog) {
				t.Errorf("log does not report the shorter window (%q):\n%s", tc.wantLog, out)
			}
		})
	}
}

// A full 60-day log is the normal case, and says nothing about the window.
func TestSyncWithOptions_shouldNotLogTheAdminWindowWhenTheLogReaches60Days(t *testing.T) {
	t.Parallel()

	f := newDisabledAdminFixture(t)
	f.cfg.Audit.Retention = 90 * 24 * time.Hour
	recordAudit(t, f.db, AuditCertRequested, "someone-else", nil, time.Now().Add(-70*24*time.Hour))
	recordLogin(t, f.db, f.userID, []string{"ssh-admins"}, time.Now().Add(-time.Hour))

	if _, err := f.svc.SyncWithOptions(context.Background(), SyncOptions{}); err != nil {
		t.Fatalf("SyncWithOptions() error = %v", err)
	}

	if strings.Contains(f.log.String(), "effective_window=") {
		t.Errorf("logged a shorter window for a log that reaches 60 days:\n%s", f.log.String())
	}
}

// A pass that disables several admins reads the logins once, not once per
// admin, and emails each of them.
func TestSyncWithOptions_shouldEmailEveryAdminItDisablesInOnePass(t *testing.T) {
	t.Parallel()

	f := newDisabledAdminFixture(t)
	bobID := seedLDAPUser(t, f.db, "sub-bob", "bob")
	if err := f.db.Create(&model.UserLDAP{UserID: bobID, DN: "uid=bob,ou=people,dc=test"}).Error; err != nil {
		t.Fatalf("seed bob's directory row: %v", err)
	}
	if err := f.db.Model(&model.User{}).Where("id = ?", bobID).Update("email", "bob@example.com").Error; err != nil {
		t.Fatalf("set bob's email: %v", err)
	}
	backdateFirstMissing(t, f.db, bobID, 46*time.Minute)
	recordLogin(t, f.db, f.userID, []string{"ssh-admins"}, time.Now().Add(-time.Hour))
	recordLogin(t, f.db, bobID, []string{"ssh-admins", "developers"}, time.Now().Add(-2*time.Hour))

	if _, err := f.svc.SyncWithOptions(context.Background(), SyncOptions{}); err != nil {
		t.Fatalf("SyncWithOptions() error = %v", err)
	}

	var got []string
	for _, n := range f.adminEmails() {
		got = append(got, n.UserID)
	}
	slices.Sort(got)
	want := []string{bobID, f.userID}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("emailed %v, want %v", got, want)
	}
}

// A pass that cannot read the logins still disables, emails nobody it
// cannot justify, and says why.
func TestSyncWithOptions_shouldDisableWithoutEmailingWhenTheLoginsCannotBeRead(t *testing.T) {
	t.Parallel()

	f := newDisabledAdminFixture(t)
	recordLogin(t, f.db, f.userID, []string{"ssh-admins"}, time.Now().Add(-time.Hour))
	// Without the auditor the disable writes no audit row, so dropping the
	// table breaks only the login read under test.
	f.svc.SetAuditor(nil)
	if err := f.db.Migrator().DropTable(&model.AuditEvent{}); err != nil {
		t.Fatalf("drop audit_events: %v", err)
	}

	if _, err := f.svc.SyncWithOptions(context.Background(), SyncOptions{}); err != nil {
		t.Fatalf("SyncWithOptions() error = %v", err)
	}

	if loadUser(t, f.db, f.userID).DisabledAt == nil {
		t.Error("the user was not disabled")
	}
	if got := len(f.adminEmails()); got != 0 {
		t.Errorf("sent %d emails without being able to read the logins, want 0", got)
	}
	if !strings.Contains(f.log.String(), "could not read recent logins") {
		t.Errorf("log does not explain the unread logins:\n%s", f.log.String())
	}
}

// SetNotifier on a disabled directory is a no-op rather than a panic, the
// same contract SetAuditor has.
func TestSetNotifier_shouldTolerateADisabledDirectory(t *testing.T) {
	t.Parallel()

	var svc *LDAPService
	svc.SetNotifier(&capturingNotifier{})
}
