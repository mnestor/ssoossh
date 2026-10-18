package service

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/test/testdb"
)

// nssMailConfig is nssLookupTestConfig with mail on.
func nssMailConfig() *config.Config {
	c := nssLookupTestConfig()
	c.Mail.Enabled = true
	return c
}

// kindsMailed returns the kinds captured for keyID, in order.
func kindsMailed(n *capturingNotifier, keyID string) []notify.Kind {
	var kinds []notify.Kind
	for _, event := range n.captured() {
		if event.NSSKeyID == keyID {
			kinds = append(kinds, event.Kind)
		}
	}
	return kinds
}

// setKey updates one key's columns directly, for the states the API does
// not produce on demand.
func setKey(t *testing.T, db *gorm.DB, id string, updates map[string]any) {
	t.Helper()

	if err := db.Model(&model.NSSKey{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		t.Fatalf("update key: %v", err)
	}
}

func TestNSSKeyMail_ShouldRemindOnTheCadenceWhenAKeyNearsItsExpiry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mail      bool
		lead      time.Duration
		expiresIn time.Duration
		sentAgo   *time.Duration
		revoked   bool
		wantSent  bool
		wantDaily bool
	}{
		{name: "should send a daily reminder when the key is inside its final week", mail: true, lead: 168 * time.Hour, expiresIn: 72 * time.Hour, wantSent: true, wantDaily: true},
		{name: "should send a weekly reminder when the key is inside the lead but not its final week", mail: true, lead: 720 * time.Hour, expiresIn: 400 * time.Hour, wantSent: true},
		{name: "should not repeat a daily reminder inside a day", mail: true, lead: 168 * time.Hour, expiresIn: 72 * time.Hour, sentAgo: durationPtr(23 * time.Hour)},
		{name: "should repeat a daily reminder after a day", mail: true, lead: 168 * time.Hour, expiresIn: 72 * time.Hour, sentAgo: durationPtr(24 * time.Hour), wantSent: true, wantDaily: true},
		{name: "should not repeat a weekly reminder inside a week", mail: true, lead: 720 * time.Hour, expiresIn: 400 * time.Hour, sentAgo: durationPtr(6 * 24 * time.Hour)},
		{name: "should not remind when the key expires beyond the lead", mail: true, lead: 168 * time.Hour, expiresIn: 200 * time.Hour},
		{name: "should not remind when the key has already expired", mail: true, lead: 168 * time.Hour, expiresIn: -time.Hour},
		{name: "should not remind when the key is revoked", mail: true, lead: 168 * time.Hour, expiresIn: 72 * time.Hour, revoked: true},
		{name: "should not remind when mail is off", lead: 168 * time.Hour, expiresIn: 72 * time.Hour},
		{name: "should not remind when the lead is zero", mail: true, lead: 0, expiresIn: 72 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := nssLookupTestConfig()
			c.Mail.Enabled = tt.mail
			c.Mail.ExpiryReminderLead = tt.lead
			db := testdb.New(t)
			seedNSSCallers(t, db)
			keys, notifier := newTestNSSKeys(t, c, db)
			created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
			updates := map[string]any{"expires_at": nssNow.Add(tt.expiresIn)}
			if tt.sentAgo != nil {
				updates["expiry_reminder_sent_at"] = nssNow.Add(-*tt.sentAgo)
			}
			if tt.revoked {
				updates["revoked_at"] = nssNow.Add(-time.Hour)
			}
			setKey(t, db, created.Key.ID, updates)

			if err := keys.SweepExpiryReminders(t.Context()); err != nil {
				t.Fatalf("SweepExpiryReminders: %v", err)
			}
			mailed := kindsMailed(notifier, created.Key.ID)
			if !tt.wantSent {
				if len(mailed) != 0 {
					t.Errorf("mailed %v, want nothing", mailed)
				}
				return
			}
			event := notifier.only(t, notify.KindNSSKeyExpiring)
			payload, ok := event.Payload.(*notify.NSSKeyExpiring)
			if !ok || payload.Daily != tt.wantDaily || payload.KeyID != created.Key.ID || payload.GroupName != "web-admins" {
				t.Errorf("reminder = %+v, want daily %v for the key", payload, tt.wantDaily)
			}

			// A second instance sweeping the same moment sends nothing.
			if err := keys.SweepExpiryReminders(t.Context()); err != nil {
				t.Fatalf("second sweep: %v", err)
			}
			if again := kindsMailed(notifier, created.Key.ID); len(again) != 1 {
				t.Errorf("mailed %v after a second sweep, want the claim to hold", again)
			}
		})
	}
}

