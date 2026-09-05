package service

// Test methodology: unit tests over the three enrollment-scoped additions to
// the notification catalogue — the expiry reminder sweep, the expired-code
// attempt report, and the per-enrollment notification address — against
// in-memory SQLite with a capturing notifier in place of a broker.
//
// The properties that matter are the database-held claims. Both new
// enrollment kinds are emitted from paths every instance runs, and the
// delivery queue group deduplicates consumption rather than publication, so
// "publishes at most once per claim window" is the whole correctness
// argument and is what these tests assert.

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// notifyingEnrollmentService builds an EnrollmentService with a capturing
// notifier attached, so the tests below assert on what was published rather
// than on what was rendered.
//
// The two mail durations are passed individually rather than as a
// config.MailConfig: that struct embeds a timberjack logger, and therefore a
// sync.Once, so copying one by value trips govet's copylocks.
func notifyingEnrollmentService(t *testing.T, reminderLead, expiredWindow time.Duration) (*EnrollmentService, *capturingNotifier) {
	t.Helper()

	// A short ClientTimeout on purpose: no signer runs in these tests, so
	// the one case that reaches the signing wait (a live code) spends the
	// derived signing grace before failing, and a realistic timeout would
	// make that test seconds long for no added coverage.
	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service:       config.CertOptionsService{ValidDuration: time.Hour, EnrollmentDuration: 90 * 24 * time.Hour},
		ClientTimeout: time.Second,
	})
	svc.config.Mail.ExpiryReminderLead = reminderLead
	svc.config.Mail.ExpiredAttemptWindow = expiredWindow

	enrollment := newTestEnrollmentService(t, svc)
	notifier := &capturingNotifier{}
	enrollment.SetNotifier(notifier)
	return enrollment, notifier
}

// seedEnrollmentRow inserts one enrollment expiring at expiresAt. Written
// directly rather than through the approval path because these tests are
// about the sweep's selection and claim, and the approval path cannot mint a
// code that is already about to expire.
func seedEnrollmentRow(t *testing.T, db *gorm.DB, account string, expiresAt time.Time) model.Enrollment {
	t.Helper()

	enrollment := model.Enrollment{
		ID:             uuid.NewString(),
		Code:           uuid.NewString(),
		PublicKey:      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyMaterialForTestsOnlyxxxx test",
		OptionSet:      "{}",
		KeyID:          "svc/" + account,
		Principals:     `["` + account + `"]`,
		ServiceAccount: account,
		UserID:         uuid.NewString(),
		CreatedAt:      time.Now().Add(-24 * time.Hour),
		ExpiresAt:      expiresAt,
	}
	if err := db.Create(&enrollment).Error; err != nil {
		t.Fatalf("failed to seed enrollment: %v", err)
	}
	return enrollment
}

// The reminder's entire purpose: an enrollment inside the lead window gets
// the follow-up the "created" message promised.
func TestSweepExpiryReminders_shouldRemindAnEnrollmentInsideTheWindow(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 7*24*time.Hour, 0)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(3*24*time.Hour))

	if err := svc.SweepExpiryReminders(t.Context()); err != nil {
		t.Fatalf("SweepExpiryReminders: %v", err)
	}

	got := notifier.only(t, notify.KindServiceEnrollmentExpiring)
	if got.EnrollmentID != seeded.ID {
		t.Errorf("reminded enrollment %q, want %q", got.EnrollmentID, seeded.ID)
	}
}

// The claim is what makes "one reminder per interval" true across
// instances; a second sweep standing in for a second instance must publish
// nothing.
func TestSweepExpiryReminders_shouldRemindOnlyOncePerInterval(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 7*24*time.Hour, 0)
	seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(3*24*time.Hour))

	for range 2 {
		if err := svc.SweepExpiryReminders(t.Context()); err != nil {
			t.Fatalf("SweepExpiryReminders: %v", err)
		}
	}

	notifier.only(t, notify.KindServiceEnrollmentExpiring)
}

