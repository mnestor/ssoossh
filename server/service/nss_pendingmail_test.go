package service

// Test methodology: the pending id change mail runs end to end against a
// real database (testdb, so make test-postgres runs it on Postgres too), the
// fake directory, the real NotificationService and the real delivery
// handler, delivered inline into a recordingSender. That way the recipient
// query, the preference gate at delivery, the templates and the claims are
// all the shipped code, and a test reads what each admin's inbox got.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/mail"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
)

// nssMailHarness is an NSS harness with mail on, the notification service
// attached, and the sender every message lands in.
type nssMailHarness struct {
	*nssHarness
	notifications *NotificationService
	sender        *recordingSender
}

// nssIDMailConfig is nssTestConfig with mail on and a public URL.
func nssIDMailConfig() *config.Config {
	cfg := nssTestConfig()
	cfg.Mail.Enabled = true
	cfg.HTTP.PublicURL = "https://ssoossh.example.com"
	return cfg
}

// newNSSMailHarness builds the harness over cfg (nssIDMailConfig when nil).
func newNSSMailHarness(t *testing.T, cfg *config.Config) *nssMailHarness {
	t.Helper()
	if cfg == nil {
		cfg = nssIDMailConfig()
	}
	h := newNSSHarness(t, cfg)
	return attachNSSMail(t, h)
}

// attachNSSMail wires the notification service and a fresh sender to h.
func attachNSSMail(t *testing.T, h *nssHarness) *nssMailHarness {
	t.Helper()
	sender := &recordingSender{}
	notifications := NewNotificationService(h.db, NewNotificationHandler(h.db, mustRenderer(t), sender).InlinePublisher(),
		true, true, false)
	notifications.SetAdminConfig(&h.svc.config.Admin)
	h.svc.SetNotifications(notifications, notifications)
	return &nssMailHarness{nssHarness: h, notifications: notifications, sender: sender}
}

// nssMailPerson is one users row and the groups its last login recorded.
type nssMailPerson struct {
	username string
	email    string
	disabled bool
	oidc     []string
	ldap     []string
	advanced bool
}

// seedMailPerson writes p and its user_groups rows, returning its id.
func seedMailPerson(t *testing.T, db *gorm.DB, p nssMailPerson) string {
	t.Helper()
	now := nssTestStart()
	user := model.User{
		ID: uuid.NewString(), Subject: "sub-" + p.username, Username: p.username, Email: p.email,
		CreatedAt: now, UpdatedAt: now,
	}
	if p.disabled {
		user.DisabledAt = &now
	}
	if p.advanced {
		level := model.DetailLevelAdvanced
		user.EmailDetailLevel = &level
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user %s: %v", p.username, err)
	}
	add := func(source model.GroupSource, names []string) {
		for _, name := range names {
			row := model.UserGroup{ID: uuid.NewString(), UserID: user.ID, GroupName: name, Source: source, FirstSeenAt: now, LastSeenAt: now}
			if err := db.Create(&row).Error; err != nil {
				t.Fatalf("seed group %s of %s: %v", name, p.username, err)
			}
		}
	}
	add(model.GroupSourceOIDC, p.oidc)
	add(model.GroupSourceLDAP, p.ldap)
	return user.ID
}

// seedAdmin writes an enabled admin with an address.
func seedAdmin(t *testing.T, db *gorm.DB, username string) string {
	t.Helper()
	return seedMailPerson(t, db, nssMailPerson{username: username, email: username + "@example.com", oidc: []string{"ssh-admins"}})
}

// inbox returns the messages sent to address.
func (h *nssMailHarness) inbox(address string) []mail.Outgoing {
	var out []mail.Outgoing
	for _, msg := range h.sender.messages() {
		if msg.To == address {
			out = append(out, msg)
		}
	}
	return out
}

// sentTo returns the addresses mailed so far, sorted.
func (h *nssMailHarness) sentTo() []string {
	var out []string
	for _, msg := range h.sender.messages() {
		out = append(out, msg.To)
	}
	slices.Sort(out)
	return out
}

