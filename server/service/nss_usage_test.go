package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/test/testdb"
)

// newTestNSSUsage builds the usage service over db and keys at the fixed
// clock, with an auditor and the no-op meter.
func newTestNSSUsage(t *testing.T, c *config.Config, db *gorm.DB, keys *NSSKeyService) *NSSUsageService {
	t.Helper()

	usage, err := NewNSSUsageService(c, db, keys, noop.NewMeterProvider().Meter("test"))
	if err != nil {
		t.Fatalf("NewNSSUsageService: %v", err)
	}
	usage.now = nssClock()
	usage.SetAuditor(NewAuditService(c, db))
	return usage
}

// nssUsageFixture is a database with the callers, one key and the three
// services over it.
type nssUsageFixture struct {
	db       *gorm.DB
	keys     *NSSKeyService
	notifier *capturingNotifier
	usage    *NSSUsageService
	key      *NSSKeyView
}

// newNSSUsageFixture builds the fixture with c.
func newNSSUsageFixture(t *testing.T, c *config.Config) *nssUsageFixture {
	t.Helper()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, notifier := newTestNSSKeys(t, c, db)
	key, _ := createNSSKey(t, keys, "web fleet", "web-admins")
	return &nssUsageFixture{db: db, keys: keys, notifier: notifier, usage: newTestNSSUsage(t, c, db, keys), key: key}
}

// verified is an outcome of a lookup with the fixture's key, generation
// generation, from ip, ending as result.
func (f *nssUsageFixture) verified(ip string, generation int, result string) NSSLookupOutcome {
	return NSSLookupOutcome{
		Kind: NSSKindUser, By: NSSByName, Result: result, KeyID: f.key.Key.ID, SourceIP: ip,
		Verdict: NSSKeyVerdict{KeyID: f.key.Key.ID, OK: true, Generation: generation, Key: &f.key.Key},
	}
}

// usageRows reads every usage row, by address then generation.
func usageRows(t *testing.T, db *gorm.DB) []model.NSSKeyUsage {
	t.Helper()

	var rows []model.NSSKeyUsage
	if err := db.Order("source_ip, generation").Find(&rows).Error; err != nil {
		t.Fatalf("read usage: %v", err)
	}
	return rows
}

func TestNSSUsage_ShouldAggregateAnsweredLookupsWhenTheyAreFlushed(t *testing.T) {
	t.Parallel()

	f := newNSSUsageFixture(t, nssTestConfig())
	ctx := t.Context()
	f.usage.Observe(ctx, f.verified("192.0.2.1", 1, NSSResultHit))
	f.usage.Observe(ctx, f.verified("192.0.2.1", 1, NSSResultHit))
	f.usage.Observe(ctx, f.verified("192.0.2.1", 1, NSSResultMiss))
	f.usage.Observe(ctx, f.verified("192.0.2.1", 1, NSSResultBadRequest))
	f.usage.Observe(ctx, f.verified("192.0.2.1", 2, NSSResultHit))
	f.usage.Observe(ctx, f.verified("192.0.2.2", 1, NSSResultMiss))
	// Neither of these was served, so neither is usage.
	f.usage.Observe(ctx, f.verified("192.0.2.1", 1, NSSResultRateLimited))
	f.usage.Observe(ctx, NSSLookupOutcome{Kind: NSSKindUser, By: NSSByName, Result: NSSResultUnauthorized, KeyID: f.key.Key.ID, SourceIP: "192.0.2.1"})

	if err := f.usage.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	rows := usageRows(t, f.db)
	want := []struct {
		ip              string
		generation      int
		lookups, misses int64
	}{{"192.0.2.1", 1, 4, 1}, {"192.0.2.1", 2, 1, 0}, {"192.0.2.2", 1, 1, 1}}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v, want %d", rows, len(want))
	}
	for i, w := range want {
		row := rows[i]
		if row.SourceIP != w.ip || row.Generation != w.generation || row.Lookups != w.lookups || row.Misses != w.misses {
			t.Errorf("row %d = %+v, want %+v", i, row, w)
		}
		if !row.Day.Equal(nssDay(0)) || !row.FirstSeen.Equal(nssNow) || !row.LastSeen.Equal(nssNow) {
			t.Errorf("row %d day %v first %v last %v, want today and now", i, row.Day, row.FirstSeen, row.LastSeen)
		}
	}

	if err := f.usage.Flush(ctx); err != nil {
		t.Fatalf("an empty Flush: %v", err)
	}
	if again := usageRows(t, f.db); len(again) != 3 || again[0].Lookups != 4 {
		t.Errorf("an empty flush changed the rows: %+v", again)
	}
}

