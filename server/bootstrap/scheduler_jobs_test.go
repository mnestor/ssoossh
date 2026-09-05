package bootstrap

// Test methodology: unit tests over the conditional job registrations and
// the notification consumer wiring, on the same minimal *app fixture the
// other bootstrap tests use. What matters is the gating: a disabled
// feature registers nothing (and must not need a scheduler at all), an
// enabled one registers, and a broken mail configuration fails startup
// rather than silently sending nothing.

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/job"
	"github.com/mnestor/ssoossh/server/pubsub"
	"github.com/mnestor/ssoossh/server/service"
)

// newTestScheduler builds a real scheduler that is never started; job
// registration is all these tests exercise.
func newTestScheduler(t *testing.T) *job.Scheduler {
	t.Helper()

	s, err := job.NewScheduler()
	if err != nil {
		t.Fatalf("job.NewScheduler: %v", err)
	}
	return s
}

// setEnabledMail fills c.Mail in place with the smallest configuration
// initNotifications accepts: a plaintext local relay and the built-in
// templates. In place because MailConfig embeds a sync.Once via its
// logging block and must not be copied.
func setEnabledMail(c *config.Config) {
	c.Mail.Enabled = true
	c.Mail.From = "ssoossh@example.com"
	c.Mail.SMTP = config.SMTPConfig{
		Host:    "localhost",
		Port:    25,
		TLS:     config.MailTLSOff,
		Auth:    config.MailAuthNone,
		Timeout: time.Second,
	}
}

// newPubSubApp is an *app with a live in-process broker, for the paths
// that register consumers.
func newPubSubApp(t *testing.T, c *config.Config) *app {
	t.Helper()

	ps, err := pubsub.New(&config.PubSubConfig{}, slog.Default())
	if err != nil {
		t.Fatalf("failed to build pub/sub: %v", err)
	}
	t.Cleanup(func() { _ = ps.Close(t.Context()) })
	return &app{config: c, pubSub: ps, svc: &services{}}
}

func TestInitNotifications_ShouldDoNothingWhenMailIsDisabled(t *testing.T) {
	t.Parallel()

	// No pubSub and no db: the disabled branch must return before needing
	// either.
	a := &app{config: &config.Config{}, svc: &services{}}
	if err := a.initNotifications(); err != nil {
		t.Errorf("initNotifications() with mail disabled = %v, want nil", err)
	}
}

func TestInitNotifications_ShouldRegisterTheDeliveryConsumer(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	setEnabledMail(c)
	a := newPubSubApp(t, c)

	if err := a.initNotifications(); err != nil {
		t.Errorf("initNotifications() = %v, want the consumer registered", err)
	}
}

// A template dir with a file matching no notification is an operator edit
// that would otherwise appear to do nothing; startup must refuse it.
func TestInitNotifications_ShouldFailOnAnUnmatchedTemplateOverride(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bogus.subject.tmpl"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("writing the stray template: %v", err)
	}

	c := &config.Config{}
	setEnabledMail(c)
	c.Mail.TemplateDir = dir
	a := newPubSubApp(t, c)

	err := a.initNotifications()
	if err == nil {
		t.Fatal("initNotifications() accepted a template dir with an unmatched file")
	}
	if !strings.Contains(err.Error(), "bogus.subject.tmpl") {
		t.Errorf("initNotifications() error = %q, want the stray file named", err)
	}
}

// An unusable relay configuration must fail startup, not surface later as
// mail that silently stops arriving.
func TestInitNotifications_ShouldFailOnAnUnusableRelay(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	setEnabledMail(c)
	c.Mail.SMTP.TLS = config.MailTLSRequired
	c.Mail.SMTP.CAFile = filepath.Join(t.TempDir(), "absent.pem")
	a := newPubSubApp(t, c)

	if err := a.initNotifications(); err == nil {
		t.Error("initNotifications() accepted a relay with an unreadable CA file")
	}
}

func TestRegisterAuditSweepJob_ShouldNotRegisterWhenBothBoundsAreOff(t *testing.T) {
	t.Parallel()

	// No scheduler on the fixture: the early return must not need one.
	a := &app{config: &config.Config{}}
	if err := a.registerAuditSweepJob(t.Context()); err != nil {
		t.Errorf("registerAuditSweepJob() with pruning disabled = %v, want nil", err)
	}
}

func TestRegisterAuditSweepJob_ShouldRegisterWithTheConfiguredInterval(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.Audit.Retention = 24 * time.Hour
	c.Audit.SweepInterval = 30 * time.Minute
	a := &app{config: c, scheduler: newTestScheduler(t)}

	if err := a.registerAuditSweepJob(t.Context()); err != nil {
		t.Errorf("registerAuditSweepJob() = %v, want the sweep registered", err)
	}
}

// A zero interval falls back to hourly rather than failing or sweeping
// never; only the bounds decide whether the job exists.
func TestRegisterAuditSweepJob_ShouldDefaultTheInterval(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.Audit.MaxRows = 1000
	a := &app{config: c, scheduler: newTestScheduler(t)}

	if err := a.registerAuditSweepJob(t.Context()); err != nil {
		t.Errorf("registerAuditSweepJob() = %v, want the sweep registered hourly", err)
	}
}