// durationPtr returns a pointer to d.
func durationPtr(d time.Duration) *time.Duration { return &d }

func TestNSSKeyMail_ShouldSendTheCleanupDigestWhenHostsHaveNotSwitched(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		quiet       time.Duration
		rotatedAgo  time.Duration
		graceLeft   time.Duration
		sentAgo     *time.Duration
		notSwitched bool
		wantSent    bool
	}{
		{name: "should send the digest when the quiet period is over and a host is not switched", quiet: 168 * time.Hour, rotatedAgo: 8 * 24 * time.Hour, graceLeft: 100 * time.Hour, notSwitched: true, wantSent: true},
		{name: "should stay silent when the quiet period is not over", quiet: 168 * time.Hour, rotatedAgo: 3 * 24 * time.Hour, graceLeft: 100 * time.Hour, notSwitched: true},
		{name: "should stay silent when every host has switched", quiet: 168 * time.Hour, rotatedAgo: 8 * 24 * time.Hour, graceLeft: 100 * time.Hour},
		{name: "should stay silent when the grace has ended", quiet: 168 * time.Hour, rotatedAgo: 8 * 24 * time.Hour, graceLeft: -time.Hour, notSwitched: true},
		{name: "should not repeat the digest inside a day", quiet: 168 * time.Hour, rotatedAgo: 8 * 24 * time.Hour, graceLeft: 100 * time.Hour, sentAgo: durationPtr(12 * time.Hour), notSwitched: true},
		{name: "should repeat the digest after a day", quiet: 168 * time.Hour, rotatedAgo: 8 * 24 * time.Hour, graceLeft: 100 * time.Hour, sentAgo: durationPtr(24 * time.Hour), notSwitched: true, wantSent: true},
		{name: "should stay silent when the quiet period is zero", quiet: 0, rotatedAgo: 8 * 24 * time.Hour, graceLeft: 100 * time.Hour, notSwitched: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := nssMailConfig()
			c.Mail.RotationQuietPeriod = tt.quiet
			db := testdb.New(t)
			seedNSSCallers(t, db)
			keys, notifier := newTestNSSKeys(t, c, db)
			created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
			rotatedAt := nssNow.Add(-tt.rotatedAgo)
			keys.now = func() time.Time { return rotatedAt }
			if _, _, err := keys.Rotate(t.Context(), nssKeyAdmin, created.Key.ID, ""); err != nil {
				t.Fatalf("rotate: %v", err)
			}
			keys.now = nssLookupClock()
			updates := map[string]any{"previous_expires_at": nssNow.Add(tt.graceLeft)}
			if tt.sentAgo != nil {
				updates["rotation_cleanup_sent_at"] = nssNow.Add(-*tt.sentAgo)
			}
			setKey(t, db, created.Key.ID, updates)
			seedNSSUsage(t, db, created.Key.ID, "192.0.2.1", 2, nssNow.Add(-2*time.Hour), nssNow.Add(-time.Hour), 4)
			if tt.notSwitched {
				seedNSSUsage(t, db, created.Key.ID, "192.0.2.2", 1, nssNow.Add(-2*time.Hour), nssNow.Add(-time.Hour), 4)
			}

			if err := keys.SweepRotationCleanup(t.Context()); err != nil {
				t.Fatalf("SweepRotationCleanup: %v", err)
			}
			var digests []capturedNotification
			for _, event := range notifier.captured() {
				if event.Kind == notify.KindNSSKeyRotationCleanup {
					digests = append(digests, event)
				}
			}
			if !tt.wantSent {
				if len(digests) != 0 {
					t.Errorf("sent %d digests, want none", len(digests))
				}
				return
			}
			if len(digests) != 1 {
				t.Fatalf("sent %d digests, want 1", len(digests))
			}
			payload, ok := digests[0].Payload.(*notify.NSSKeyRotationCleanup)
			if !ok || payload.NotSwitched != 1 || payload.Switched != 1 || !reflect.DeepEqual(payload.NotSwitchedAddresses, []string{"192.0.2.2"}) {
				t.Errorf("digest = %+v, want one switched and 192.0.2.2 listed", payload)
			}
			if err := keys.SweepRotationCleanup(t.Context()); err != nil {
				t.Fatalf("second sweep: %v", err)
			}
			count := 0
			for _, event := range notifier.captured() {
				if event.Kind == notify.KindNSSKeyRotationCleanup {
					count++
				}
			}
			if count != 1 {
				t.Errorf("digests after a second sweep = %d, want the claim to hold", count)
			}
		})
	}
}