func TestNSSUsage_ShouldAddUpEveryInstancesCountsWhenTheyFlushTheSameRow(t *testing.T) {
	t.Parallel()

	c := nssTestConfig()
	f := newNSSUsageFixture(t, c)
	second := newTestNSSUsage(t, c, f.db, f.keys)
	ctx := t.Context()

	f.usage.now = func() time.Time { return nssNow.Add(-time.Hour) }
	f.usage.Observe(ctx, f.verified("192.0.2.1", 1, NSSResultHit))
	f.usage.Observe(ctx, f.verified("192.0.2.1", 1, NSSResultMiss))
	second.now = func() time.Time { return nssNow.Add(time.Hour) }
	second.Observe(ctx, f.verified("192.0.2.1", 1, NSSResultHit))

	if err := second.Flush(ctx); err != nil {
		t.Fatalf("second Flush: %v", err)
	}
	if err := f.usage.Flush(ctx); err != nil {
		t.Fatalf("first Flush: %v", err)
	}
	rows := usageRows(t, f.db)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	row := rows[0]
	if row.Lookups != 3 || row.Misses != 1 {
		t.Errorf("counts = %d lookups %d misses, want 3 and 1", row.Lookups, row.Misses)
	}
	if !row.FirstSeen.Equal(nssNow.Add(-time.Hour)) || !row.LastSeen.Equal(nssNow.Add(time.Hour)) {
		t.Errorf("first %v last %v, want the earlier first and the later last", row.FirstSeen, row.LastSeen)
	}
}

func TestNSSUsage_ShouldKeepTheCountsWhenAFlushFails(t *testing.T) {
	t.Parallel()

	f := newNSSUsageFixture(t, nssTestConfig())
	f.usage.Observe(t.Context(), f.verified("192.0.2.1", 1, NSSResultHit))
	sqlDB, err := f.db.DB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	_ = sqlDB.Close()

	if err := f.usage.Flush(t.Context()); err == nil {
		t.Fatal("Flush on a closed database succeeded")
	}
	f.usage.Observe(t.Context(), f.verified("192.0.2.1", 1, NSSResultMiss))
	f.usage.mu.Lock()
	defer f.usage.mu.Unlock()
	if len(f.usage.counts) != 1 {
		t.Fatalf("counts = %d rows, want the failed row kept", len(f.usage.counts))
	}
	for _, count := range f.usage.counts {
		if count.lookups != 2 || count.misses != 1 {
			t.Errorf("count = %+v, want the failed flush merged with the new lookup", count)
		}
	}
}

func TestNSSUsage_ShouldMergeRestoredCountsWhenTheRowHasNewOnes(t *testing.T) {
	t.Parallel()

	usage := &NSSUsageService{counts: map[nssUsageKey]*nssUsageCount{}}
	key := nssUsageKey{keyID: "nss_a", sourceIP: "192.0.2.1", generation: 1, day: nssDay(0)}
	usage.counts[key] = &nssUsageCount{lookups: 1, firstSeen: nssNow, lastSeen: nssNow}
	usage.restore(map[nssUsageKey]*nssUsageCount{
		key:              {lookups: 2, misses: 1, firstSeen: nssNow.Add(-time.Minute), lastSeen: nssNow.Add(-time.Second)},
		{keyID: "nss_b"}: {lookups: 5},
	})

	got := usage.counts[key]
	if got.lookups != 3 || got.misses != 1 || !got.firstSeen.Equal(nssNow.Add(-time.Minute)) || !got.lastSeen.Equal(nssNow) {
		t.Errorf("merged = %+v, want summed counts, the earlier first and the later last", got)
	}
	if len(usage.counts) != 2 {
		t.Errorf("rows = %d, want the restored-only row added", len(usage.counts))
	}
}