// markReminded stamps the enrollment's last reminder, standing in for an
// earlier sweep having sent one at that time.
func markReminded(t *testing.T, db *gorm.DB, id string, sentAt time.Time) {
	t.Helper()

	if err := db.Model(&model.Enrollment{}).Where("id = ?", id).
		Update("expiry_reminder_sent_at", sentAt).Error; err != nil {
		t.Fatalf("failed to mark the enrollment reminded: %v", err)
	}
}

// The cadence: weekly while the code has more than a week left, daily once
// it does not, each measured from the last reminder sent. The margin on
// each side of a boundary is an hour, comfortably more than a test runs in
// and less than any interval in play.
func TestSweepExpiryReminders_shouldFollowTheWeeklyThenDailyCadence(t *testing.T) {
	t.Parallel()

	const day = 24 * time.Hour
	tests := []struct {
		name      string
		expiresIn time.Duration
		sentAgo   time.Duration // zero means never reminded
		want      bool
		wantDaily bool
	}{
		{name: "should remind weekly-phase enrollment never reminded", expiresIn: 20 * day, want: true, wantDaily: false},
		{name: "should remind weekly-phase enrollment a week after the last", expiresIn: 20 * day, sentAgo: 7*day + time.Hour, want: true, wantDaily: false},
		{name: "should not remind weekly-phase enrollment inside a week of the last", expiresIn: 20 * day, sentAgo: 7*day - time.Hour, want: false},
		{name: "should not remind weekly-phase enrollment a day after the last", expiresIn: 20 * day, sentAgo: day + time.Hour, want: false},
		{name: "should remind daily-phase enrollment never reminded", expiresIn: 3 * day, want: true, wantDaily: true},
		{name: "should remind daily-phase enrollment a day after the last", expiresIn: 3 * day, sentAgo: day + time.Hour, want: true, wantDaily: true},
		{name: "should not remind daily-phase enrollment inside a day of the last", expiresIn: 3 * day, sentAgo: day - time.Hour, want: false},
		{name: "should switch to daily on entering the final week", expiresIn: 7*day - time.Hour, sentAgo: 2 * day, want: true, wantDaily: true},
		{name: "should stay weekly just outside the final week", expiresIn: 7*day + time.Hour, sentAgo: 2 * day, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc, notifier := notifyingEnrollmentService(t, 30*day, 0)
			seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(tt.expiresIn))
			if tt.sentAgo != 0 {
				markReminded(t, svc.db, seeded.ID, time.Now().Add(-tt.sentAgo))
			}

			if err := svc.SweepExpiryReminders(t.Context()); err != nil {
				t.Fatalf("SweepExpiryReminders: %v", err)
			}

			if !tt.want {
				if got := notifier.captured(); len(got) != 0 {
					t.Errorf("published %+v, want nothing", got)
				}
				return
			}
			got := notifier.only(t, notify.KindServiceEnrollmentExpiring)
			payload, ok := got.Payload.(*notify.ServiceEnrollmentExpiring)
			if !ok {
				t.Fatalf("payload is %T, want *notify.ServiceEnrollmentExpiring", got.Payload)
			}
			if payload.Daily != tt.wantDaily {
				t.Errorf("Daily = %t, want %t", payload.Daily, tt.wantDaily)
			}
		})
	}
}

// A reminder sent under the old cadence must not block the next one:
// the stamp moves forward with each send, so a sweep a day later inside
// the final week sends again rather than treating the row as done.
func TestSweepExpiryReminders_shouldAdvanceTheStampOnEachSend(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 7*24*time.Hour, 0)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(3*24*time.Hour))
	markReminded(t, svc.db, seeded.ID, time.Now().Add(-25*time.Hour))

	if err := svc.SweepExpiryReminders(t.Context()); err != nil {
		t.Fatalf("SweepExpiryReminders: %v", err)
	}
	notifier.only(t, notify.KindServiceEnrollmentExpiring)

	var row model.Enrollment
	if err := svc.db.First(&row, "id = ?", seeded.ID).Error; err != nil {
		t.Fatalf("failed to reload the enrollment: %v", err)
	}
	if row.ExpiryReminderSentAt == nil || time.Since(*row.ExpiryReminderSentAt) > time.Minute {
		t.Errorf("expiry_reminder_sent_at = %v, want stamped with the time of this send", row.ExpiryReminderSentAt)
	}
}