// seedDirectory puts an account and a group in the directory and runs the
// first full sync, which stores them with nothing pending.
func (h *nssMailHarness) seedDirectory(t *testing.T) {
	t.Helper()
	h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
	h.dir.put(nssGroupEntry("devs", 5000, "ada"))
	h.fullSync(t)
}

// changeNumbers gives ada a new uid and devs a new gid in the directory.
func (h *nssMailHarness) changeNumbers(uid, gid uint32) {
	h.dir.put(nssAccountEntry("", "ada", uid, 1001, nil))
	h.dir.put(nssGroupEntry("devs", gid, "ada"))
}

// notifiedAt reads ada's and devs' pending_notified_at.
func (h *nssMailHarness) notifiedAt(t *testing.T) (*time.Time, *time.Time) {
	t.Helper()
	account, group := h.accountByName(t, "ada"), h.groupByName(t, "devs")
	return account.PendingNotifiedAt, group.PendingNotifiedAt
}

func TestNSSPendingMail_ShouldMailEachEligibleAdminOnceWhenAFullSyncMakesAChangePending(t *testing.T) {
	h := newNSSMailHarness(t, nil)
	seedAdmin(t, h.db, "alice")
	seedMailPerson(t, h.db, nssMailPerson{username: "gina", email: "gina@example.com", oidc: []string{"ssh-admins", "soc"}, advanced: true})
	seedMailPerson(t, h.db, nssMailPerson{username: "bob", email: "bob@example.com", oidc: []string{"ssh-admins"}, disabled: true})
	seedMailPerson(t, h.db, nssMailPerson{username: "carol", oidc: []string{"ssh-admins"}})
	seedMailPerson(t, h.db, nssMailPerson{username: "dave", email: "dave@example.com", oidc: []string{"soc"}})
	seedMailPerson(t, h.db, nssMailPerson{username: "erin", email: "erin@example.com", ldap: []string{"ssh-admins"}})
	frank := seedAdmin(t, h.db, "frank")
	if err := h.notifications.SetPreferences(t.Context(), frank, map[notify.Kind]bool{notify.KindNSSIDChangePending: false}); err != nil {
		t.Fatalf("SetPreferences: %v", err)
	}
	h.seedDirectory(t)
	if got := h.sentTo(); len(got) != 0 {
		t.Fatalf("the first sync mailed %v, want nothing while nothing is pending", got)
	}

	h.changeNumbers(2001, 5050)
	h.fullSync(t)

	tests := []struct {
		name    string
		address string
		want    int
	}{
		{name: "should mail an admin once when the sync makes changes pending", address: "alice@example.com", want: 1},
		{name: "should mail a second admin once when the sync makes changes pending", address: "gina@example.com", want: 1},
		{name: "should not mail a disabled admin", address: "bob@example.com"},
		{name: "should not mail a non-admin", address: "dave@example.com"},
		{name: "should not mail someone whose admin group came from the directory only", address: "erin@example.com"},
		{name: "should not mail an admin who switched the kind off", address: "frank@example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := len(h.inbox(tc.address)); got != tc.want {
				t.Errorf("%s got %d messages, want %d", tc.address, got, tc.want)
			}
		})
	}
	t.Run("should send nobody else anything", func(t *testing.T) {
		if got := h.sentTo(); !slices.Equal(got, []string{"alice@example.com", "gina@example.com"}) {
			t.Errorf("mailed %v, want alice and gina", got)
		}
	})
	t.Run("should name the count and both changes when it mails", func(t *testing.T) {
		msg := h.inbox("alice@example.com")[0]
		if msg.Subject != "2 NSS id changes are waiting for approval" {
			t.Errorf("subject = %q", msg.Subject)
		}
		for _, want := range []string{"ada", "1001", "2001", "devs", "5000", "5050", "https://ssoossh.example.com/admin/directory#pending-id-changes"} {
			if !strings.Contains(msg.Text, want) {
				t.Errorf("text body does not carry %q", want)
			}
		}
	})
	t.Run("should add the DN only for the admin who reads advanced", func(t *testing.T) {
		dn := "uid=ada,ou=people,dc=test"
		if strings.Contains(h.inbox("alice@example.com")[0].Text, dn) {
			t.Error("the simplified message carries the DN")
		}
		if !strings.Contains(h.inbox("gina@example.com")[0].Text, dn) {
			t.Error("the advanced message does not carry the DN")
		}
	})
}