func TestNSSUsage_ShouldFlushAndRaiseWhatIsQueuedWhenShutDown(t *testing.T) {
	t.Parallel()

	f := newNSSUsageFixture(t, nssTestConfig())
	outcome := f.verified("192.0.2.1", 1, NSSResultHit)
	outcome.Name = "root"
	f.usage.Observe(t.Context(), outcome)

	if err := f.usage.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if rows := usageRows(t, f.db); len(rows) != 1 {
		t.Errorf("rows = %+v, want the pending count flushed", rows)
	}
	if details := auditDetails(t, f.db, AuditNSSPrivilegedNameLookup); len(details) != 1 {
		t.Errorf("nss.privileged_name_lookup rows = %d, want the queued alert raised", len(details))
	}
}

func TestNSSUsage_ShouldRaiseQueuedAlertsAndStopWhenRunUntilCancelled(t *testing.T) {
	t.Parallel()

	f := newNSSUsageFixture(t, nssTestConfig())
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- f.usage.Run(ctx) }()

	f.usage.Observe(t.Context(), NSSLookupOutcome{Kind: NSSKindUser, By: NSSByName, Result: NSSResultForbidden, KeyID: "nss_whatever", SourceIP: "198.51.100.7"})
	deadline := time.Now().Add(5 * time.Second)
	for len(auditDetails(t, f.db, AuditNSSLookupForbidden)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the running loop never raised the queued alert")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil on cancellation", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

func TestNSSUsage_ShouldPruneOnlyRowsPastTheRetentionWhenPruned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		retention time.Duration
		wantDays  []time.Time
	}{
		{name: "should delete the days before the retention when it is set", retention: 48 * time.Hour, wantDays: []time.Time{nssDay(-2), nssDay(0)}},
		{name: "should keep every row when the retention is zero", retention: 0, wantDays: []time.Time{nssDay(-5), nssDay(-3), nssDay(-2), nssDay(0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := nssTestConfig()
			c.NSS.UsageRetention = tt.retention
			f := newNSSUsageFixture(t, c)
			for _, day := range []int{-5, -3, -2, 0} {
				seedNSSUsage(t, f.db, f.key.Key.ID, "192.0.2.1", 1, nssDay(day).Add(time.Hour), nssDay(day).Add(time.Hour), 1)
			}

			if err := f.usage.PruneUsage(t.Context()); err != nil {
				t.Fatalf("PruneUsage: %v", err)
			}
			var days []time.Time
			for _, row := range usageRows(t, f.db) {
				days = append(days, row.Day.UTC())
			}
			sortTimes(days)
			if len(days) != len(tt.wantDays) {
				t.Fatalf("days left = %v, want %v", days, tt.wantDays)
			}
			for i := range days {
				if !days[i].Equal(tt.wantDays[i]) {
					t.Errorf("days left = %v, want %v", days, tt.wantDays)
				}
			}
		})
	}
}

// sortTimes sorts ts in place, oldest first.
func sortTimes(ts []time.Time) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j].Before(ts[j-1]); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}

func TestNSSUsage_ShouldRaiseTheKeySourceAlertOnceADayWhenAKeyIsSeenFromTooManyAddresses(t *testing.T) {
	t.Parallel()

	c := nssTestConfig()
	c.NSS.KeySourceAlert = 2
	f := newNSSUsageFixture(t, c)
	ctx := t.Context()
	for _, ip := range []string{"192.0.2.1", "192.0.2.2"} {
		f.usage.Observe(ctx, f.verified(ip, 1, NSSResultHit))
	}
	if err := f.usage.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if details := auditDetails(t, f.db, AuditNSSKeySourceAlert); len(details) != 0 {
		t.Fatalf("alert raised at the limit: %v", details)
	}

	// A second instance sees the third address: the count is the
	// database's, across both.
	second := newTestNSSUsage(t, c, f.db, f.keys)
	second.Observe(ctx, f.verified("192.0.2.3", 1, NSSResultHit))
	if err := second.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	f.usage.Observe(ctx, f.verified("192.0.2.4", 1, NSSResultHit))
	if err := f.usage.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	details := auditDetails(t, f.db, AuditNSSKeySourceAlert)
	if len(details) != 1 || details[0]["key_id"] != f.key.Key.ID || details[0]["sources"] != float64(3) {
		t.Errorf("nss.key_source_alert rows = %v, want one at 3 sources", details)
	}
}

