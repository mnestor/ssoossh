package migration_test

// Test methodology: step a database back to the version before
// 20261011000000_repair_disabled_source, write users rows the way the admin
// disable and re-enable used to leave them, migrate up, and read back each
// row's disabled_source. What matters: a stale ldap_sync is cleared wherever
// it could hand an operator's disable to the directory sync, and the sync's
// own disables and every other source are left as they were.
//
// The Postgres half is disabled_source_repair_pg_test.go.

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/test/sqlite"
)

// versionBeforeRepair is the migration the repair follows.
const versionBeforeRepair = 20260910000000

// repairCase is one users row before the repair and the source it should
// carry after.
type repairCase struct {
	name     string
	disabled bool
	// byOperator names an operator in disabled_by_user_id, which only the
	// admin disable writes.
	byOperator bool
	source     *string
	want       *string
}

// strPtr returns a pointer to s, for the nullable source column.
func strPtr(s string) *string { return &s }

// repairCases covers both rows the repair clears and every row it keeps.
var repairCases = []repairCase{
	{
		name:   "should clear a stale ldap_sync on an enabled account",
		source: strPtr("ldap_sync"),
	},
	{
		name:       "should clear ldap_sync on an operator's disable",
		disabled:   true,
		byOperator: true,
		source:     strPtr("ldap_sync"),
	},
	{
		name:     "should keep the directory sync's own disable",
		disabled: true,
		source:   strPtr("ldap_sync"),
		want:     strPtr("ldap_sync"),
	},
	{
		name:       "should keep an admin disable",
		disabled:   true,
		byOperator: true,
		source:     strPtr("admin"),
		want:       strPtr("admin"),
	},
	{
		name:       "should keep a SOC disable",
		disabled:   true,
		byOperator: true,
		source:     strPtr("soc"),
		want:       strPtr("soc"),
	},
	{
		name:       "should keep a disable with no source",
		disabled:   true,
		byOperator: true,
	},
}

// repairUserID is the users.id case i is seeded under.
func repairUserID(i int) string {
	return fmt.Sprintf("u-case-%d", i)
}

// seedRepairCases writes the operator and one users row per case. Bound
// parameters rather than SQL time functions, so both dialects share it.
func seedRepairCases(t *testing.T, db *gorm.DB) {
	t.Helper()

	now := time.Now().UTC()
	if err := db.Exec(`INSERT INTO users (id, subject, username, email, created_at, updated_at)
		VALUES ('u-op', 'sub-op', 'op', 'op@example.com', ?, ?)`, now, now).Error; err != nil {
		t.Fatalf("failed to seed the operator: %v", err)
	}

	for i, c := range repairCases {
		var disabledAt *time.Time
		if c.disabled {
			disabledAt = &now
		}
		var disabledBy *string
		if c.byOperator {
			disabledBy = strPtr("u-op")
		}
		if err := db.Exec(`INSERT INTO users
			(id, subject, username, email, created_at, updated_at, disabled_at, disabled_by_user_id, disabled_source)
			VALUES (?, ?, ?, '', ?, ?, ?, ?, ?)`,
			repairUserID(i), "sub-"+repairUserID(i), repairUserID(i), now, now,
			disabledAt, disabledBy, c.source).Error; err != nil {
			t.Fatalf("failed to seed %q: %v", c.name, err)
		}
	}
}

// assertRepaired checks each case's source after the repair ran.
func assertRepaired(t *testing.T, db *gorm.DB) {
	t.Helper()

	for i, c := range repairCases {
		t.Run(c.name, func(t *testing.T) {
			var got *string
			if err := db.Raw(`SELECT disabled_source FROM users WHERE id = ?`, repairUserID(i)).
				Row().Scan(&got); err != nil {
				t.Fatalf("failed to read the source back: %v", err)
			}
			if describe(got) != describe(c.want) {
				t.Errorf("disabled_source = %s, want %s", describe(got), describe(c.want))
			}
		})
	}
}

// describe renders a nullable source for comparison and messages.
func describe(s *string) string {
	if s == nil {
		return "NULL"
	}
	return fmt.Sprintf("%q", *s)
}

func TestRepairDisabledSource_ShouldClearOnlyStaleLDAPSyncSources(t *testing.T) {
	t.Parallel()

	db := sqlite.ConnectAndMigrate(t)
	if err := sqlite.RunTo(t, db, versionBeforeRepair); err != nil {
		t.Fatalf("failed to step back before the repair: %v", err)
	}
	seedRepairCases(t, db)
	if err := sqlite.RunUp(t, db); err != nil {
		t.Fatalf("failed to apply the repair: %v", err)
	}

	assertRepaired(t, db)
}
