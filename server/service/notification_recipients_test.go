package service

// Test methodology: unit tests over the two fan-out recipient resolvers and
// the small publication plumbing around them, against in-memory SQLite. The
// properties that matter are the exclusion rules — disabled accounts and
// addressless users never receive fan-out — and the exact-element matching
// of the JSON-encoded service account list, where a substring false
// positive would email the wrong people.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
)

// recipientsDB is an in-memory database with the tables the resolvers read.
func recipientsDB(t *testing.T) *gorm.DB {
	t.Helper()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.UserGroup{}, &model.NotificationPreference{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	return db
}

// addUser inserts one user row and returns it. serviceAccounts is stored
// verbatim so a test can plant malformed JSON.
func addUser(t *testing.T, db *gorm.DB, username, email, serviceAccounts string, disabled bool) model.User {
	t.Helper()

	user := model.User{
		ID:              uuid.NewString(),
		Subject:         "sub-" + username,
		Username:        username,
		Email:           email,
		ServiceAccounts: serviceAccounts,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if disabled {
		now := time.Now()
		user.DisabledAt = &now
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user %s: %v", username, err)
	}
	return user
}

// addGroupRow records one group membership capture for a user.
func addGroupRow(t *testing.T, db *gorm.DB, userID, group string, source model.GroupSource) {
	t.Helper()

	row := model.UserGroup{
		ID:          uuid.NewString(),
		UserID:      userID,
		GroupName:   group,
		Source:      source,
		FirstSeenAt: time.Now(),
		LastSeenAt:  time.Now(),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("failed to create group row: %v", err)
	}
}

// usernames flattens a recipient list for comparison.
func usernames(users []model.User) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.Username)
	}
	return out
}

func TestGroupRecipients_ShouldApplyTheExclusionRules(t *testing.T) {
	t.Parallel()

	db := recipientsDB(t)
	svc := NewNotificationService(db, nil, true)

	member := addUser(t, db, "alice", "alice@example.com", "", false)
	addGroupRow(t, db, member.ID, "ops", model.GroupSourceOIDC)

	disabled := addUser(t, db, "mallory", "mallory@example.com", "", true)
	addGroupRow(t, db, disabled.ID, "ops", model.GroupSourceOIDC)

	addressless := addUser(t, db, "bob", "", "", false)
	addGroupRow(t, db, addressless.ID, "ops", model.GroupSourceLDAP)

	other := addUser(t, db, "carol", "carol@example.com", "", false)
	addGroupRow(t, db, other.ID, "dev", model.GroupSourceOIDC)

	got, err := svc.GroupRecipients(t.Context(), "ops")
	if err != nil {
		t.Fatalf("GroupRecipients: %v", err)
	}
	if names := usernames(got); len(names) != 1 || names[0] != "alice" {
		t.Errorf("GroupRecipients(ops) = %v, want only the enabled, addressed member", names)
	}
}