func TestNSSPendingMail_ShouldMailOnlyWhatIsNewWhenSyncsRepeat(t *testing.T) {
	h := newNSSMailHarness(t, nil)
	seedAdmin(t, h.db, "alice")
	h.seedDirectory(t)
	h.changeNumbers(2001, 5050)
	h.fullSync(t)
	if got := len(h.inbox("alice@example.com")); got != 1 {
		t.Fatalf("the change mailed %d messages, want 1", got)
	}

	steps := []struct {
		name  string
		run   func(t *testing.T)
		want  int
		check string
	}{
		{
			name: "should send nothing when a second sync sees the same pending change",
			run:  func(t *testing.T) { h.clock.advance(time.Hour); h.fullSync(t) },
			want: 1,
		},
		{
			name: "should send nothing when a sweep follows the sync",
			run: func(t *testing.T) {
				if err := h.svc.SweepPendingIDChangeMail(t.Context()); err != nil {
					t.Fatalf("SweepPendingIDChangeMail: %v", err)
				}
			},
			want: 1,
		},
		{
			name:  "should mail again when the directory gives the same row a different new number",
			run:   func(t *testing.T) { h.clock.advance(time.Hour); h.changeNumbers(3001, 5050); h.fullSync(t) },
			want:  2,
			check: "3001",
		},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.run(t)
			msgs := h.inbox("alice@example.com")
			if len(msgs) != step.want {
				t.Fatalf("alice has %d messages, want %d", len(msgs), step.want)
			}
			if step.check != "" {
				last := msgs[len(msgs)-1]
				if last.Subject != "1 NSS id change is waiting for approval" || !strings.Contains(last.Text, step.check) {
					t.Errorf("the new mail is %q and does not list only the new uid %s:\n%s", last.Subject, step.check, last.Text)
				}
			}
		})
	}
}

