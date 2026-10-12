package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// should read each user's latest login on SQLite. The same table runs on
// Postgres under the dbparity tag (ldapsync_admin_pg_test.go).
func TestLatestLoginGroups_shouldReadEachUsersLatestLogin(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.AuditEvent{}); err != nil {
		t.Fatalf("migrate audit_events: %v", err)
	}
	checkLatestLoginGroups(t, db)
}

// checkLatestLoginGroups runs the latest-login cases against db, which must
// hold an empty, migrated audit_events table. Shared so both backends answer
// the same table.
func checkLatestLoginGroups(t *testing.T, db *gorm.DB) {
	t.Helper()

	now := time.Now()
	since := now.Add(-adminLoginWindow)
	day := 24 * time.Hour

	// alice: several logins, the latest wins.
	recordLogin(t, db, "alice", []string{"developers"}, now.Add(-10*day))
	recordLogin(t, db, "alice", []string{"ssh-admins"}, now.Add(-2*day))
	// bob: a denied login after his last good one is not a login.
	recordLogin(t, db, "bob", []string{"ssh-admins"}, now.Add(-3*day))
	recordAudit(t, db, AuditAuthLoginDenied, "bob", []string{"developers"}, now.Add(-day))
	// carol: only logged in before the window.
	recordLogin(t, db, "carol", []string{"ssh-admins"}, now.Add(-61*day))
	// dave: other actions carrying groups are not logins.
	recordAudit(t, db, AuditUserDisabled, "dave", []string{"ssh-admins"}, now.Add(-day))
	// erin: logged in once inside the window with no groups at all.
	recordLogin(t, db, "erin", nil, now.Add(-day))
	// A login whose users row was never resolved has no actor id to key on.
	recordLogin(t, db, "", []string{"ssh-admins"}, now.Add(-day))
	// frank: a latest login whose payload cannot be read is no evidence of
	// a role, even after a readable admin login.
	recordLogin(t, db, "frank", []string{"ssh-admins"}, now.Add(-3*day))
	frank := "frank"
	if err := db.Create(&model.AuditEvent{
		ID: "corrupt-login", CreatedAt: now.Add(-day).UTC(), ActorUserID: &frank,
		Payload: `{"action":"auth.login",not json`,
	}).Error; err != nil {
		t.Fatalf("store a corrupt login: %v", err)
	}

	got, err := latestLoginGroups(context.Background(), db, since)
	if err != nil {
		t.Fatalf("latestLoginGroups() error = %v", err)
	}

	tests := []struct {
		name       string
		userID     string
		wantFound  bool
		wantGroups []string
	}{
		{name: "should take the latest of several logins", userID: "alice", wantFound: true, wantGroups: []string{"ssh-admins"}},
		{name: "should ignore a denied login after the latest good one", userID: "bob", wantFound: true, wantGroups: []string{"ssh-admins"}},
		{name: "should leave out a user whose only login is older than the window", userID: "carol", wantFound: false},
		{name: "should leave out a user with no login, only other events", userID: "dave", wantFound: false},
		{name: "should report a login that carried no groups", userID: "erin", wantFound: true, wantGroups: nil},
		{name: "should leave out a user whose latest login cannot be read", userID: "frank", wantFound: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			groups, found := got[tc.userID]
			if found != tc.wantFound {
				t.Fatalf("found = %v, want %v (all: %v)", found, tc.wantFound, got)
			}
			if !slices.Equal(groups, tc.wantGroups) {
				t.Errorf("groups = %v, want %v", groups, tc.wantGroups)
			}
		})
	}

	t.Run("should key nothing on a login with no actor id", func(t *testing.T) {
		if _, found := got[""]; found {
			t.Error("a login with no actor id was keyed under the empty id")
		}
	})
}

// The prefix the query matches on depends on how the recorder encodes a
// login. If that encoding ever moves the action, this fails before the
// sync silently stops recognising every admin.
func TestLoginPayloadPrefix_shouldMatchARecordedLogin(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.AuditEvent{}); err != nil {
		t.Fatalf("migrate audit_events: %v", err)
	}
	recordLogin(t, db, "alice", []string{"ssh-admins"}, time.Now())
	recordAudit(t, db, AuditAuthLoginDenied, "bob", nil, time.Now())

	tests := []struct {
		name   string
		userID string
		want   int64
	}{
		{name: "should match a successful login", userID: "alice", want: 1},
		{name: "should not match a denied login", userID: "bob", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var count int64
			if err := db.Model(&model.AuditEvent{}).
				Where("actor_user_id = ? AND payload LIKE ?", tc.userID, loginPayloadPrefix).
				Count(&count).Error; err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != tc.want {
				t.Errorf("matched %d rows, want %d", count, tc.want)
			}
		})
	}
}
