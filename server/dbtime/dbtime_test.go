package dbtime_test

// Test methodology: these run against a real in-memory SQLite *gorm.DB
// rather than asserting on the plugin's reflection in isolation, because
// the bug being guarded against is a property of what SQLite ends up
// storing and comparing, not of the Go values. A test that only checked
// `t.Location() == time.UTC` would pass on code that still wrote local
// offsets through a path the plugin missed.

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/dbtime"
)

// probe stands in for a model: one of each shape of time field the real
// models use — a value, a pointer, and a GORM-managed CreatedAt.
type probe struct {
	ID         string `gorm:"primaryKey"`
	OccurredAt time.Time
	ResolvedAt *time.Time
	CreatedAt  time.Time
}

// newDB opens an in-memory database configured exactly as
// bootstrap.openWithRetry configures the real one.
func newDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{NowFunc: dbtime.NowFunc})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	if err := db.Use(dbtime.Plugin{}); err != nil {
		t.Fatalf("failed to register plugin: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get underlying sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&probe{}); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	return db
}

// storedValue reads the raw text SQLite holds for a column, which is what
// its comparisons actually operate on.
func storedValue(t *testing.T, db *gorm.DB, id, column string) string {
	t.Helper()

	var got string
	if err := db.Raw(`SELECT `+column+` FROM probes WHERE id = ?`, id).Scan(&got).Error; err != nil {
		t.Fatalf("failed to read %s: %v", column, err)
	}
	return got
}

var (
	east = time.FixedZone("EAST", 4*3600)
	west = time.FixedZone("WEST", -4*3600)
)

func TestPlugin_ShouldStoreAValueTimeFieldInUTCWhenCreatedFromAnotherZone(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	at := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC).In(west)
	if err := db.Create(&probe{ID: "a", OccurredAt: at}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	if got, want := storedValue(t, db, "a", "occurred_at"), "2026-08-22T12:00:00Z"; got != want {
		t.Errorf("stored occurred_at = %q, want %q", got, want)
	}
}

func TestPlugin_ShouldStoreAPointerTimeFieldInUTCWhenCreatedFromAnotherZone(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	at := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC).In(east)
	if err := db.Create(&probe{ID: "a", ResolvedAt: &at}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	if got, want := storedValue(t, db, "a", "resolved_at"), "2026-08-22T12:00:00Z"; got != want {
		t.Errorf("stored resolved_at = %q, want %q", got, want)
	}
}

func TestNowFunc_ShouldStoreAGormManagedTimestampInUTC(t *testing.T) {
	t.Parallel()

	// CreatedAt is left zero so GORM stamps it itself, from inside its own
	// create callback — after the plugin has already run. NowFunc is what
	// covers that path.
	db := newDB(t)
	if err := db.Create(&probe{ID: "a"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	if got := storedValue(t, db, "a", "created_at"); !hasUTCSuffix(got) {
		t.Errorf("stored created_at = %q, want a UTC (Z-suffixed) value", got)
	}
}

func TestPlugin_ShouldStoreAnUpdatesMapValueInUTC(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	if err := db.Create(&probe{ID: "a"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	at := time.Date(2026, 8, 22, 15, 0, 0, 0, time.UTC).In(west)
	if err := db.Model(&probe{}).Where("id = ?", "a").
		Updates(map[string]any{"occurred_at": at}).Error; err != nil {
		t.Fatalf("update: %v", err)
	}

	if got, want := storedValue(t, db, "a", "occurred_at"), "2026-08-22T15:00:00Z"; got != want {
		t.Errorf("stored occurred_at = %q, want %q", got, want)
	}
}

func TestPlugin_ShouldStoreEveryRowOfABatchCreateInUTC(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	rows := []probe{
		{ID: "a", OccurredAt: time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC).In(west)},
		{ID: "b", OccurredAt: time.Date(2026, 8, 22, 13, 0, 0, 0, time.UTC).In(east)},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	for id, want := range map[string]string{
		"a": "2026-08-22T12:00:00Z",
		"b": "2026-08-22T13:00:00Z",
	} {
		if got := storedValue(t, db, id, "occurred_at"); got != want {
			t.Errorf("stored occurred_at for %q = %q, want %q", id, got, want)
		}
	}
}

// TestPlugin_ShouldOrderChronologicallyAcrossWriterZones is the regression
// test for the ordering half of the bug: before normalization, these two
// rows came back in reverse chronological order because SQLite compared
// "2026-08-22T16:00:00+04:00" against "2026-08-22T09:00:00-04:00" as text.
func TestPlugin_ShouldOrderChronologicallyAcrossWriterZones(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	earlier := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC).In(east) // 16:00+04:00
	later := time.Date(2026, 8, 22, 13, 0, 0, 0, time.UTC).In(west)   // 09:00-04:00
	if err := db.Create(&[]probe{{ID: "earlier", OccurredAt: earlier}, {ID: "later", OccurredAt: later}}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	var ids []string
	if err := db.Model(&probe{}).Order("occurred_at ASC").Pluck("id", &ids).Error; err != nil {
		t.Fatalf("order: %v", err)
	}

	if want := []string{"earlier", "later"}; !equal(ids, want) {
		t.Errorf("ORDER BY occurred_at ASC = %v, want %v", ids, want)
	}
}

// TestPlugin_ShouldApplyARangePredicateByInstantNotByText is the regression
// test for the comparison half: before normalization, a cutoff expressed in
// a different zone from the stored rows matched rows created after it.
func TestPlugin_ShouldApplyARangePredicateByInstantNotByText(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	cutoff := base.Add(-90 * time.Minute)

	// The same instant, spelled three ways. All three must select the same
	// single row — that is exactly what failed before.
	for _, tc := range []struct {
		name   string
		cutoff time.Time
	}{
		{"cutoff in UTC", cutoff.UTC()},
		{"cutoff east of UTC", cutoff.In(east)},
		{"cutoff west of UTC", cutoff.In(west)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			db := newDB(t)
			if err := db.Create(&[]probe{
				{ID: "t-2h", OccurredAt: base.Add(-2 * time.Hour).In(west)},
				{ID: "t-1h", OccurredAt: base.Add(-1 * time.Hour).In(east)},
				{ID: "t-0h", OccurredAt: base},
			}).Error; err != nil {
				t.Fatalf("create: %v", err)
			}

			var ids []string
			if err := db.Model(&probe{}).
				Where("occurred_at < ?", tc.cutoff.UTC()).
				Order("occurred_at ASC").Pluck("id", &ids).Error; err != nil {
				t.Fatalf("query: %v", err)
			}

			if want := []string{"t-2h"}; !equal(ids, want) {
				t.Errorf("rows before cutoff = %v, want %v", ids, want)
			}
		})
	}
}

func TestPlugin_ShouldLeaveAZeroTimeAlone(t *testing.T) {
	t.Parallel()

	// A zero time is "unset", not an instant to be re-expressed; converting
	// it would turn a sentinel into a real timestamp in a non-UTC process.
	db := newDB(t)
	if err := db.Create(&probe{ID: "a"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	var got probe
	if err := db.First(&got, "id = ?", "a").Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !got.OccurredAt.IsZero() {
		t.Errorf("OccurredAt = %v, want the zero time", got.OccurredAt)
	}
}

func TestPlugin_ShouldLeaveANilPointerTimeAlone(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	if err := db.Create(&probe{ID: "a"}).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	var got probe
	if err := db.First(&got, "id = ?", "a").Error; err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.ResolvedAt != nil {
		t.Errorf("ResolvedAt = %v, want nil", got.ResolvedAt)
	}
}

func TestPlugin_ShouldRejectRegisteringTwiceUnderTheSameName(t *testing.T) {
	t.Parallel()

	db := newDB(t)
	if err := db.Use(dbtime.Plugin{}); err == nil {
		t.Error("second Use() error = nil, want a duplicate-plugin error")
	}
}

// hasUTCSuffix reports whether a stored timestamp is expressed in UTC.
// SQLite holds these as RFC3339 text, where UTC renders as a "Z" suffix.
func hasUTCSuffix(s string) bool {
	return len(s) > 0 && s[len(s)-1] == 'Z'
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