// A code expiring in three months is not news yet. Reminding early would
// train the recipient to ignore the message that matters.
func TestSweepExpiryReminders_shouldIgnoreAnEnrollmentBeyondTheWindow(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 7*24*time.Hour, 0)
	seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(60*24*time.Hour))

	if err := svc.SweepExpiryReminders(t.Context()); err != nil {
		t.Fatalf("SweepExpiryReminders: %v", err)
	}

	if got := notifier.captured(); len(got) != 0 {
		t.Errorf("published %+v, want nothing for an enrollment outside the window", got)
	}
}

// "Expires in already elapsed" helps nobody. The aftermath of an expired
// code has its own notification, raised by anything that still presents it.
func TestSweepExpiryReminders_shouldIgnoreAnAlreadyExpiredEnrollment(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 7*24*time.Hour, 0)
	seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(-time.Hour))

	if err := svc.SweepExpiryReminders(t.Context()); err != nil {
		t.Fatalf("SweepExpiryReminders: %v", err)
	}

	if got := notifier.captured(); len(got) != 0 {
		t.Errorf("published %+v, want nothing for an already-expired enrollment", got)
	}
}

// A never-redeemed code is usually a job that was never finished, which is a
// different decision for the reader than one that has been running for
// months. The payload has to carry that distinction.
func TestSweepExpiryReminders_shouldReportWhetherTheCodeWasEverRedeemed(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 7*24*time.Hour, 0)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(3*24*time.Hour))

	redeemedAt := time.Now().Add(-30 * 24 * time.Hour)
	if err := svc.db.Model(&model.Enrollment{}).Where("id = ?", seeded.ID).
		Update("redeemed_at", redeemedAt).Error; err != nil {
		t.Fatalf("failed to stamp the redemption: %v", err)
	}

	if err := svc.SweepExpiryReminders(t.Context()); err != nil {
		t.Fatalf("SweepExpiryReminders: %v", err)
	}

	payload, ok := notifier.only(t, notify.KindServiceEnrollmentExpiring).Payload.(*notify.ServiceEnrollmentExpiring)
	if !ok {
		t.Fatalf("payload is %T, want *notify.ServiceEnrollmentExpiring", notifier.only(t, notify.KindServiceEnrollmentExpiring).Payload)
	}
	if payload.FirstRedeemedAt.IsZero() {
		t.Error("FirstRedeemedAt is zero for a code that has been redeemed")
	}
}

// Zero turns the reminder off. Registration already declines to schedule the
// sweep in that case, so this covers the guard on the value itself — which is
// what protects a deployment that reloads the lead to zero.
func TestSweepExpiryReminders_shouldDoNothingWhenTheLeadIsZero(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 0, 0)
	seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(3*24*time.Hour))

	if err := svc.SweepExpiryReminders(t.Context()); err != nil {
		t.Fatalf("SweepExpiryReminders: %v", err)
	}

	if got := notifier.captured(); len(got) != 0 {
		t.Errorf("published %+v with the reminder disabled, want nothing", got)
	}
}

// Presenting an expired code is either a job now failing on schedule or a
// credential being replayed. Both are worth a message.
func TestRetrieve_shouldNotifyAboutAnExpiredCode(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 0, 24*time.Hour)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(-time.Hour))

	if _, err := svc.Retrieve(t.Context(), seeded.Code, "198.51.100.7"); err == nil {
		t.Fatal("Retrieve accepted an expired code")
	}

	payload, ok := notifier.only(t, notify.KindServiceEnrollmentExpiredAttempt).Payload.(*notify.ServiceEnrollmentExpiredAttempt)
	if !ok {
		t.Fatalf("payload is %T, want *notify.ServiceEnrollmentExpiredAttempt", notifier.captured()[0].Payload)
	}
	if payload.SourceIP != "198.51.100.7" {
		t.Errorf("SourceIP = %q, want the address the attempt came from", payload.SourceIP)
	}
}

