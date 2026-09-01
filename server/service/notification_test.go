package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/mail"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
)

// recordingSender captures what would have been delivered, and can be made
// to fail or to block.
type recordingSender struct {
	mu   sync.Mutex
	sent []mail.Outgoing

	err   error
	block chan struct{}
}

func (s *recordingSender) Send(ctx context.Context, msg mail.Outgoing) error {
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.sent = append(s.sent, msg)
	return nil
}

func (s *recordingSender) messages() []mail.Outgoing {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]mail.Outgoing, len(s.sent))
	copy(out, s.sent)
	return out
}

// notificationFixture is a NotificationService and a running delivery
// handler on one in-process transport, mirroring bootstrap's wiring.
type notificationFixture struct {
	svc    *NotificationService
	db     *gorm.DB
	sender *recordingSender
	user   model.User
}

func newNotificationFixture(t *testing.T) *notificationFixture {
	t.Helper()
	return newNotificationFixtureWithSender(t, &recordingSender{})
}

func newNotificationFixtureWithSender(t *testing.T, sender *recordingSender) *notificationFixture {
	t.Helper()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.NotificationPreference{}, &model.Enrollment{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}

	user := model.User{
		ID:        uuid.NewString(),
		Subject:   "subject-1",
		Username:  "alice",
		Email:     "alice@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create the test user: %v", err)
	}

	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	renderer, err := mail.NewRenderer("", "")
	if err != nil {
		t.Fatalf("failed to build the renderer: %v", err)
	}

	router, err := message.NewRouter(message.RouterConfig{CloseTimeout: time.Second},
		watermill.NewSlogLogger(slog.Default()))
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}
	NewNotificationHandler(db, renderer, sender).Register(router, channel)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		if err := router.Run(ctx); err != nil {
			t.Errorf("router stopped with an error: %v", err)
		}
	}()
	<-router.Running()

	return &notificationFixture{
		svc:    NewNotificationService(db, channel, true),
		db:     db,
		sender: sender,
		user:   user,
	}
}

// sampleCreated is a payload with enough filled in to recognize the
// resulting message.
func sampleCreated() *notify.ServiceEnrollmentCreated {
	return &notify.ServiceEnrollmentCreated{
		ServiceAccount:      "deploy-bot",
		RequestID:           "req-1",
		EnrollmentID:        "enr-1",
		KeyID:               "alice",
		Principals:          []string{"deploy-bot"},
		ApprovedAt:          time.Now(),
		CodeExpiresAt:       time.Now().Add(90 * 24 * time.Hour),
		CertificateLifetime: 8 * time.Hour,
	}
}

