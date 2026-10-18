package service

import (
	"reflect"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/paging"
	"github.com/mnestor/ssoossh/test/testdb"
)

// nssDay is midnight UTC of the day d away from nssNow's.
func nssDay(days int) time.Time {
	return time.Date(nssNow.Year(), nssNow.Month(), nssNow.Day()+days, 0, 0, 0, 0, time.UTC)
}

// seedNSSUsage writes one usage row for key.
func seedNSSUsage(t *testing.T, db *gorm.DB, keyID, ip string, generation int, firstSeen, lastSeen time.Time, lookups int64) {
	t.Helper()

	row := model.NSSKeyUsage{
		KeyID: keyID, SourceIP: ip, Generation: generation,
		Day:     time.Date(lastSeen.Year(), lastSeen.Month(), lastSeen.Day(), 0, 0, 0, 0, time.UTC),
		Lookups: lookups, FirstSeen: firstSeen, LastSeen: lastSeen,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed usage: %v", err)
	}
}

func TestNSSKeyReport_ShouldBucketEachAddressWhenTheKeyWasRotated(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
	rotatedAt := nssNow.Add(-36 * time.Hour)
	keys.now = func() time.Time { return rotatedAt }
	if _, _, err := keys.Rotate(t.Context(), nssKeyAdmin, created.Key.ID, ""); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	keys.now = nssLookupClock()
	id := created.Key.ID

	// A switched: the previous secret before the rotation, the current
	// one after. B not switched: the previous secret after the rotation.
	// C gone: not seen since. D switched: only ever the current secret.
	seedNSSUsage(t, db, id, "192.0.2.1", 1, rotatedAt.Add(-72*time.Hour), rotatedAt.Add(-time.Hour), 10)
	seedNSSUsage(t, db, id, "192.0.2.1", 2, rotatedAt.Add(time.Hour), rotatedAt.Add(2*time.Hour), 5)
	seedNSSUsage(t, db, id, "192.0.2.2", 1, rotatedAt.Add(-48*time.Hour), rotatedAt.Add(3*time.Hour), 7)
	seedNSSUsage(t, db, id, "192.0.2.3", 1, rotatedAt.Add(-50*time.Hour), rotatedAt.Add(-48*time.Hour), 3)
	seedNSSUsage(t, db, id, "2001:db8::4", 2, rotatedAt.Add(4*time.Hour), rotatedAt.Add(5*time.Hour), 1)

	report, err := keys.RotationReport(t.Context(), nssAuditor, id)
	if err != nil {
		t.Fatalf("RotationReport: %v", err)
	}
	if report.Switched != 2 || report.NotSwitched != 1 || report.Gone != 1 {
		t.Errorf("counts = %d switched %d not %d gone, want 2, 1, 1", report.Switched, report.NotSwitched, report.Gone)
	}
	// The grace is 720h from the rotation, 36h ago: 28.5 days left.
	if report.GraceDaysLeft != 29 || report.CurrentGeneration != 2 || report.RotatedAt == nil || !report.RotatedAt.Equal(rotatedAt) {
		t.Errorf("report = %+v, want 29 days left at generation 2", report)
	}
	want := []NSSRotationReportRow{
		{SourceIP: "192.0.2.2", FirstSeen: rotatedAt.Add(-48 * time.Hour), LastSeen: rotatedAt.Add(3 * time.Hour), Lookups: 7, LastGeneration: 1, Bucket: NSSRotationNotSwitched},
		{SourceIP: "2001:db8::4", FirstSeen: rotatedAt.Add(4 * time.Hour), LastSeen: rotatedAt.Add(5 * time.Hour), Lookups: 1, LastGeneration: 2, Bucket: NSSRotationSwitched},
		{SourceIP: "192.0.2.1", FirstSeen: rotatedAt.Add(-72 * time.Hour), LastSeen: rotatedAt.Add(2 * time.Hour), Lookups: 15, LastGeneration: 2, Bucket: NSSRotationSwitched},
		{SourceIP: "192.0.2.3", FirstSeen: rotatedAt.Add(-50 * time.Hour), LastSeen: rotatedAt.Add(-48 * time.Hour), Lookups: 3, LastGeneration: 1, Bucket: NSSRotationGone},
	}
	for i := range report.Rows {
		report.Rows[i].FirstSeen = report.Rows[i].FirstSeen.UTC()
		report.Rows[i].LastSeen = report.Rows[i].LastSeen.UTC()
	}
	if !reflect.DeepEqual(report.Rows, want) {
		t.Errorf("rows =\n%+v\nwant\n%+v", report.Rows, want)
	}
}