func TestNSSPendingMail_ShouldMailNothingWhenTheChangeEndsBeforeTheSend(t *testing.T) {
	tests := []struct {
		name string
		end  func(t *testing.T, h *nssMailHarness)
	}{
		{
			name: "should mail nothing when an admin accepts the change before the sweep",
			end: func(t *testing.T, h *nssMailHarness) {
				if _, err := h.svc.AcceptUserIDChange(t.Context(), 1001, nssAdminActor()); err != nil {
					t.Fatalf("AcceptUserIDChange: %v", err)
				}
			},
		},
		{
			name: "should mail nothing when the directory reverts the change before the sweep",
			end: func(t *testing.T, h *nssMailHarness) {
				h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
				if _, err := h.svc.RefreshUserByUID(t.Context(), 1001); err != nil {
					t.Fatalf("RefreshUserByUID: %v", err)
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newNSSMailHarness(t, nil)
			seedAdmin(t, h.db, "alice")
			h.seedDirectory(t)
			h.dir.put(nssAccountEntry("", "ada", 2001, 1001, nil))
			if _, err := h.svc.RefreshUserByUID(t.Context(), 2001); err != nil {
				t.Fatalf("RefreshUserByUID: %v", err)
			}
			if row := h.accountByName(t, "ada"); row.PendingUID == nil {
				t.Fatal("the on-demand read left nothing pending")
			}

			tc.end(t, h)
			if err := h.svc.SweepPendingIDChangeMail(t.Context()); err != nil {
				t.Fatalf("SweepPendingIDChangeMail: %v", err)
			}

			if got := h.sentTo(); len(got) != 0 {
				t.Errorf("mailed %v, want nothing", got)
			}
			if row := h.accountByName(t, "ada"); row.PendingSince != nil || row.PendingNotifiedAt != nil {
				t.Errorf("pending_since = %v, pending_notified_at = %v, want both cleared", row.PendingSince, row.PendingNotifiedAt)
			}
		})
	}
}

func TestNSSPendingMail_ShouldClearTheMarkerWhenAMailedChangeIsAccepted(t *testing.T) {
	tests := []struct {
		name   string
		accept func(t *testing.T, h *nssMailHarness)
		read   func(t *testing.T, h *nssMailHarness) *time.Time
	}{
		{
			name: "should clear the account's marker when its change is accepted",
			accept: func(t *testing.T, h *nssMailHarness) {
				if _, err := h.svc.AcceptUserIDChange(t.Context(), 1001, nssAdminActor()); err != nil {
					t.Fatalf("AcceptUserIDChange: %v", err)
				}
			},
			read: func(t *testing.T, h *nssMailHarness) *time.Time { a, _ := h.notifiedAt(t); return a },
		},
		{
			name: "should clear the group's marker when its change is accepted",
			accept: func(t *testing.T, h *nssMailHarness) {
				if _, err := h.svc.AcceptGroupIDChange(t.Context(), 5000, nssAdminActor()); err != nil {
					t.Fatalf("AcceptGroupIDChange: %v", err)
				}
			},
			read: func(t *testing.T, h *nssMailHarness) *time.Time { _, g := h.notifiedAt(t); return g },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newNSSMailHarness(t, nil)
			seedAdmin(t, h.db, "alice")
			h.seedDirectory(t)
			h.changeNumbers(2001, 5050)
			h.fullSync(t)
			if tc.read(t, h) == nil {
				t.Fatal("the mailed change has no marker")
			}

			tc.accept(t, h)

			if got := tc.read(t, h); got != nil {
				t.Errorf("pending_notified_at = %v after the accept, want NULL", got)
			}
		})
	}
}

func TestNSSPendingMail_ShouldBatchWhatOnDemandAndLoginReadsMakePending(t *testing.T) {
	lh := newNSSLinkHarness(t, func(c *config.Config) {
		c.Mail.Enabled = true
		c.HTTP.PublicURL = "https://ssoossh.example.com"
	})
	h := attachNSSMail(t, lh.nssHarness)
	seedAdmin(t, h.db, "alice")
	h.dir.put(nssAccountEntry("", "ada", 1001, 1001, nil))
	h.dir.put(nssAccountEntry("", "bea", 1002, 1002, nil))
	h.dir.put(nssGroupEntry("devs", 5000, "ada"))
	h.fullSync(t)

	// Two on-demand reads by the new uids, then a login that reads a new
	// person whose group's gid changed.
	h.dir.put(nssAccountEntry("", "ada", 2001, 1001, nil))
	h.dir.put(nssAccountEntry("", "bea", 2002, 1002, nil))
	for _, uid := range []uint32{2001, 2002} {
		if _, err := h.svc.RefreshUserByUID(t.Context(), uid); err != nil {
			t.Fatalf("RefreshUserByUID(%d): %v", uid, err)
		}
	}
	h.dir.put(nssAccountEntry("", "zoe", 1003, 1003, nil))
	h.dir.put(nssGroupEntry("devs", 5050, "ada", "zoe"))
	lh.login(t, seedPerson(t, h.db, "u-zoe", "zoe"), "zoe")
	if got := h.sentTo(); len(got) != 0 {
		t.Fatalf("the reads mailed %v before the sweep, want nothing", got)
	}

	if err := h.svc.SweepPendingIDChangeMail(t.Context()); err != nil {
		t.Fatalf("SweepPendingIDChangeMail: %v", err)
	}

	msgs := h.inbox("alice@example.com")
	t.Run("should send one message for the batch", func(t *testing.T) {
		if len(msgs) != 1 {
			t.Fatalf("alice has %d messages, want 1", len(msgs))
		}
	})
	t.Run("should list every change the reads made pending", func(t *testing.T) {
		if len(msgs) != 1 {
			t.Skip("no single message to read")
		}
		if msgs[0].Subject != "3 NSS id changes are waiting for approval" {
			t.Errorf("subject = %q", msgs[0].Subject)
		}
		for _, want := range []string{"ada", "2001", "bea", "2002", "devs", "5050"} {
			if !strings.Contains(msgs[0].Text, want) {
				t.Errorf("text body does not carry %q", want)
			}
		}
	})
}