func TestNSSKeyMail_ShouldSendNoSweepMailWhenMailIsOff(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, notifier := newTestNSSKeys(t, nssLookupTestConfig(), db)
	created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
	setKey(t, db, created.Key.ID, map[string]any{
		"expires_at": nssNow.Add(time.Hour), "rotated_at": nssNow.Add(-30 * 24 * time.Hour),
		"previous_expires_at": nssNow.Add(time.Hour), "previous_secret_hash": "x",
	})
	if err := keys.SweepRotationCleanup(t.Context()); err != nil {
		t.Fatalf("SweepRotationCleanup: %v", err)
	}
	if err := keys.SweepExpiryReminders(t.Context()); err != nil {
		t.Fatalf("SweepExpiryReminders: %v", err)
	}
	if mailed := kindsMailed(notifier, created.Key.ID); len(mailed) != 0 {
		t.Errorf("mailed %v with mail off", mailed)
	}
}

func TestNSSKeyMail_ShouldListAtMostFiftyAddressesWhenTheDigestIsBuilt(t *testing.T) {
	t.Parallel()

	report := &NSSRotationReport{NotSwitched: 53, Switched: 2}
	for i := range 53 {
		report.Rows = append(report.Rows, NSSRotationReportRow{SourceIP: fmt.Sprintf("192.0.2.%d", i), Bucket: NSSRotationNotSwitched})
	}
	report.Rows = append(report.Rows, NSSRotationReportRow{SourceIP: "198.51.100.1", Bucket: NSSRotationSwitched})
	payload := cleanupPayload(&model.NSSKey{ID: "nss_a", Name: "k", CurrentGeneration: 2}, report, "https://ssh.example.org")
	if len(payload.NotSwitchedAddresses) != nssCleanupListLimit || payload.MoreNotSwitched != 3 {
		t.Errorf("listed %d with %d more, want %d and 3", len(payload.NotSwitchedAddresses), payload.MoreNotSwitched, nssCleanupListLimit)
	}
	if payload.NotSwitchedAddresses[0] != "192.0.2.0" {
		t.Errorf("first address = %q, want the report's order kept", payload.NotSwitchedAddresses[0])
	}
}

func TestNSSKeyMail_ShouldMailADeadKeyAttemptOncePerWindowWhenItRepeats(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		window   time.Duration
		gap      time.Duration
		wantSent int
	}{
		{name: "should mail once when the second attempt is inside the window", window: 24 * time.Hour, gap: time.Hour, wantSent: 1},
		{name: "should mail again when the second attempt is past the window", window: 24 * time.Hour, gap: 25 * time.Hour, wantSent: 2},
		{name: "should mail nothing when the window is zero", window: 0, gap: time.Hour, wantSent: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := nssMailConfig()
			c.Mail.ExpiredAttemptWindow = tt.window
			db := testdb.New(t)
			seedNSSCallers(t, db)
			keys, notifier := newTestNSSKeys(t, c, db)
			created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
			key := created.Key

			keys.notifyDeadKeyAttempt(t.Context(), &nssDeadKeyAttempt{key: key, dead: NSSDeadKeyExpired, sourceIP: "192.0.2.1", at: nssNow})
			keys.notifyDeadKeyAttempt(t.Context(), &nssDeadKeyAttempt{key: key, dead: NSSDeadKeyExpired, sourceIP: "192.0.2.1", at: nssNow.Add(tt.gap)})
			if got := len(kindsMailed(notifier, key.ID)); got != tt.wantSent {
				t.Errorf("mailed %d, want %d", got, tt.wantSent)
			}
		})
	}
}

func TestNSSKeyMail_ShouldNameTheAttemptWhenARevokedKeyIsPresented(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, notifier := newTestNSSKeys(t, nssMailConfig(), db)
	created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
	key := created.Key
	key.RevokedAt = nssAt(-time.Hour)

	keys.notifyDeadKeyAttempt(t.Context(), &nssDeadKeyAttempt{key: key, dead: NSSDeadKeyRevoked, sourceIP: "192.0.2.1", at: nssNow})
	event := notifier.only(t, notify.KindNSSKeyRevokedAttempt)
	payload, ok := event.Payload.(*notify.NSSKeyRevokedAttempt)
	if !ok || payload.SourceIP != "192.0.2.1" || !payload.RevokedAt.Equal(*key.RevokedAt) || payload.ServerURL != "https://ssh.example.org" {
		t.Errorf("attempt mail = %+v, want the address, the revocation and the server", payload)
	}
}