// The notification must not change the answer: an expired code and an
// unknown one look identical on the wire, so whoever holds it learns nothing
// from the refusal.
func TestRetrieve_shouldStillAnswerNotFoundForAnExpiredCode(t *testing.T) {
	t.Parallel()

	svc, _ := notifyingEnrollmentService(t, 0, 24*time.Hour)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(-time.Hour))

	_, err := svc.Retrieve(t.Context(), seeded.Code, "198.51.100.7")

	var notFound *errorresponses.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Retrieve error = %v, want a NotFoundError", err)
	}
}

// A broken cron job retries forever. Without the window every retry would be
// a message, which is the fastest way to make the notification worthless.
func TestRetrieve_shouldNotifyOncePerWindowForRepeatedExpiredAttempts(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 0, 24*time.Hour)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(-time.Hour))

	for range 3 {
		if _, err := svc.Retrieve(t.Context(), seeded.Code, "198.51.100.7"); err == nil {
			t.Fatal("Retrieve accepted an expired code")
		}
	}

	notifier.only(t, notify.KindServiceEnrollmentExpiredAttempt)
}

// The window is a rate limit, not a one-shot: a job still failing tomorrow
// is still worth hearing about.
func TestRetrieve_shouldNotifyAgainAfterTheWindowElapses(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 0, time.Hour)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(-48*time.Hour))

	if _, err := svc.Retrieve(t.Context(), seeded.Code, "198.51.100.7"); err == nil {
		t.Fatal("Retrieve accepted an expired code")
	}

	// Backdate the claim rather than sleeping: the window is the only thing
	// under test, and a test that waits an hour is not a test.
	stale := time.Now().Add(-2 * time.Hour)
	if err := svc.db.Model(&model.Enrollment{}).Where("id = ?", seeded.ID).
		Update("last_expired_attempt_notified_at", stale).Error; err != nil {
		t.Fatalf("failed to backdate the claim: %v", err)
	}

	if _, err := svc.Retrieve(t.Context(), seeded.Code, "198.51.100.7"); err == nil {
		t.Fatal("Retrieve accepted an expired code")
	}

	var count int
	for _, event := range notifier.captured() {
		if event.Kind == notify.KindServiceEnrollmentExpiredAttempt {
			count++
		}
	}
	if count != 2 {
		t.Errorf("published %d expired-attempt notifications, want one per window", count)
	}
}

// Zero turns the notification off entirely, which is what an operator whose
// jobs legitimately outlive their codes needs.
func TestRetrieve_shouldNotNotifyWhenTheWindowIsZero(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 0, 0)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(-time.Hour))

	if _, err := svc.Retrieve(t.Context(), seeded.Code, "198.51.100.7"); err == nil {
		t.Fatal("Retrieve accepted an expired code")
	}

	if got := notifier.captured(); len(got) != 0 {
		t.Errorf("published %+v with the window disabled, want nothing", got)
	}
}

// A code that is still live is not an expired attempt, however the
// redemption itself turns out.
func TestRetrieve_shouldNotNotifyAnExpiredAttemptForALiveCode(t *testing.T) {
	t.Parallel()

	svc, notifier := notifyingEnrollmentService(t, 0, 24*time.Hour)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(24*time.Hour))

	// No signer is running, so this fails at signing rather than at the
	// expiry check — which is the point: the expired-attempt notification
	// must not fire for a code that was still redeemable.
	if _, err := svc.Retrieve(t.Context(), seeded.Code, "198.51.100.7"); err == nil {
		t.Fatal("Retrieve returned a certificate with no signer running")
	}

	for _, event := range notifier.captured() {
		if event.Kind == notify.KindServiceEnrollmentExpiredAttempt {
			t.Error("published an expired-attempt notification for a live code")
		}
	}
}