func TestNSSPendingMail_ShouldNotMailWhenItCannotReachAnAdmin(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, h *nssMailHarness)
		run     func(t *testing.T, h *nssMailHarness) error
	}{
		{
			name:    "should leave the change unclaimed when no admin can be mailed",
			prepare: func(*testing.T, *nssMailHarness) {},
			run:     func(t *testing.T, h *nssMailHarness) error { return h.svc.SweepPendingIDChangeMail(t.Context()) },
		},
		{
			name:    "should leave the change unclaimed when mail is off",
			prepare: func(t *testing.T, h *nssMailHarness) { seedAdmin(t, h.db, "alice"); h.svc.config.Mail.Enabled = false },
			run:     func(t *testing.T, h *nssMailHarness) error { return h.svc.SweepPendingIDChangeMail(t.Context()) },
		},
		{
			name:    "should leave the change unclaimed when a sync is running on this instance",
			prepare: func(t *testing.T, h *nssMailHarness) { seedAdmin(t, h.db, "alice"); h.svc.syncing.Store(true) },
			run:     func(t *testing.T, h *nssMailHarness) error { return h.svc.SweepPendingIDChangeMail(t.Context()) },
		},
		{
			name:    "should send no reminder when nothing was mailed yet",
			prepare: func(t *testing.T, h *nssMailHarness) { seedAdmin(t, h.db, "alice") },
			run:     func(t *testing.T, h *nssMailHarness) error { return h.svc.SweepPendingIDChangeReminder(t.Context()) },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newNSSMailHarness(t, nil)
			h.seedDirectory(t)
			h.dir.put(nssAccountEntry("", "ada", 2001, 1001, nil))
			if _, err := h.svc.RefreshUserByUID(t.Context(), 2001); err != nil {
				t.Fatalf("RefreshUserByUID: %v", err)
			}
			tc.prepare(t, h)

			if err := tc.run(t, h); err != nil {
				t.Fatalf("sweep: %v", err)
			}

			if got := h.sentTo(); len(got) != 0 {
				t.Errorf("mailed %v, want nothing", got)
			}
			if row := h.accountByName(t, "ada"); row.PendingNotifiedAt != nil {
				t.Errorf("pending_notified_at = %v, want the change left for a later send", row.PendingNotifiedAt)
			}
		})
	}
}

func TestNSSPendingMail_ShouldSendOnceWhenTwoInstancesShareTheDatabase(t *testing.T) {
	tests := []struct {
		name  string
		sweep func(s *NSSDirectoryService) func(*testing.T) error
		setup func(t *testing.T, h *nssMailHarness)
	}{
		{
			name: "should mail a new change once when both instances sweep",
			sweep: func(s *NSSDirectoryService) func(*testing.T) error {
				return func(t *testing.T) error { return s.SweepPendingIDChangeMail(t.Context()) }
			},
			setup: func(t *testing.T, h *nssMailHarness) {
				h.dir.put(nssAccountEntry("", "ada", 2001, 1001, nil))
				if _, err := h.svc.RefreshUserByUID(t.Context(), 2001); err != nil {
					t.Fatalf("RefreshUserByUID: %v", err)
				}
			},
		},
		{
			name: "should send one weekly reminder when both instances run it",
			sweep: func(s *NSSDirectoryService) func(*testing.T) error {
				return func(t *testing.T) error { return s.SweepPendingIDChangeReminder(t.Context()) }
			},
			setup: func(t *testing.T, h *nssMailHarness) {
				h.changeNumbers(2001, 5000)
				h.fullSync(t)
				h.sender.mu.Lock()
				h.sender.sent = nil
				h.sender.mu.Unlock()
				h.clock.advance(7 * 24 * time.Hour)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			first := newNSSMailHarness(t, nil)
			seedAdmin(t, first.db, "alice")
			first.seedDirectory(t)
			tc.setup(t, first)

			other, err := NewNSSDirectoryService(first.svc.config, first.db)
			if err != nil {
				t.Fatalf("NewNSSDirectoryService: %v", err)
			}
			other.dial, other.now = first.dir.dialer(), first.clock.now
			other.SetNotifications(first.notifications, first.notifications)

			for _, s := range []*NSSDirectoryService{first.svc, other} {
				if err := tc.sweep(s)(t); err != nil {
					t.Fatalf("sweep: %v", err)
				}
			}

			if got := len(first.inbox("alice@example.com")); got != 1 {
				t.Errorf("alice got %d messages from two instances, want 1", got)
			}
		})
	}
}