func TestNSSUsage_ShouldRaiseTheMissRateAlertWhenAKeyMissesFasterThanTheRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rate      float64
		misses    int
		wantAlert bool
	}{
		// 0.05 per second over a minute is 3 misses.
		{name: "should raise the alert when the misses pass the rate", rate: 0.05, misses: 4, wantAlert: true},
		{name: "should not raise the alert when the misses are at the rate", rate: 0.05, misses: 3},
		{name: "should never raise the alert when the rate is zero", rate: 0, misses: 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := nssTestConfig()
			c.NSS.MissAlertRate = tt.rate
			f := newNSSUsageFixture(t, c)
			for range tt.misses {
				f.usage.Observe(t.Context(), f.verified("192.0.2.1", 1, NSSResultMiss))
			}
			if err := f.usage.Shutdown(t.Context()); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}
			details := auditDetails(t, f.db, AuditNSSMissRateAlert)
			if got := len(details) == 1; got != tt.wantAlert || len(details) > 1 {
				t.Errorf("nss.miss_rate_alert rows = %v, want alert %v", details, tt.wantAlert)
			}
		})
	}
}

func TestNSSUsage_ShouldStartANewMissWindowWhenAMinuteHasPassed(t *testing.T) {
	t.Parallel()

	c := nssTestConfig()
	c.NSS.MissAlertRate = 0.05
	f := newNSSUsageFixture(t, c)
	now := nssNow
	f.usage.now = func() time.Time { return now }
	for range 3 {
		f.usage.Observe(t.Context(), f.verified("192.0.2.1", 1, NSSResultMiss))
	}
	now = now.Add(nssMissWindow)
	for range 3 {
		f.usage.Observe(t.Context(), f.verified("192.0.2.1", 1, NSSResultMiss))
	}
	if len(f.usage.events) != 0 {
		t.Errorf("queued %d alerts, want none: neither window passed the rate", len(f.usage.events))
	}
}

func TestNSSUsage_ShouldRaiseThePrivilegedNameAlertOncePerKeyAndNameWhenOneIsLookedUp(t *testing.T) {
	t.Parallel()

	f := newNSSUsageFixture(t, nssTestConfig())
	for _, name := range []string{"root", "root", "alice", "daemon"} {
		outcome := f.verified("192.0.2.1", 1, NSSResultMiss)
		outcome.Name = name
		f.usage.Observe(t.Context(), outcome)
	}
	// The second instance's debounce is its own; the claim holds the day.
	second := newTestNSSUsage(t, nssTestConfig(), f.db, f.keys)
	outcome := f.verified("192.0.2.9", 1, NSSResultHit)
	outcome.Name = "root"
	second.Observe(t.Context(), outcome)
	for _, usage := range []*NSSUsageService{f.usage, second} {
		if err := usage.Shutdown(t.Context()); err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	}

	details := auditDetails(t, f.db, AuditNSSPrivilegedNameLookup)
	names := map[any]int{}
	for _, detail := range details {
		names[detail["name"]]++
	}
	if len(details) != 2 || names["root"] != 1 || names["daemon"] != 1 {
		t.Errorf("nss.privileged_name_lookup rows = %v, want root and daemon once each", details)
	}
}

func TestNSSUsage_ShouldAuditARefusedAddressHourlyWhenALookupIsForbidden(t *testing.T) {
	t.Parallel()

	f := newNSSUsageFixture(t, nssTestConfig())
	forbidden := NSSLookupOutcome{Kind: NSSKindGroup, By: NSSByID, Result: NSSResultForbidden, KeyID: f.key.Key.ID, SourceIP: "198.51.100.7"}
	f.usage.Observe(t.Context(), forbidden)
	f.usage.now = func() time.Time { return nssNow.Add(2 * time.Minute) }
	f.usage.Observe(t.Context(), forbidden)
	if err := f.usage.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	var rows []model.AuditEvent
	f.db.Where("action = ?", string(AuditNSSLookupForbidden)).Find(&rows)
	if len(rows) != 1 || rows[0].SourceIP != "198.51.100.7" {
		t.Fatalf("nss.lookup_forbidden rows = %+v, want one carrying the address", rows)
	}
	if details := auditDetails(t, f.db, AuditNSSLookupForbidden); details[0]["key_id"] != f.key.Key.ID {
		t.Errorf("detail = %v, want the presented key id", details[0])
	}
}

