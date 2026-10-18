package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// Shared fixtures for the NSS service tests: a configuration with NSS on,
// a fixed clock, POSIX rows, fleet keys and a fake directory refresher.

// nssNow is the fixed instant every NSS test runs at, whole seconds so a
// value read back compares equal on both backends.
var nssNow = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// nssLookupClock returns a clock fixed at nssNow.
func nssLookupClock() func() time.Time { return func() time.Time { return nssNow } }

// nssLookupTestConfig is a configuration with NSS on and the shipped defaults,
// the four role groups named, and mail off.
func nssLookupTestConfig() *config.Config {
	c := &config.Config{}
	c.HTTP.PublicURL = "https://ssh.example.org"
	c.Admin = config.AdminConfig{RequireGroup: "admins", SOCGroup: "soc", AuditorGroup: "auditors", NSSKeyGroup: "nss-admins"}
	c.NSS = config.NSSConfig{
		Enabled:             true,
		AutoPrivateGroups:   config.AutoPrivateGroupsOff,
		TombstoneRetention:  2160 * time.Hour,
		DefaultHome:         "/home/%u",
		DisabledShell:       "/usr/sbin/nologin",
		AllowedShells:       []string{"sh", "bash", "zsh", "fish"},
		MaxGroupMembers:     2000,
		RateLimit:           20,
		RateBurst:           200,
		MissAlertRate:       5,
		UsageRetention:      2160 * time.Hour,
		KeyDefaultLifetime:  8760 * time.Hour,
		KeyRotationGrace:    720 * time.Hour,
		PrivilegedNameAlert: []string{"root", "admin", "daemon", "bin", "sys"},
		KeySourceAlert:      1000,
	}
	c.Mail.ExpiryReminderLead = 168 * time.Hour
	c.Mail.ExpiredAttemptWindow = 24 * time.Hour
	c.Mail.RotationQuietPeriod = 168 * time.Hour
	return c
}

// seedPosixUser writes a posix_users row, filling the anchors and
// timestamps a test leaves out, and returns it as stored.
func seedPosixUser(t *testing.T, db *gorm.DB, row model.PosixUser) model.PosixUser {
	t.Helper()

	if row.ID == "" {
		row.ID = uuid.NewString()
	}
	if row.DN == "" {
		row.DN = fmt.Sprintf("uid=%s,ou=people,dc=example", row.Name)
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt, row.UpdatedAt, row.LastSyncedAt = nssNow, nssNow, nssNow
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed posix user %q: %v", row.Name, err)
	}
	return row
}

// seedPosixGroup writes a posix_groups row the same way.
func seedPosixGroup(t *testing.T, db *gorm.DB, row model.PosixGroup) model.PosixGroup {
	t.Helper()

	if row.ID == "" {
		row.ID = uuid.NewString()
	}
	if row.DN == "" {
		row.DN = fmt.Sprintf("cn=%s,ou=groups,dc=example", row.Name)
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt, row.UpdatedAt = nssNow, nssNow
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed posix group %q: %v", row.Name, err)
	}
	return row
}

// seedPosixMembers writes one membership row per uid in gid.
func seedPosixMembers(t *testing.T, db *gorm.DB, gid uint32, uids ...uint32) {
	t.Helper()

	for _, uid := range uids {
		if err := db.Create(&model.PosixGroupMember{GID: gid, UID: uid}).Error; err != nil {
			t.Fatalf("seed membership %d in %d: %v", uid, gid, err)
		}
	}
}

// seedNSSPerson writes a users row with the given subject and username,
// disabled at disabledAt when it is set, and returns its id.
func seedNSSPerson(t *testing.T, db *gorm.DB, subject, username string, disabledAt *time.Time) string {
	t.Helper()

	user := model.User{
		ID: uuid.NewString(), Subject: subject, Username: username, Email: username + "@example.org",
		CreatedAt: nssNow, UpdatedAt: nssNow, DisabledAt: disabledAt,
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("seed user %q: %v", username, err)
	}
	return user.ID
}

// nssAt returns nssNow moved by d, as a pointer for nullable columns.
func nssAt(d time.Duration) *time.Time {
	v := nssNow.Add(d)
	return &v
}

// fakeNSSRefresher stands in for the directory half. Each call is
// recorded; onRefresh, when set, runs first (to write the row a real
// refresh would), then found and err are returned.
type fakeNSSRefresher struct {
	mu        sync.Mutex
	calls     []string
	found     bool
	err       error
	onRefresh func(call string)
}

// record notes one call and answers it.
func (f *fakeNSSRefresher) record(call string) (bool, error) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	hook := f.onRefresh
	f.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	return f.found, f.err
}

// RefreshUserByName records "user-name:<name>".
func (f *fakeNSSRefresher) RefreshUserByName(_ context.Context, name string) (bool, error) {
	return f.record("user-name:" + name)
}

// RefreshUserByUID records "user-uid:<uid>".
func (f *fakeNSSRefresher) RefreshUserByUID(_ context.Context, uid uint32) (bool, error) {
	return f.record(fmt.Sprintf("user-uid:%d", uid))
}

// RefreshGroupByName records "group-name:<name>".
func (f *fakeNSSRefresher) RefreshGroupByName(_ context.Context, name string) (bool, error) {
	return f.record("group-name:" + name)
}

// RefreshGroupByGID records "group-gid:<gid>".
func (f *fakeNSSRefresher) RefreshGroupByGID(_ context.Context, gid uint32) (bool, error) {
	return f.record(fmt.Sprintf("group-gid:%d", gid))
}

// recorded returns the calls so far.
func (f *fakeNSSRefresher) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

// newTestNSSLookup builds the lookup service over db at the fixed clock.
func newTestNSSLookup(c *config.Config, db *gorm.DB, refresher nssDirectoryRefresher) *NSSLookupService {
	s := NewNSSLookupService(c, db, refresher)
	s.now = nssLookupClock()
	return s
}