func TestNSSPendingMail_ShouldRemindWeeklyOfEverythingStillPending(t *testing.T) {
	h := newNSSMailHarness(t, nil)
	seedAdmin(t, h.db, "alice")
	h.seedDirectory(t)
	h.changeNumbers(2001, 5050)
	h.fullSync(t)
	reminders := func() []mail.Outgoing {
		var out []mail.Outgoing
		for _, msg := range h.inbox("alice@example.com") {
			if strings.HasPrefix(msg.Subject, "Reminder:") {
				out = append(out, msg)
			}
		}
		return out
	}
	remind := func(t *testing.T) {
		if err := h.svc.SweepPendingIDChangeReminder(t.Context()); err != nil {
			t.Fatalf("SweepPendingIDChangeReminder: %v", err)
		}
	}

	steps := []struct {
		name string
		run  func(t *testing.T)
		want int
	}{
		{name: "should remind of everything still pending when the reminder first runs", run: func(t *testing.T) { h.clock.advance(3 * 24 * time.Hour); remind(t) }, want: 1},
		{name: "should not remind again when the week is not over", run: func(t *testing.T) { h.clock.advance(24 * time.Hour); remind(t) }, want: 1},
		{name: "should remind again when a week has passed", run: func(t *testing.T) { h.clock.advance(6 * 24 * time.Hour); remind(t) }, want: 2},
		{
			name: "should not remind when nothing is pending any more",
			run: func(t *testing.T) {
				if _, err := h.svc.AcceptUserIDChange(t.Context(), 1001, nssAdminActor()); err != nil {
					t.Fatalf("AcceptUserIDChange: %v", err)
				}
				if _, err := h.svc.AcceptGroupIDChange(t.Context(), 5000, nssAdminActor()); err != nil {
					t.Fatalf("AcceptGroupIDChange: %v", err)
				}
				h.clock.advance(7 * 24 * time.Hour)
				remind(t)
			},
			want: 2,
		},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			step.run(t)
			if got := len(reminders()); got != step.want {
				t.Fatalf("alice has %d reminders, want %d", got, step.want)
			}
		})
	}
	t.Run("should list each change with how long it has waited", func(t *testing.T) {
		got := reminders()
		if len(got) == 0 {
			t.Skip("no reminder to read")
		}
		msg := got[0]
		if msg.Subject != "Reminder: 2 NSS id changes are still waiting for approval" {
			t.Errorf("subject = %q", msg.Subject)
		}
		for _, want := range []string{"ada", "2001", "devs", "5050", "waiting 3 days"} {
			if !strings.Contains(msg.Text, want) {
				t.Errorf("text body does not carry %q:\n%s", want, msg.Text)
			}
		}
	})
}