// A user captured into the same group from both sources has two rows; the
// resolver must still reach them once, not twice.
func TestGroupRecipients_ShouldNotDoubleSendAcrossCaptureSources(t *testing.T) {
	t.Parallel()

	db := recipientsDB(t)
	svc := NewNotificationService(db, nil, true)

	member := addUser(t, db, "alice", "alice@example.com", "", false)
	addGroupRow(t, db, member.ID, "ops", model.GroupSourceOIDC)
	addGroupRow(t, db, member.ID, "ops", model.GroupSourceLDAP)

	got, err := svc.GroupRecipients(t.Context(), "ops")
	if err != nil {
		t.Fatalf("GroupRecipients: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("GroupRecipients(ops) returned %d rows, want the member once", len(got))
	}
}

func TestGroupRecipients_ShouldReturnNothingForAnEmptyName(t *testing.T) {
	t.Parallel()

	svc := NewNotificationService(recipientsDB(t), nil, true)

	got, err := svc.GroupRecipients(t.Context(), "")
	if err != nil {
		t.Fatalf("GroupRecipients: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("GroupRecipients(\"\") = %v, want no recipients", usernames(got))
	}
}

func TestServiceAccountRecipients_ShouldMatchWholeElementsOnly(t *testing.T) {
	t.Parallel()

	db := recipientsDB(t)
	svc := NewNotificationService(db, nil, true)

	addUser(t, db, "holder", "holder@example.com", `["deploy-bot","other-bot"]`, false)
	addUser(t, db, "prefix", "prefix@example.com", `["deploy-bot-staging"]`, false)
	addUser(t, db, "disabled", "disabled@example.com", `["deploy-bot"]`, true)
	addUser(t, db, "addressless", "", `["deploy-bot"]`, false)

	got, err := svc.ServiceAccountRecipients(t.Context(), "deploy-bot")
	if err != nil {
		t.Fatalf("ServiceAccountRecipients: %v", err)
	}
	if names := usernames(got); len(names) != 1 || names[0] != "holder" {
		t.Errorf("ServiceAccountRecipients(deploy-bot) = %v, want only the enabled, addressed holder", names)
	}
}

// A row whose stored list does not parse is the case the confirm-by-decode
// step exists for: the LIKE can match inside garbage, and one bad row must
// neither be notified on a substring match nor silence everyone else.
func TestServiceAccountRecipients_ShouldSkipAnUnparseableRow(t *testing.T) {
	t.Parallel()

	db := recipientsDB(t)
	svc := NewNotificationService(db, nil, true)

	addUser(t, db, "good", "good@example.com", `["deploy-bot"]`, false)
	addUser(t, db, "corrupt", "corrupt@example.com", `oops "deploy-bot" oops`, false)

	got, err := svc.ServiceAccountRecipients(t.Context(), "deploy-bot")
	if err != nil {
		t.Fatalf("ServiceAccountRecipients: %v", err)
	}
	if names := usernames(got); len(names) != 1 || names[0] != "good" {
		t.Errorf("ServiceAccountRecipients(deploy-bot) = %v, want the parseable holder only", names)
	}
}

func TestServiceAccountRecipients_ShouldReturnNothingForAnEmptyName(t *testing.T) {
	t.Parallel()

	svc := NewNotificationService(recipientsDB(t), nil, true)

	got, err := svc.ServiceAccountRecipients(t.Context(), "")
	if err != nil {
		t.Fatalf("ServiceAccountRecipients: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ServiceAccountRecipients(\"\") = %v, want no recipients", usernames(got))
	}
}

// MailEnabled is what the preferences page uses to explain why toggles
// produce no mail; it must mirror the constructor's flag exactly.
func TestMailEnabled_ShouldMirrorTheConstructorFlag(t *testing.T) {
	t.Parallel()

	if !NewNotificationService(nil, nil, true).MailEnabled() {
		t.Error("MailEnabled() = false for a service built enabled")
	}
	if NewNotificationService(nil, nil, false).MailEnabled() {
		t.Error("MailEnabled() = true for a service built disabled")
	}
}

// The discard Notifier is what every service holds until bootstrap attaches
// a real one; both methods must be safe to call with anything, including a
// nil payload, or the no-mail deployment panics where the mailed one works.
func TestDiscardNotifications_ShouldBeSafeToCall(t *testing.T) {
	t.Parallel()

	var n Notifier = discardNotifications{}
	n.Notify(t.Context(), notify.KindServiceEnrollmentCreated, "user-1", nil)
	n.NotifyServiceAccount(t.Context(), notify.KindServiceEnrollmentRedeemed, "deploy-bot", nil)
}

// A notification that cannot be queued is logged and dropped: the work it
// describes already succeeded, so the failure must not escape.
// failingPublisher comes from certrequest_test.go.
func TestNotify_ShouldAbsorbAPublishFailure(t *testing.T) {
	t.Parallel()

	svc := NewNotificationService(recipientsDB(t), failingPublisher{}, true)
	svc.Notify(t.Context(), notify.KindServiceEnrollmentCreated, "user-1", &notify.ServiceEnrollmentCreated{})
	svc.NotifyServiceAccount(t.Context(), notify.KindServiceEnrollmentRedeemed, "deploy-bot", &notify.ServiceEnrollmentRedeemed{})
}

// The database failing mid-read is the one branch of the preference lookup
// the delivery tests cannot reach; a closed handle stands in for it.
func TestEnabledFor_ShouldReportADatabaseFailure(t *testing.T) {
	t.Parallel()

	db := recipientsDB(t)
	svc := NewNotificationService(db, nil, true)

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrapping the sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("closing the database: %v", err)
	}

	if _, err := svc.enabledFor(context.Background(), "user-1", notify.KindServiceEnrollmentCreated); err == nil {
		t.Error("enabledFor on a closed database returned no error")
	}
}