// newTestEnrollmentServiceForScheduler builds the smallest enrollment
// service the sweep registration needs. Registration only takes the method
// value, so nothing here is ever called.
func newTestEnrollmentServiceForScheduler(t *testing.T, c *config.Config) *service.EnrollmentService {
	t.Helper()

	svc, err := service.NewEnrollmentService(c, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewEnrollmentService: %v", err)
	}
	return svc
}

// With no relay there is nowhere for a reminder to go, so the sweep would be
// a recurring query producing events nothing consumes.
func TestRegisterExpiryReminderJob_ShouldNotRegisterWhenMailIsDisabled(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.Mail.ExpiryReminderLead = 7 * 24 * time.Hour
	// No scheduler on the fixture: the early return must not need one.
	a := &app{config: c}

	if err := a.registerExpiryReminderJob(t.Context()); err != nil {
		t.Errorf("registerExpiryReminderJob() with mail disabled = %v, want nil", err)
	}
}

func TestRegisterExpiryReminderJob_ShouldNotRegisterWithAZeroLead(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.Mail.Enabled = true
	a := &app{config: c}

	if err := a.registerExpiryReminderJob(t.Context()); err != nil {
		t.Errorf("registerExpiryReminderJob() with a zero lead = %v, want nil", err)
	}
}

func TestRegisterExpiryReminderJob_ShouldRegisterTheSweep(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.Mail.Enabled = true
	c.Mail.ExpiryReminderLead = 7 * 24 * time.Hour
	a := &app{config: c, scheduler: newTestScheduler(t), svc: &services{
		enrollment: newTestEnrollmentServiceForScheduler(t, c),
	}}

	if err := a.registerExpiryReminderJob(t.Context()); err != nil {
		t.Errorf("registerExpiryReminderJob() = %v, want the sweep registered", err)
	}
}

// The sweep paces itself off the shortest cadence, so a long lead still
// sweeps often enough to keep the final week's reminders daily, and a lab
// lead of an hour is floored rather than swept every couple of minutes.
func TestExpiryReminderInterval_ShouldFollowTheShortestCadence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lead time.Duration
		want time.Duration
	}{
		{name: "should sweep hourly for the default week", lead: 7 * 24 * time.Hour, want: time.Hour},
		{name: "should still sweep hourly for a month", lead: 30 * 24 * time.Hour, want: time.Hour},
		{name: "should sweep at a fraction of a lead shorter than a day", lead: 12 * time.Hour, want: 30 * time.Minute},
		{name: "should floor a very short lead", lead: time.Hour, want: minExpiryReminderInterval},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := expiryReminderInterval(tt.lead); got != tt.want {
				t.Errorf("expiryReminderInterval(%v) = %v, want %v", tt.lead, got, tt.want)
			}
		})
	}
}

// A very short lead would otherwise produce a sweep every couple of minutes,
// which is a query per few minutes forever to catch a deadline measured in
// hours. gocron also rejects intervals at the bottom of that range.
func TestRegisterExpiryReminderJob_ShouldFloorTheIntervalForAShortLead(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.Mail.Enabled = true
	c.Mail.ExpiryReminderLead = time.Hour
	a := &app{config: c, scheduler: newTestScheduler(t), svc: &services{
		enrollment: newTestEnrollmentServiceForScheduler(t, c),
	}}

	if err := a.registerExpiryReminderJob(t.Context()); err != nil {
		t.Errorf("registerExpiryReminderJob() with a one-hour lead = %v, want the floored interval registered", err)
	}
}

func TestRegisterLDAPSyncJob_ShouldNotRegisterWithoutADirectory(t *testing.T) {
	t.Parallel()

	a := &app{config: &config.Config{}, svc: &services{}}
	if err := a.registerLDAPSyncJob(t.Context()); err != nil {
		t.Errorf("registerLDAPSyncJob() with ldap disabled = %v, want nil", err)
	}
}

// newTestLDAPService builds the smallest enabled LDAP service; registration
// never dials, so no directory is needed.
func newTestLDAPService(t *testing.T, c *config.Config) *service.LDAPService {
	t.Helper()

	c.LDAP = config.LDAPConfig{
		Enabled:    true,
		URL:        "ldaps://directory.test",
		BaseDN:     "ou=people,dc=test",
		UserFilter: "(uid={{.Username}})",
		Sync:       config.LDAPSync{Interval: time.Minute, DisableAfter: 3},
	}
	svc, err := service.NewLDAPService(c, nil)
	if err != nil {
		t.Fatalf("NewLDAPService: %v", err)
	}
	return svc
}

func TestRegisterLDAPSyncJob_ShouldNotRegisterWithAZeroInterval(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	svc := newTestLDAPService(t, c)
	c.LDAP.Sync.Interval = 0

	// No scheduler: the interval gate must return before using it.
	a := &app{config: c, svc: &services{ldap: svc}}
	if err := a.registerLDAPSyncJob(t.Context()); err != nil {
		t.Errorf("registerLDAPSyncJob() with a zero interval = %v, want nil", err)
	}
}

func TestRegisterLDAPSyncJob_ShouldRegisterTheSync(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	svc := newTestLDAPService(t, c)
	a := &app{config: c, svc: &services{ldap: svc}, scheduler: newTestScheduler(t)}

	if err := a.registerLDAPSyncJob(t.Context()); err != nil {
		t.Errorf("registerLDAPSyncJob() = %v, want the sync registered", err)
	}
}