func TestNSSPendingMail_ShouldCapTheListAndCountTheRest(t *testing.T) {
	h := newNSSMailHarness(t, nil)
	seedAdmin(t, h.db, "alice")
	since := nssTestStart()
	for i := range 53 {
		pending := uint32(20000 + i)
		at := since.Add(time.Duration(i) * time.Minute)
		row := model.PosixUser{
			ID: uuid.NewString(), UID: uint32(10000 + i), Name: fmt.Sprintf("user%02d", i), DN: fmt.Sprintf("uid=user%02d,ou=people,dc=test", i),
			PendingUID: &pending, PendingSince: &at, CreatedAt: since, UpdatedAt: since, LastSyncedAt: since,
		}
		if err := h.db.Create(&row).Error; err != nil {
			t.Fatalf("seed account %d: %v", i, err)
		}
	}

	if err := h.svc.SweepPendingIDChangeMail(t.Context()); err != nil {
		t.Fatalf("SweepPendingIDChangeMail: %v", err)
	}

	msgs := h.inbox("alice@example.com")
	if len(msgs) != 1 {
		t.Fatalf("alice has %d messages, want 1", len(msgs))
	}
	tests := []struct {
		name    string
		want    string
		present bool
	}{
		{name: "should count every change in the subject", want: "53 NSS id changes", present: true},
		{name: "should list the oldest change", want: "user00", present: true},
		{name: "should list the fiftieth change", want: "user49", present: true},
		{name: "should leave the fifty-first change out", want: "user50", present: false},
		{name: "should count the changes it left out", want: "and 3 more", present: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Contains(msgs[0].Subject+"\n"+msgs[0].Text, tc.want)
			if got != tc.present {
				t.Errorf("message carries %q = %v, want %v", tc.want, got, tc.present)
			}
		})
	}
	t.Run("should claim every change, the unlisted ones included", func(t *testing.T) {
		if n := h.count(t, &model.PosixUser{}, "pending_notified_at IS NULL"); n != 0 {
			t.Errorf("%d changes left unclaimed, want 0", n)
		}
	})
}

func TestNSSPendingUpdates_ShouldClearTheMarkerWhenTheChangeIsNewOrGone(t *testing.T) {
	n := func(v uint32) *uint32 { return &v }
	since := nssTestStart()
	now := since.Add(time.Hour)
	tests := []struct {
		name      string
		since     *time.Time
		pairs     [][2]*uint32
		wantClear bool
		wantSince *time.Time
	}{
		{name: "should clear the marker when nothing is pending", pairs: [][2]*uint32{{nil, nil}}, wantClear: true},
		{name: "should clear the marker when the directory reverts", since: &since, pairs: [][2]*uint32{{nil, n(2001)}}, wantClear: true},
		{name: "should clear the marker when a number turns pending", pairs: [][2]*uint32{{n(2001), nil}}, wantClear: true, wantSince: &now},
		{name: "should clear the marker when the pending number changes", since: &since, pairs: [][2]*uint32{{n(3001), n(2001)}}, wantClear: true, wantSince: &now},
		{name: "should clear the marker when a second number turns pending", since: &since, pairs: [][2]*uint32{{n(2001), n(2001)}, {n(7), nil}}, wantClear: true, wantSince: &now},
		{name: "should keep the marker when the same number stays pending", since: &since, pairs: [][2]*uint32{{n(2001), n(2001)}}, wantSince: &since},
		{name: "should keep the marker when one of two pending numbers reverts", since: &since, pairs: [][2]*uint32{{n(2001), n(2001)}, {nil, n(7)}}, wantSince: &since},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			updates := map[string]any{}
			nssPendingUpdates(updates, tc.since, now, tc.pairs...)

			_, cleared := updates["pending_notified_at"]
			if cleared != tc.wantClear {
				t.Errorf("pending_notified_at cleared = %v, want %v", cleared, tc.wantClear)
			}
			got, _ := updates["pending_since"].(*time.Time)
			if (got == nil) != (tc.wantSince == nil) || (got != nil && !got.Equal(*tc.wantSince)) {
				t.Errorf("pending_since = %v, want %v", got, tc.wantSince)
			}
		})
	}
}