// waitForMessages blocks until the sender has n messages or the deadline
// passes. Delivery is asynchronous by design, so nothing can assert on it
// synchronously.
func (f *notificationFixture) waitForMessages(t *testing.T, n int) []mail.Outgoing {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if msgs := f.sender.messages(); len(msgs) >= n {
			return msgs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waited for %d delivered message(s), got %d", n, len(f.sender.messages()))
	return nil
}

// assertNoMessages gives delivery a chance to happen and then asserts it did
// not. Without the wait this would pass even when the notification is on its
// way.
func (f *notificationFixture) assertNoMessages(t *testing.T) {
	t.Helper()

	time.Sleep(250 * time.Millisecond)
	if msgs := f.sender.messages(); len(msgs) != 0 {
		t.Fatalf("expected no delivery, got %d message(s): %+v", len(msgs), msgs)
	}
}

func TestNotify_shouldDeliverToTheUsersAddress(t *testing.T) {
	f := newNotificationFixture(t)

	f.svc.Notify(context.Background(), notify.KindServiceEnrollmentCreated, f.user.ID, sampleCreated())

	msgs := f.waitForMessages(t, 1)
	if msgs[0].To != "alice@example.com" {
		t.Errorf("delivered to %q", msgs[0].To)
	}
	if msgs[0].Subject == "" {
		t.Error("delivered an empty subject")
	}
}

// Notify is called from the approval and redemption paths, both of which a
// caller is waiting on. It must not wait on SMTP, so this asserts it returns
// while delivery is still blocked.
func TestNotify_shouldNotWaitForDelivery(t *testing.T) {
	sender := &recordingSender{block: make(chan struct{})}
	f := newNotificationFixtureWithSender(t, sender)

	done := make(chan struct{})
	go func() {
		f.svc.Notify(context.Background(), notify.KindServiceEnrollmentCreated, f.user.ID, sampleCreated())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Notify blocked while delivery was in progress")
	}

	// Release delivery and confirm the message really was still in flight,
	// so the test above proved something about a blocked send rather than
	// racing a completed one.
	close(sender.block)
	f.waitForMessages(t, 1)
}

// A canceled request context must not cancel the delivery it triggered: the
// browser that approved a request disconnects the moment it has its answer,
// and that is not a reason to drop the notification.
func TestNotify_shouldSurviveTheRequestContextEnding(t *testing.T) {
	f := newNotificationFixture(t)

	ctx, cancel := context.WithCancel(context.Background())
	f.svc.Notify(ctx, notify.KindServiceEnrollmentCreated, f.user.ID, sampleCreated())
	cancel()

	f.waitForMessages(t, 1)
}

func TestNotify_shouldDoNothingWhenMailIsDisabled(t *testing.T) {
	f := newNotificationFixture(t)
	f.svc.enabled = false

	f.svc.Notify(context.Background(), notify.KindServiceEnrollmentCreated, f.user.ID, sampleCreated())

	f.assertNoMessages(t)
}

func TestNotify_shouldDoNothingWithoutARecipient(t *testing.T) {
	f := newNotificationFixture(t)

	f.svc.Notify(context.Background(), notify.KindServiceEnrollmentCreated, "", sampleCreated())

	f.assertNoMessages(t)
}

func TestNotify_shouldNotPublishAnUnregisteredKind(t *testing.T) {
	f := newNotificationFixture(t)

	f.svc.Notify(context.Background(), "nope", f.user.ID, sampleCreated())

	f.assertNoMessages(t)
}

func TestNotificationHandler_shouldNotDeliverADisabledKind(t *testing.T) {
	f := newNotificationFixture(t)

	if err := f.svc.SetPreferences(context.Background(), f.user.ID,
		map[notify.Kind]bool{notify.KindServiceEnrollmentCreated: false}); err != nil {
		t.Fatalf("SetPreferences: %v", err)
	}

	f.svc.Notify(context.Background(), notify.KindServiceEnrollmentCreated, f.user.ID, sampleCreated())

	f.assertNoMessages(t)
}

// The preference is read at delivery, not at publication: an event that sat
// in a retry backoff must respect the answer the user gives in the meantime.
func TestNotificationHandler_shouldReadThePreferenceAtDeliveryTime(t *testing.T) {
	sender := &recordingSender{block: make(chan struct{})}
	f := newNotificationFixtureWithSender(t, sender)
	close(sender.block)

	f.svc.Notify(context.Background(), notify.KindServiceEnrollmentRedeemed, f.user.ID, &notify.ServiceEnrollmentRedeemed{
		ServiceAccount: "deploy-bot",
		Succeeded:      true,
	})
	f.waitForMessages(t, 1)

	if err := f.svc.SetPreferences(context.Background(), f.user.ID,
		map[notify.Kind]bool{notify.KindServiceEnrollmentRedeemed: false}); err != nil {
		t.Fatalf("SetPreferences: %v", err)
	}

	f.svc.Notify(context.Background(), notify.KindServiceEnrollmentRedeemed, f.user.ID, &notify.ServiceEnrollmentRedeemed{})
	time.Sleep(250 * time.Millisecond)
	if got := len(f.sender.messages()); got != 1 {
		t.Errorf("delivered %d messages, want the second one suppressed", got)
	}
}

func TestNotificationHandler_shouldSkipAUserWithNoAddress(t *testing.T) {
	f := newNotificationFixture(t)
	if err := f.db.Model(&model.User{}).Where("id = ?", f.user.ID).Update("email", "").Error; err != nil {
		t.Fatalf("clear email: %v", err)
	}

	f.svc.Notify(context.Background(), notify.KindServiceEnrollmentCreated, f.user.ID, sampleCreated())

	f.assertNoMessages(t)
}

func TestNotificationHandler_shouldSkipAnUnknownUser(t *testing.T) {
	f := newNotificationFixture(t)

	f.svc.Notify(context.Background(), notify.KindServiceEnrollmentCreated, uuid.NewString(), sampleCreated())

	f.assertNoMessages(t)
}

// A message nothing can parse will not parse on redelivery either, so it is
// acknowledged with a log line rather than retried forever.
func TestNotificationHandler_shouldDropAnUnparseableEvent(t *testing.T) {
	f := newNotificationFixture(t)

	handler := NewNotificationHandler(f.db, mustRenderer(t), f.sender)
	if err := handler.handle(message.NewMessage(watermill.NewUUID(), []byte("{not json"))); err != nil {
		t.Errorf("handle returned %v, want the message acknowledged", err)
	}
}

func TestNotificationHandler_shouldDropAnEventForAnUnregisteredKind(t *testing.T) {
	f := newNotificationFixture(t)

	payload, err := json.Marshal(notify.Event{Kind: "nope", UserID: f.user.ID, Payload: []byte("{}")})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	handler := NewNotificationHandler(f.db, mustRenderer(t), f.sender)
	if err := handler.handle(message.NewMessage(watermill.NewUUID(), payload)); err != nil {
		t.Errorf("handle returned %v, want the message acknowledged", err)
	}
}

// A relay that is down is temporary, so the message is nacked and the
// router's retry middleware backs off and tries again.
func TestNotificationHandler_shouldRetryADeliveryFailure(t *testing.T) {
	f := newNotificationFixture(t)
	f.sender.err = context.DeadlineExceeded

	event, err := notify.NewEvent(notify.KindServiceEnrollmentCreated, f.user.ID, sampleCreated())
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	handler := NewNotificationHandler(f.db, mustRenderer(t), f.sender)
	if err := handler.handle(message.NewMessage(watermill.NewUUID(), payload)); err == nil {
		t.Error("handle acknowledged a failed delivery")
	}
}

func TestPreferences_shouldReturnTheRegisteredDefaultsForANewUser(t *testing.T) {
	f := newNotificationFixture(t)

	prefs, err := f.svc.Preferences(context.Background(), f.user.ID)
	if err != nil {
		t.Fatalf("Preferences: %v", err)
	}

	if len(prefs) != len(notify.Definitions()) {
		t.Fatalf("got %d preferences, want one per registered kind (%d)", len(prefs), len(notify.Definitions()))
	}
	for i, def := range notify.Definitions() {
		if prefs[i].Kind != def.Kind {
			t.Errorf("preference %d is %q, want %q (registry order)", i, prefs[i].Kind, def.Kind)
		}
		if prefs[i].Enabled != def.DefaultEnabled {
			t.Errorf("%s enabled = %v, want the registered default %v", def.Kind, prefs[i].Enabled, def.DefaultEnabled)
		}
	}
}

func TestPreferences_shouldReturnAStoredChoice(t *testing.T) {
	f := newNotificationFixture(t)
	ctx := context.Background()

	if err := f.svc.SetPreferences(ctx, f.user.ID,
		map[notify.Kind]bool{notify.KindServiceEnrollmentCreated: false}); err != nil {
		t.Fatalf("SetPreferences: %v", err)
	}

	prefs, err := f.svc.Preferences(ctx, f.user.ID)
	if err != nil {
		t.Fatalf("Preferences: %v", err)
	}
	for _, pref := range prefs {
		if pref.Kind == notify.KindServiceEnrollmentCreated && pref.Enabled {
			t.Error("the stored choice was not returned")
		}
	}
}

// A row for a kind this build no longer registers is inert, not an error:
// it survives a downgrade and stops mattering rather than breaking the page.
func TestPreferences_shouldIgnoreARowForAnUnregisteredKind(t *testing.T) {
	f := newNotificationFixture(t)

	orphan := model.NotificationPreference{
		ID:        uuid.NewString(),
		UserID:    f.user.ID,
		Kind:      "removed_in_a_later_build",
		Enabled:   true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := f.db.Create(&orphan).Error; err != nil {
		t.Fatalf("create orphan row: %v", err)
	}

	prefs, err := f.svc.Preferences(context.Background(), f.user.ID)
	if err != nil {
		t.Fatalf("Preferences: %v", err)
	}
	for _, pref := range prefs {
		if pref.Kind == "removed_in_a_later_build" {
			t.Error("an unregistered kind was returned to the UI")
		}
	}
}

func TestSetPreferences_shouldUpdateAnExistingChoice(t *testing.T) {
	f := newNotificationFixture(t)
	ctx := context.Background()

	for _, want := range []bool{false, true, false} {
		if err := f.svc.SetPreferences(ctx, f.user.ID,
			map[notify.Kind]bool{notify.KindServiceEnrollmentCreated: want}); err != nil {
			t.Fatalf("SetPreferences(%v): %v", want, err)
		}

		enabled, err := f.svc.enabledFor(ctx, f.user.ID, notify.KindServiceEnrollmentCreated)
		if err != nil {
			t.Fatalf("enabledFor: %v", err)
		}
		if enabled != want {
			t.Errorf("enabled = %v, want %v", enabled, want)
		}
	}

	// One row per (user, kind) however many times it is written.
	var count int64
	if err := f.db.Model(&model.NotificationPreference{}).
		Where("user_id = ? AND kind = ?", f.user.ID, notify.KindServiceEnrollmentCreated).
		Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("stored %d rows for one (user, kind), want 1", count)
	}
}

func TestSetPreferences_shouldRejectAnUnregisteredKind(t *testing.T) {
	f := newNotificationFixture(t)

	err := f.svc.SetPreferences(context.Background(), f.user.ID, map[notify.Kind]bool{"nope": true})
	if err == nil {
		t.Fatal("SetPreferences accepted an unregistered kind")
	}

	var count int64
	if err := f.db.Model(&model.NotificationPreference{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("stored %d rows for a rejected update, want 0", count)
	}
}

// A partially applied update would leave the page showing something the
// user did not choose, so the whole set is rejected if any part of it is.
func TestSetPreferences_shouldRejectTheWholeSetWhenOneKindIsUnknown(t *testing.T) {
	f := newNotificationFixture(t)

	err := f.svc.SetPreferences(context.Background(), f.user.ID, map[notify.Kind]bool{
		notify.KindServiceEnrollmentCreated: false,
		"nope":                              true,
	})
	if err == nil {
		t.Fatal("SetPreferences accepted a set containing an unregistered kind")
	}

	var count int64
	if err := f.db.Model(&model.NotificationPreference{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("stored %d rows for a rejected update, want 0", count)
	}
}

func TestSetPreferences_shouldAcceptAnEmptyUpdate(t *testing.T) {
	f := newNotificationFixture(t)

	if err := f.svc.SetPreferences(context.Background(), f.user.ID, nil); err != nil {
		t.Errorf("SetPreferences on an empty set: %v", err)
	}
}

// mustRenderer builds the embedded-template renderer or fails the test.
func mustRenderer(t *testing.T) *mail.Renderer {
	t.Helper()

	renderer, err := mail.NewRenderer("", "")
	if err != nil {
		t.Fatalf("failed to build the renderer: %v", err)
	}
	return renderer
}

func TestPreferencesForIdentity_shouldResolveTheUserBySubject(t *testing.T) {
	f := newNotificationFixture(t)

	settings, err := f.svc.PreferencesForIdentity(context.Background(), &Identity{Subject: f.user.Subject})
	if err != nil {
		t.Fatalf("PreferencesForIdentity: %v", err)
	}

	if settings.Address != f.user.Email {
		t.Errorf("Address = %q, want %q", settings.Address, f.user.Email)
	}
	if len(settings.Kinds) != len(notify.Definitions()) {
		t.Errorf("got %d kinds, want %d", len(settings.Kinds), len(notify.Definitions()))
	}
	if !settings.MailEnabled {
		t.Error("MailEnabled is false, want it to mirror the server configuration")
	}
}

// A session whose users row is gone is a session that outlived its
// identity; it must not silently read or write someone else's preferences.
func TestPreferencesForIdentity_shouldRefuseAnIdentityWithNoUserRecord(t *testing.T) {
	f := newNotificationFixture(t)

	if _, err := f.svc.PreferencesForIdentity(context.Background(), &Identity{Subject: "no-such-subject"}); err == nil {
		t.Error("PreferencesForIdentity accepted an identity with no user record")
	}
}

func TestSetPreferencesForIdentity_shouldStoreAgainstTheResolvedUser(t *testing.T) {
	f := newNotificationFixture(t)
	ctx := context.Background()

	err := f.svc.SetPreferencesForIdentity(ctx, &Identity{Subject: f.user.Subject},
		map[notify.Kind]bool{notify.KindServiceEnrollmentCreated: false})
	if err != nil {
		t.Fatalf("SetPreferencesForIdentity: %v", err)
	}

	var pref model.NotificationPreference
	if err := f.db.First(&pref, "user_id = ?", f.user.ID).Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if pref.Enabled {
		t.Error("the stored preference was not the one submitted")
	}
}

func TestSetPreferencesForIdentity_shouldRefuseAnIdentityWithNoUserRecord(t *testing.T) {
	f := newNotificationFixture(t)

	err := f.svc.SetPreferencesForIdentity(context.Background(), &Identity{Subject: "no-such-subject"},
		map[notify.Kind]bool{notify.KindServiceEnrollmentCreated: false})
	if err == nil {
		t.Error("SetPreferencesForIdentity accepted an identity with no user record")
	}
}

// seedHolder adds a user holding accounts, for the service-account fan-out
// below. Everything a recipient needs is a row with an address and the
// account in its service_accounts JSON.
func (f *notificationFixture) seedHolder(t *testing.T, username string, accounts ...string) model.User {
	t.Helper()

	encoded, err := json.Marshal(accounts)
	if err != nil {
		t.Fatalf("failed to encode service accounts: %v", err)
	}
	user := model.User{
		ID:              uuid.NewString(),
		Subject:         "subject-" + username,
		Username:        username,
		Email:           username + "@example.com",
		ServiceAccounts: string(encoded),
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if err := f.db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create the holder %q: %v", username, err)
	}
	return user
}

// recipients returns the addresses n delivered messages went to, sorted so a
// fan-out's arrival order does not decide whether the test passes.
func (f *notificationFixture) recipients(t *testing.T, n int) []string {
	t.Helper()

	msgs := f.waitForMessages(t, n)
	to := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		to = append(to, msg.To)
	}
	sort.Strings(to)
	return to
}

// The whole of group ownership at delivery: an enrollment belongs to its
// service account, so a notification about one reaches everybody holding
// that account rather than the single person who approved it.
func TestNotifyServiceAccount_shouldDeliverToEveryHolder(t *testing.T) {
	f := newNotificationFixture(t)
	f.seedHolder(t, "bob", "deploy-bot")
	f.seedHolder(t, "carol", "deploy-bot", "backup-bot")

	f.svc.NotifyServiceAccount(context.Background(),
		notify.KindServiceEnrollmentCreated, "deploy-bot", sampleCreated())

	got := f.recipients(t, 2)
	want := []string{"bob@example.com", "carol@example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("delivered to %v, want %v", got, want)
	}
}

// A holder is somebody who holds the account, not somebody whose stored JSON
// happens to contain the name: the quoted match is what stops "deploy" from
// reaching the holders of "deploy-bot".
func TestNotifyServiceAccount_shouldNotDeliverToAHolderOfADifferentAccount(t *testing.T) {
	f := newNotificationFixture(t)
	f.seedHolder(t, "bob", "deploy-bot")
	f.seedHolder(t, "carol", "deploy")

	f.svc.NotifyServiceAccount(context.Background(),
		notify.KindServiceEnrollmentCreated, "deploy", sampleCreated())

	got := f.recipients(t, 1)
	if !slices.Equal(got, []string{"carol@example.com"}) {
		t.Errorf("delivered to %v, want only the holder of the named account", got)
	}
}

// Each copy is gated separately, so one holder opting out silences their own
// mail and nobody else's.
func TestNotifyServiceAccount_shouldRespectEachHoldersOwnPreference(t *testing.T) {
	f := newNotificationFixture(t)
	bob := f.seedHolder(t, "bob", "deploy-bot")
	f.seedHolder(t, "carol", "deploy-bot")

	if err := f.svc.SetPreferences(context.Background(), bob.ID,
		map[notify.Kind]bool{notify.KindServiceEnrollmentCreated: false}); err != nil {
		t.Fatalf("SetPreferences: %v", err)
	}

	f.svc.NotifyServiceAccount(context.Background(),
		notify.KindServiceEnrollmentCreated, "deploy-bot", sampleCreated())

	got := f.recipients(t, 1)
	if !slices.Equal(got, []string{"carol@example.com"}) {
		t.Errorf("delivered to %v, want only the holder who did not opt out", got)
	}
}

// A disabled account has lost its access, so it loses the mail about what
// that access used to reach — the same rule GroupRecipients applies.
func TestNotifyServiceAccount_shouldSkipDisabledHolders(t *testing.T) {
	f := newNotificationFixture(t)
	bob := f.seedHolder(t, "bob", "deploy-bot")
	f.seedHolder(t, "carol", "deploy-bot")

	disabledAt := time.Now()
	if err := f.db.Model(&model.User{}).Where("id = ?", bob.ID).
		Update("disabled_at", disabledAt).Error; err != nil {
		t.Fatalf("failed to disable the holder: %v", err)
	}

	f.svc.NotifyServiceAccount(context.Background(),
		notify.KindServiceEnrollmentCreated, "deploy-bot", sampleCreated())

	got := f.recipients(t, 1)
	if !slices.Equal(got, []string{"carol@example.com"}) {
		t.Errorf("delivered to %v, want only the enabled holder", got)
	}
}

// seedEnrollment adds an enrollment row carrying address, which is what the
// delivery path reads to decide between the address and fan-out.
func (f *notificationFixture) seedEnrollment(t *testing.T, id, account, address string) {
	t.Helper()

	enrollment := model.Enrollment{
		ID:                id,
		Code:              "code-" + id,
		ServiceAccount:    account,
		NotificationEmail: address,
		CreatedAt:         time.Now(),
		ExpiresAt:         time.Now().Add(24 * time.Hour),
	}
	if err := f.db.Create(&enrollment).Error; err != nil {
		t.Fatalf("failed to create the enrollment %q: %v", id, err)
	}
}

// The address's first job: reaching somebody at all where fan-out reaches
// nobody, because no holder of the account has ever logged in.
func TestNotifyEnrollment_shouldDeliverToTheEnrollmentAddressInsteadOfFanningOut(t *testing.T) {
	f := newNotificationFixture(t)
	f.seedHolder(t, "bob", "deploy-bot")
	f.seedEnrollment(t, "enr-1", "deploy-bot", "deploys@example.com")

	f.svc.NotifyEnrollment(context.Background(),
		notify.KindServiceEnrollmentCreated, "enr-1", "deploy-bot", sampleCreated())

	got := f.recipients(t, 1)
	if !slices.Equal(got, []string{"deploys@example.com"}) {
		t.Errorf("delivered to %v, want only the enrollment's own address", got)
	}
}

// With no address set the enrollment form behaves exactly like the account
// form, so setting one is the only thing that changes who hears.
func TestNotifyEnrollment_shouldFanOutWhenNoAddressIsSet(t *testing.T) {
	f := newNotificationFixture(t)
	f.seedHolder(t, "bob", "deploy-bot")
	f.seedHolder(t, "carol", "deploy-bot")
	f.seedEnrollment(t, "enr-1", "deploy-bot", "")

	f.svc.NotifyEnrollment(context.Background(),
		notify.KindServiceEnrollmentCreated, "enr-1", "deploy-bot", sampleCreated())

	got := f.recipients(t, 2)
	want := []string{"bob@example.com", "carol@example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("delivered to %v, want %v", got, want)
	}
}

// A set address is the account's own subscription, entered deliberately at
// approval or by a holder afterwards. With no single owning user there is no
// principled preference that could gate it, so an opted-out holder does not
// silence it.
func TestNotifyEnrollment_shouldSendToTheAddressUngated(t *testing.T) {
	f := newNotificationFixture(t)
	bob := f.seedHolder(t, "bob", "deploy-bot")
	f.seedEnrollment(t, "enr-1", "deploy-bot", "deploys@example.com")

	if err := f.svc.SetPreferences(context.Background(), bob.ID,
		map[notify.Kind]bool{notify.KindServiceEnrollmentCreated: false}); err != nil {
		t.Fatalf("SetPreferences: %v", err)
	}

	f.svc.NotifyEnrollment(context.Background(),
		notify.KindServiceEnrollmentCreated, "enr-1", "deploy-bot", sampleCreated())

	got := f.recipients(t, 1)
	if !slices.Equal(got, []string{"deploys@example.com"}) {
		t.Errorf("delivered to %v, want the address regardless of any holder's preference", got)
	}
}

// An enrollment can be deleted while one of its events is still queued. The
// right answer then is the account's holders, not a failed delivery.
func TestNotifyEnrollment_shouldFallBackToHoldersWhenTheEnrollmentIsGone(t *testing.T) {
	f := newNotificationFixture(t)
	f.seedHolder(t, "bob", "deploy-bot")

	f.svc.NotifyEnrollment(context.Background(),
		notify.KindServiceEnrollmentCreated, "enr-vanished", "deploy-bot", sampleCreated())

	got := f.recipients(t, 1)
	if !slices.Equal(got, []string{"bob@example.com"}) {
		t.Errorf("delivered to %v, want the account's holders", got)
	}
}

// An enrollment whose principals never parsed has no service account, so
// fan-out reaches nobody by construction. Its address is then the only way
// anyone hears about it, which is why this form publishes rather than
// dropping the event the way NotifyServiceAccount does.
func TestNotifyEnrollment_shouldReachTheAddressOfAnAccountlessEnrollment(t *testing.T) {
	f := newNotificationFixture(t)
	f.seedEnrollment(t, "enr-orphan", "", "deploys@example.com")

	f.svc.NotifyEnrollment(context.Background(),
		notify.KindServiceEnrollmentCreated, "enr-orphan", "", sampleCreated())

	got := f.recipients(t, 1)
	if !slices.Equal(got, []string{"deploys@example.com"}) {
		t.Errorf("delivered to %v, want the accountless enrollment's own address", got)
	}
}

// An account whose holders have never logged in is reachable by nobody. That
// is a quiet outcome rather than an error, and it is the gap the
// per-enrollment notification address exists to close.
func TestNotifyServiceAccount_shouldStayQuietWhenNobodyHoldsTheAccount(t *testing.T) {
	f := newNotificationFixture(t)

	f.svc.NotifyServiceAccount(context.Background(),
		notify.KindServiceEnrollmentCreated, "nobody-holds-this", sampleCreated())

	f.assertNoMessages(t)
}

// An enrollment whose stored principals never parsed carries no account, so
// it is owned by nobody. Publishing an event addressed to "" would fan out
// to every user whose service_accounts contains an empty string.
func TestNotifyServiceAccount_shouldRefuseAnEmptyAccount(t *testing.T) {
	f := newNotificationFixture(t)
	f.seedHolder(t, "bob", "")

	f.svc.NotifyServiceAccount(context.Background(),
		notify.KindServiceEnrollmentCreated, "", sampleCreated())

	f.assertNoMessages(t)
}