// A holder of the service account may point the enrollment's notifications
// wherever the job's owners actually read.
func TestSetNotificationEmail_shouldStoreAnAddressForAHolder(t *testing.T) {
	t.Parallel()

	svc, _ := notifyingEnrollmentService(t, 0, 0)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(24*time.Hour))
	identity := &Identity{Subject: "sub-holder", Username: "holder", ServiceAccounts: []string{"deploy-bot"}}

	if err := svc.SetNotificationEmail(t.Context(), seeded.ID, identity, "  deploys@example.com  "); err != nil {
		t.Fatalf("SetNotificationEmail: %v", err)
	}

	var stored model.Enrollment
	if err := svc.db.First(&stored, "id = ?", seeded.ID).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.NotificationEmail != "deploys@example.com" {
		t.Errorf("stored %q, want the trimmed address", stored.NotificationEmail)
	}
}

// Clearing restores fan-out, which is the only way back from an address that
// turned out to be wrong.
func TestSetNotificationEmail_shouldClearOnAnEmptyValue(t *testing.T) {
	t.Parallel()

	svc, _ := notifyingEnrollmentService(t, 0, 0)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(24*time.Hour))
	identity := &Identity{Subject: "sub-holder", Username: "holder", ServiceAccounts: []string{"deploy-bot"}}

	if err := svc.SetNotificationEmail(t.Context(), seeded.ID, identity, "deploys@example.com"); err != nil {
		t.Fatalf("SetNotificationEmail: %v", err)
	}
	if err := svc.SetNotificationEmail(t.Context(), seeded.ID, identity, ""); err != nil {
		t.Fatalf("SetNotificationEmail clearing: %v", err)
	}

	var stored model.Enrollment
	if err := svc.db.First(&stored, "id = ?", seeded.ID).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if stored.NotificationEmail != "" {
		t.Errorf("stored %q, want the address cleared", stored.NotificationEmail)
	}
}

// A typo here silently sends every future notification about a credential
// nowhere, so it fails at the point of entry.
func TestSetNotificationEmail_shouldRejectAnInvalidAddress(t *testing.T) {
	t.Parallel()

	svc, _ := notifyingEnrollmentService(t, 0, 0)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(24*time.Hour))
	identity := &Identity{Subject: "sub-holder", Username: "holder", ServiceAccounts: []string{"deploy-bot"}}

	err := svc.SetNotificationEmail(t.Context(), seeded.ID, identity, "not an address")

	var invalid *errorresponses.InvalidRequestError
	if !errors.As(err, &invalid) {
		t.Fatalf("SetNotificationEmail error = %v, want an InvalidRequestError", err)
	}
}

// Ownership is holding the service account and nothing else. Someone who
// does not hold it must not be able to redirect its mail.
func TestSetNotificationEmail_shouldRefuseANonHolder(t *testing.T) {
	t.Parallel()

	svc, _ := notifyingEnrollmentService(t, 0, 0)
	seeded := seedEnrollmentRow(t, svc.db, "deploy-bot", time.Now().Add(24*time.Hour))
	identity := &Identity{Subject: "sub-other", Username: "other", ServiceAccounts: []string{"backup-bot"}}

	err := svc.SetNotificationEmail(t.Context(), seeded.ID, identity, "deploys@example.com")

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("SetNotificationEmail error = %v, want a ForbiddenError", err)
	}
}

func TestSetNotificationEmail_shouldReportAnUnknownEnrollment(t *testing.T) {
	t.Parallel()

	svc, _ := notifyingEnrollmentService(t, 0, 0)
	identity := &Identity{Subject: "sub-holder", Username: "holder", ServiceAccounts: []string{"deploy-bot"}}

	err := svc.SetNotificationEmail(t.Context(), "no-such-enrollment", identity, "deploys@example.com")

	var notFound *errorresponses.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("SetNotificationEmail error = %v, want a NotFoundError", err)
	}
}
