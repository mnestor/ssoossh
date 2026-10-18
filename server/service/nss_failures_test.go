package service

import (
	"context"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/paging"
	"github.com/mnestor/ssoossh/test/testdb"
)

// The NSS services' database failure paths. Each case builds a working
// fixture, closes the database underneath it, and checks the call reports
// the failure rather than answering as if nothing were there.

func TestNSSServices_ShouldReturnTheErrorWhenTheDatabaseFails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		call func(ctx context.Context, f *nssUsageFixture) error
	}{
		{name: "should fail the key list when the read fails", call: func(ctx context.Context, f *nssUsageFixture) error {
			_, err := f.keys.List(ctx, nssAdmin)
			return err
		}},
		{name: "should fail the key read when the read fails", call: func(ctx context.Context, f *nssUsageFixture) error {
			_, err := f.keys.Get(ctx, nssAdmin, f.key.Key.ID)
			return err
		}},
		{name: "should fail the create when the caller cannot be resolved", call: func(ctx context.Context, f *nssUsageFixture) error {
			_, _, err := f.keys.Create(ctx, nssAdmin, NSSKeyCreate{Name: "x"}, "")
			return err
		}},
		{name: "should fail the update when the key cannot be read", call: func(ctx context.Context, f *nssUsageFixture) error {
			_, err := f.keys.Update(ctx, nssAdmin, f.key.Key.ID, NSSKeyUpdate{Description: ptr("the web tier")}, "")
			return err
		}},
		{name: "should fail the rotation when the key cannot be read", call: func(ctx context.Context, f *nssUsageFixture) error {
			_, _, err := f.keys.Rotate(ctx, nssAdmin, f.key.Key.ID, "")
			return err
		}},
		{name: "should fail the usage page when the read fails", call: func(ctx context.Context, f *nssUsageFixture) error {
			_, err := f.keys.Usage(ctx, nssAdmin, f.key.Key.ID, paging.Params{Limit: 2})
			return err
		}},
		{name: "should fail the report when the usage cannot be read", call: func(ctx context.Context, f *nssUsageFixture) error {
			_, err := f.keys.rotationReport(ctx, &f.key.Key)
			return err
		}},
		{name: "should fail the reminder sweep when the read fails", call: func(ctx context.Context, f *nssUsageFixture) error {
			return f.keys.SweepExpiryReminders(ctx)
		}},
		{name: "should fail the cleanup sweep when the read fails", call: func(ctx context.Context, f *nssUsageFixture) error {
			return f.keys.SweepRotationCleanup(ctx)
		}},
		{name: "should fail the prune when the delete fails", call: func(ctx context.Context, f *nssUsageFixture) error {
			return f.usage.PruneUsage(ctx)
		}},
		{name: "should fail the account read when the user cannot be resolved", call: func(ctx context.Context, f *nssUsageFixture) error {
			lookups := newTestNSSLookup(nssLookupTestConfig(), f.db, nil)
			_, err := lookups.AccountForIdentity(ctx, nssAdmin)
			return err
		}},
		{name: "should fail the shell change when the user cannot be resolved", call: func(ctx context.Context, f *nssUsageFixture) error {
			lookups := newTestNSSLookup(nssLookupTestConfig(), f.db, nil)
			_, err := lookups.SetShellPref(ctx, nssAdmin, nil, "")
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newNSSUsageFixture(t, nssMailConfig())
			sqlDB, err := f.db.DB()
			if err != nil {
				t.Fatalf("db: %v", err)
			}
			_ = sqlDB.Close()
			if err := tt.call(t.Context(), f); err == nil {
				t.Error("the call succeeded on a closed database, want its error")
			}
		})
	}
}

func TestNSSServices_ShouldLogAndCarryOnWhenABackgroundWriteFails(t *testing.T) {
	t.Parallel()

	f := newNSSUsageFixture(t, nssMailConfig())
	sqlDB, err := f.db.DB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	_ = sqlDB.Close()
	ctx := t.Context()

	// None of these returns an error to anyone; each must survive the
	// failure without raising anything.
	f.usage.raise(ctx, nssAlert{kind: nssAlertMissRate, subject: f.key.Key.ID, window: time.Hour,
		event: AuditEvent{Action: AuditNSSMissRateAlert, System: true, OccurredAt: nssNow}})
	f.usage.checkKeySources(ctx, []string{f.key.Key.ID})
	f.keys.notifyDeadKeyAttempt(ctx, &nssDeadKeyAttempt{key: f.key.Key, dead: NSSDeadKeyExpired, sourceIP: "192.0.2.1", at: nssNow})
	if mailed := kindsMailed(f.notifier, f.key.Key.ID); len(mailed) != 0 {
		t.Errorf("mailed %v without a claim", mailed)
	}
}

func TestNSSUsage_ShouldFlushOnTheTickerWhenRunning(t *testing.T) {
	t.Parallel()

	f := newNSSUsageFixture(t, nssLookupTestConfig())
	f.usage.flushInterval = 10 * time.Millisecond
	f.usage.Observe(t.Context(), f.verified("192.0.2.1", 1, NSSResultHit))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = f.usage.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int64
		f.db.Model(&model.NSSKeyUsage{}).Count(&count)
		if count == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the running loop never flushed the counts")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestNSSUsage_ShouldForgetOldMissWindowsWhenPruned(t *testing.T) {
	t.Parallel()

	f := newNSSUsageFixture(t, nssLookupTestConfig())
	f.usage.Observe(t.Context(), f.verified("192.0.2.1", 1, NSSResultMiss))
	f.usage.mu.Lock()
	f.usage.pruneLocked(nssNow.Add(nssMissWindow))
	kept := len(f.usage.misses)
	f.usage.pruneLocked(nssNow.Add(2 * nssMissWindow))
	left := len(f.usage.misses)
	f.usage.mu.Unlock()
	if kept != 1 || left != 0 {
		t.Errorf("miss windows kept %d then %d, want 1 then 0", kept, left)
	}
}

func TestNSSKeys_ShouldDescribeTheActorWhenTheMailNamesThem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		actor nssActor
		want  string
	}{
		{name: "should use the session's name when it has one", actor: nssActor{identity: &Identity{DisplayName: "Kim K"}, user: model.User{DisplayName: "Kim"}}, want: "Kim K"},
		{name: "should fall back to the stored name when the session has none", actor: nssActor{identity: &Identity{}, user: model.User{DisplayName: "Kim"}}, want: "Kim"},
		{name: "should be empty when neither has a name", actor: nssActor{identity: &Identity{}}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.actor.displayName(); got != tt.want {
				t.Errorf("displayName() = %q, want %q", got, tt.want)
			}
		})
	}
	if groups := identityGroups(nil); groups != nil {
		t.Errorf("identityGroups(nil) = %v, want nil", groups)
	}
}

func TestNSSKeys_ShouldListNothingWhenTheCallerHasNoGroupsAndNoRole(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	createNSSKey(t, keys, "web fleet", "web-admins")
	views, err := keys.List(t.Context(), nil)
	if err != nil || len(views) != 0 {
		t.Errorf("List(nil) = %v, %v; want nothing", views, err)
	}
}