func TestNSSUsage_ShouldAuditAndMailADeadKeyAttemptWhenARevokedOrExpiredKeyIsPresented(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dead       string
		mail       bool
		wantAction AuditAction
		wantKind   notify.Kind
	}{
		{name: "should report a revoked key when mail is on", dead: NSSDeadKeyRevoked, mail: true, wantAction: AuditNSSKeyRevokedAttempt, wantKind: notify.KindNSSKeyRevokedAttempt},
		{name: "should report an expired key when mail is on", dead: NSSDeadKeyExpired, mail: true, wantAction: AuditNSSKeyExpiredAttempt, wantKind: notify.KindNSSKeyExpiredAttempt},
		{name: "should audit but not mail when mail is off", dead: NSSDeadKeyExpired, wantAction: AuditNSSKeyExpiredAttempt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := nssTestConfig()
			c.Mail.Enabled = tt.mail
			f := newNSSUsageFixture(t, c)
			key := f.key.Key
			key.RevokedAt = nssAt(-time.Hour)
			attempt := NSSLookupOutcome{
				Kind: NSSKindUser, By: NSSByName, Result: NSSResultUnauthorized, KeyID: key.ID, SourceIP: "192.0.2.50",
				Verdict: NSSKeyVerdict{KeyID: key.ID, Dead: tt.dead, Key: &key},
			}
			f.usage.Observe(t.Context(), attempt)
			if err := f.usage.Shutdown(t.Context()); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}

			if details := auditDetails(t, f.db, tt.wantAction); len(details) != 1 || details[0]["key_id"] != key.ID {
				t.Errorf("%s rows = %v, want one naming the key", tt.wantAction, details)
			}
			var mailed []capturedNotification
			for _, event := range f.notifier.captured() {
				if event.NSSKeyID == key.ID && event.Kind != notify.KindNSSKeyRotated {
					mailed = append(mailed, event)
				}
			}
			if tt.wantKind == "" {
				if len(mailed) != 0 {
					t.Errorf("mailed %+v with mail off", mailed)
				}
				return
			}
			if len(mailed) != 1 || mailed[0].Kind != tt.wantKind {
				t.Errorf("mailed %+v, want one %s", mailed, tt.wantKind)
			}
			if rows := usageRows(t, f.db); len(rows) != 0 {
				t.Errorf("usage rows = %+v, want none for a refused key", rows)
			}
		})
	}
}

func TestNSSUsage_ShouldQueueAnAlertOncePerDebounceWhenTheSameEventRepeats(t *testing.T) {
	t.Parallel()

	usage := newTestNSSUsage(t, nssTestConfig(), testdb.New(t), nil)
	now := nssNow
	usage.now = func() time.Time { return now }
	forbidden := NSSLookupOutcome{Result: NSSResultForbidden, SourceIP: "198.51.100.7", KeyID: "nss_abcdefgh"}
	usage.Observe(t.Context(), forbidden)
	usage.Observe(t.Context(), forbidden)
	if got := len(usage.events); got != 1 {
		t.Fatalf("queued %d, want the repeat debounced", got)
	}
	now = now.Add(nssAlertDebounce)
	usage.Observe(t.Context(), forbidden)
	if got := len(usage.events); got != 2 {
		t.Errorf("queued %d after the debounce, want 2", got)
	}

	usage.mu.Lock()
	usage.pruneLocked(now.Add(nssAlertDebounce))
	left := len(usage.queued)
	usage.mu.Unlock()
	if left != 0 {
		t.Errorf("debounce entries after pruning = %d, want 0", left)
	}
}

func TestNSSUsage_ShouldDropAnAlertWhenTheQueueIsFull(t *testing.T) {
	t.Parallel()

	usage := newTestNSSUsage(t, nssTestConfig(), testdb.New(t), nil)
	for i := range nssEventQueue + 3 {
		usage.Observe(t.Context(), NSSLookupOutcome{Result: NSSResultForbidden, SourceIP: "198.51.100.7", KeyID: string(rune('a'+i%26)) + time.Duration(i).String()})
	}
	usage.mu.Lock()
	dropped := usage.dropped
	usage.mu.Unlock()
	if len(usage.events) != nssEventQueue || dropped != 3 {
		t.Errorf("queued %d dropped %d, want %d and 3", len(usage.events), dropped, nssEventQueue)
	}
}

