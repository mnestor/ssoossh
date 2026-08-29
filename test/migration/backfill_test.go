package migration_test

// The schema goldens next door pin what the migrations build. This pins what
// one of them does to the rows that were already there: enrollments.
// service_account is derived from the principals JSON, and a wrong or
// failed derivation would silently hand every pre-existing enrollment to
// nobody (see docs/proposals/enrollment-group-ownership.md, where the column
// is the whole of ownership).

import (
	"testing"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/test/sqlite"
)

// The migration under test, and the one immediately before it.
const (
	groupOwnershipVersion = 20260829030000
	previousVersion       = 20260829020000
)

// enrollmentRow is the subset of the table this test writes and reads. Not
// model.Enrollment: that carries the column the migration is what adds, so
// GORM would try to write it before it exists.
type enrollmentRow struct {
	ID             string
	Principals     string
	ServiceAccount string
}

// backfilled steps back to before the group-ownership migration, writes
// enrollments whose principals column holds raw, applies the migration, and
// returns the service_account it derived.
func backfilled(t *testing.T, principals string) string {
	t.Helper()

	db := sqlite.ConnectAndMigrate(t)
	if err := sqlite.RunTo(t, db, previousVersion); err != nil {
		t.Fatalf("failed to step back to %d: %v", previousVersion, err)
	}

	// A user row to satisfy the foreign key, written through the same raw
	// SQL for the same reason as the enrollment.
	if err := db.Exec(`INSERT INTO users (id, subject, username, email, created_at, updated_at)
		VALUES ('u-1', 'sub-1', 'alice', 'alice@example.com', datetime('now'), datetime('now'))`).
		Error; err != nil {
		t.Fatalf("failed to seed the owning user: %v", err)
	}
	if err := db.Exec(`INSERT INTO enrollments
		(id, code, public_key, option_set, key_id, principals, user_id, created_at, expires_at)
		VALUES ('e-1', 'code-1', 'k', '{}', 'kid', ?, 'u-1', datetime('now'), datetime('now', '+1 day'))`,
		principals).Error; err != nil {
		t.Fatalf("failed to seed the enrollment: %v", err)
	}

	if err := sqlite.RunTo(t, db, groupOwnershipVersion); err != nil {
		t.Fatalf("failed to apply %d: %v", groupOwnershipVersion, err)
	}

	var row enrollmentRow
	if err := db.Raw(`SELECT id, principals, service_account FROM enrollments WHERE id = 'e-1'`).
		Scan(&row).Error; err != nil {
		t.Fatalf("failed to read back the enrollment: %v", err)
	}
	return row.ServiceAccount
}

func TestGroupOwnershipMigration_ShouldBackfillTheServiceAccount(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		principals string
		want       string
	}{
		{
			name:       "the ordinary row, one principal fixed at approval",
			principals: `["svc-backup"]`,
			want:       "svc-backup",
		},
		{
			// Nothing writes more than one, but reading the first is the
			// documented rule rather than an accident of the data.
			name:       "more principals than a service enrollment should have",
			principals: `["svc-backup","svc-extra"]`,
			want:       "svc-backup",
		},
		{
			// Rows written before principals were fixed at approval time.
			// These have to survive the migration owned by nobody, not fail
			// it: json_extract raises on malformed input, which is why the
			// backfill is guarded by json_valid.
			name:       "principals that never parsed",
			principals: "{{{",
			want:       "",
		},
		{
			name:       "principals stored empty",
			principals: "",
			want:       "",
		},
		{
			name:       "a well-formed but empty array",
			principals: `[]`,
			want:       "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := backfilled(t, tc.principals); got != tc.want {
				t.Errorf("service_account = %q, want %q", got, tc.want)
			}
		})
	}
}

// The column is added and dropped cleanly, so a deployment can roll back to
// the previous release and forward again.
func TestGroupOwnershipMigration_ShouldRoundTrip(t *testing.T) {
	t.Parallel()

	db := sqlite.ConnectAndMigrate(t)
	if err := sqlite.RunTo(t, db, previousVersion); err != nil {
		t.Fatalf("failed to step back: %v", err)
	}
	if hasServiceAccountColumn(t, db) {
		t.Error("service_account survived the down migration")
	}
	if err := sqlite.RunUp(t, db); err != nil {
		t.Fatalf("failed to migrate up again: %v", err)
	}
	if !hasServiceAccountColumn(t, db) {
		t.Error("service_account is missing after migrating up again")
	}
}

func hasServiceAccountColumn(t *testing.T, db *gorm.DB) bool {
	t.Helper()

	var count int64
	if err := db.Raw(
		`SELECT COUNT(*) FROM pragma_table_info('enrollments') WHERE name = 'service_account'`).
		Scan(&count).Error; err != nil {
		t.Fatalf("failed to inspect the enrollments columns: %v", err)
	}
	return count > 0
}