func TestNSSKeyReport_ShouldCountEveryAddressSwitchedWhenTheKeyWasNeverRotated(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
	seedNSSUsage(t, db, created.Key.ID, "192.0.2.1", 1, nssNow.Add(-time.Hour), nssNow, 1)

	report, err := keys.RotationReport(t.Context(), nssMember, created.Key.ID)
	if err != nil {
		t.Fatalf("RotationReport: %v", err)
	}
	if report.Switched != 1 || report.NotSwitched != 0 || report.Gone != 0 || report.GraceDaysLeft != 0 || report.RotatedAt != nil {
		t.Errorf("report = %+v, want one switched address and no grace", report)
	}
}

func TestNSSKeyReport_ShouldHideTheReportWhenTheCallerMayNotViewTheKey(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	created, _ := createNSSKey(t, keys, "web fleet", "web-admins")

	if _, err := keys.RotationReport(t.Context(), nssPlain, created.Key.ID); !isNotFound(err) {
		t.Errorf("RotationReport = %v, want 404", err)
	}
	if _, err := keys.Usage(t.Context(), nssPlain, created.Key.ID, paging.Params{Limit: 10}); !isNotFound(err) {
		t.Errorf("Usage = %v, want 404", err)
	}
}

func TestNSSKeyReport_ShouldPageThroughEveryUsageRowOnceWhenFollowingTheCursor(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	created, _ := createNSSKey(t, keys, "web fleet", "web-admins")
	other, _ := createNSSKey(t, keys, "db fleet", "db-admins")
	id := created.Key.ID
	seedNSSUsage(t, db, id, "192.0.2.2", 1, nssDay(0).Add(time.Hour), nssDay(0).Add(2*time.Hour), 1)
	seedNSSUsage(t, db, id, "192.0.2.1", 2, nssDay(0).Add(time.Hour), nssDay(0).Add(2*time.Hour), 2)
	seedNSSUsage(t, db, id, "192.0.2.1", 1, nssDay(0).Add(time.Hour), nssDay(0).Add(2*time.Hour), 3)
	seedNSSUsage(t, db, id, "2001:db8::1", 1, nssDay(-1).Add(time.Hour), nssDay(-1).Add(2*time.Hour), 4)
	seedNSSUsage(t, db, id, "192.0.2.9", 1, nssDay(-2).Add(time.Hour), nssDay(-2).Add(2*time.Hour), 5)
	seedNSSUsage(t, db, other.Key.ID, "192.0.2.1", 1, nssDay(0).Add(time.Hour), nssDay(0).Add(2*time.Hour), 99)

	var seen []int64
	after := ""
	for page := 0; page < 10; page++ {
		result, err := keys.Usage(t.Context(), nssKeyAdmin, id, paging.Params{Limit: 2, After: after})
		if err != nil {
			t.Fatalf("Usage page %d: %v", page, err)
		}
		if result.Total != 5 {
			t.Errorf("total = %d, want 5", result.Total)
		}
		for _, row := range result.Rows {
			seen = append(seen, row.Lookups)
		}
		if result.NextCursor == nil {
			break
		}
		after = *result.NextCursor
	}
	// Day newest first, then address, then generation newest first.
	if want := []int64{2, 3, 1, 4, 5}; !reflect.DeepEqual(seen, want) {
		t.Errorf("rows in order = %v, want %v", seen, want)
	}
}

func TestNSSKeyReport_ShouldRefuseTheCursorWhenItIsNotOneTheServerWrote(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	seedNSSCallers(t, db)
	keys, _ := newTestNSSKeys(t, nssLookupTestConfig(), db)
	created, _ := createNSSKey(t, keys, "web fleet", "web-admins")

	for _, cursor := range []string{"garbage", "1.2", "x.1.192.0.2.1", "1.x.192.0.2.1", "1.1."} {
		if _, err := keys.Usage(t.Context(), nssAdmin, created.Key.ID, paging.Params{Limit: 2, After: cursor}); !isInvalid(err) {
			t.Errorf("Usage with cursor %q = %v, want 400", cursor, err)
		}
	}
}

func TestNSSKeyReport_ShouldRoundTripTheCursorWhenTheAddressIsIPv6(t *testing.T) {
	t.Parallel()

	row := model.NSSKeyUsage{Day: nssDay(0), Generation: 3, SourceIP: "2001:db8::1"}
	day, ip, generation, ok := parseNSSUsageCursor(nssUsageCursor(row))
	if !ok || !day.Equal(row.Day) || ip != row.SourceIP || generation != 3 {
		t.Errorf("round trip = %v %q %d %v, want the row back", day, ip, generation, ok)
	}
}