func TestNSSUsage_ShouldCapThePresentedKeyIDWhenItIsStored(t *testing.T) {
	t.Parallel()

	long := "nss_" + string(make([]byte, 100))
	if got := boundedKeyID(long); len(got) != maxNSSPresentedKeyID {
		t.Errorf("bounded length = %d, want %d", len(got), maxNSSPresentedKeyID)
	}
	if got := boundedKeyID("nss_abcdefgh"); got != "nss_abcdefgh" {
		t.Errorf("bounded = %q, want a short id unchanged", got)
	}
}

func TestNSSMetrics_ShouldCountLookupsByKindAndResultWhenObserved(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	db := testdb.New(t)
	seedNSSCallers(t, db)
	c := nssTestConfig()
	keys, err := NewNSSKeyService(c, db, provider.Meter("test"))
	if err != nil {
		t.Fatalf("NewNSSKeyService: %v", err)
	}
	keys.now = nssClock()
	usage, err := NewNSSUsageService(c, db, keys, provider.Meter("test"))
	if err != nil {
		t.Fatalf("NewNSSUsageService: %v", err)
	}
	usage.now = nssClock()

	created, _ := createNSSKey(t, keys, "live", "")
	revoked, _ := createNSSKey(t, keys, "dead", "")
	if _, err := keys.Revoke(t.Context(), nssAdmin, revoked.Key.ID, ""); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	expired, _ := createNSSKey(t, keys, "old", "")
	db.Model(&model.NSSKey{}).Where("id = ?", expired.Key.ID).Update("expires_at", nssNow)

	ctx := t.Context()
	hit := NSSLookupOutcome{Kind: NSSKindUser, By: NSSByName, Result: NSSResultHit, Duration: 3 * time.Millisecond,
		Verdict: NSSKeyVerdict{KeyID: created.Key.ID, OK: true, Generation: 1}}
	usage.Observe(ctx, hit)
	usage.Observe(ctx, hit)
	usage.Observe(ctx, NSSLookupOutcome{Kind: NSSKindGroup, By: NSSByID, Result: NSSResultUnauthorized, Duration: time.Millisecond})

	var data metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &data); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	counts := map[string]int64{}
	var histogramCount uint64
	gauge := map[string]int64{}
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch m.Name {
			case "ssoossh_nss_lookups":
				sum, _ := m.Data.(metricdata.Sum[int64])
				for _, point := range sum.DataPoints {
					counts[attrString(point.Attributes, "kind")+"/"+attrString(point.Attributes, "by")+"/"+attrString(point.Attributes, "result")] = point.Value
				}
			case "ssoossh_nss_lookup_duration":
				histogram, _ := m.Data.(metricdata.Histogram[float64])
				for _, point := range histogram.DataPoints {
					histogramCount += point.Count
				}
			case "ssoossh_nss_keys":
				g, _ := m.Data.(metricdata.Gauge[int64])
				for _, point := range g.DataPoints {
					gauge[attrString(point.Attributes, "status")] = point.Value
				}
			}
		}
	}
	if counts["user/name/hit"] != 2 || counts["group/id/unauthorized"] != 1 || len(counts) != 2 {
		t.Errorf("lookup counts = %v, want 2 user hits by name and 1 group refusal by id", counts)
	}
	if histogramCount != 3 {
		t.Errorf("durations recorded = %d, want 3", histogramCount)
	}
	if gauge[NSSKeyStatusActive] != 1 || gauge[NSSKeyStatusRevoked] != 1 || gauge[NSSKeyStatusExpired] != 1 {
		t.Errorf("keys gauge = %v, want one key in each status", gauge)
	}
}

// attrString reads one string attribute from a set.
func attrString(set attribute.Set, key string) string {
	value, _ := set.Value(attribute.Key(key))
	return value.AsString()
}

func TestNSSMetrics_ShouldFailTheGaugeCollectionWhenTheDatabaseCannotBeRead(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	keys, err := NewNSSKeyService(nssTestConfig(), db, nil)
	if err != nil {
		t.Fatalf("NewNSSKeyService: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	_ = sqlDB.Close()
	if _, err := keys.countByStatus(t.Context()); err == nil || errors.Is(err, context.Canceled) {
		t.Errorf("countByStatus on a closed database = %v, want the read error", err)
	}
}
