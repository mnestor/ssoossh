package migration_test

// Test methodology: step a database back to the version before
// 20261012120000_audit_event_source_ip, write audit_events rows the way the
// recorder used to (no source_ip column), migrate up, and read back each
// row's source_ip column. What matters: a readable payload's string
// detail.source_ip is copied, no other address is (approver_ip, a top-level
// source_ip, the reported remote_host), and a payload that cannot be read
// leaves '' rather than failing the migration.
//
// The Postgres half is audit_source_ip_backfill_pg_test.go.

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/test/sqlite"
)

// versionBeforeAuditSourceIP is the migration the source_ip column follows.
const versionBeforeAuditSourceIP = 20261012000000

// sourceIPBackfillCases covers the payloads the recorder writes and the ones
// a damaged or foreign row could hold.
var sourceIPBackfillCases = []backfillCase{
	{
		name:    "should copy the source of a refused request",
		payload: `{"action":"cert.request_refused_network","system":true,"detail":{"cert_type":"console","source_ip":"192.0.2.7"},"v":1}`,
		want:    "192.0.2.7",
	},
	{
		name:    "should copy the requester's source and not the approver's",
		payload: `{"action":"cert.approved","detail":{"approver_ip":"198.51.100.9","source_ip":"192.0.2.8"},"v":1}`,
		want:    "192.0.2.8",
	},
	{
		name:    "should copy an IPv6 source",
		payload: `{"action":"enrollment.redeemed","detail":{"source_ip":"2001:db8::1"},"v":1}`,
		want:    "2001:db8::1",
	},
	{
		name:    "should copy the source when a detail string holds a NUL escape",
		payload: `{"action":"cert.denied","detail":{"note":"a\u0000b","source_ip":"192.0.2.9"},"v":1}`,
		want:    "192.0.2.9",
	},
	{
		name:    "should leave empty an event with only an approver address",
		payload: `{"action":"cert.denied","detail":{"approver_ip":"198.51.100.9"},"v":1}`,
		want:    "",
	},
	{
		name:    "should leave empty an event with only a reported remote host",
		payload: `{"action":"cert.requested","detail":{"remote_host":"203.0.113.5"},"v":1}`,
		want:    "",
	},
	{
		name:    "should leave empty a source_ip outside the detail",
		payload: `{"action":"auth.login","source_ip":"192.0.2.7","v":1}`,
		want:    "",
	},
	{
		name:    "should leave empty an event with no detail",
		payload: `{"action":"auth.login","v":1}`,
		want:    "",
	},
	{
		name:    "should leave empty a source_ip that is not a string",
		payload: `{"action":"cert.requested","detail":{"source_ip":7},"v":1}`,
		want:    "",
	},
	{
		name:    "should leave empty a null source_ip",
		payload: `{"action":"cert.requested","detail":{"source_ip":null},"v":1}`,
		want:    "",
	},
	{
		name:    "should leave empty a detail that is not an object",
		payload: `{"action":"cert.requested","detail":["192.0.2.7"],"v":1}`,
		want:    "",
	},
	{
		name:    "should leave empty a payload that is not JSON",
		payload: `{"detail":{"source_ip":"192.0.2.7"},not json`,
		want:    "",
	},
	{
		name:    "should leave empty an empty payload",
		payload: ``,
		want:    "",
	},
	{
		name:    "should leave empty a payload that is a bare string",
		payload: `"192.0.2.7"`,
		want:    "",
	},
}

// sourceIPEventID is the audit_events.id case i is seeded under.
func sourceIPEventID(i int) string {
	return fmt.Sprintf("ae-source-case-%d", i)
}

// seedSourceIPBackfillCases writes one audit_events row per case, with only
// the columns the table had before the migration. Bound parameters, so both
// dialects share it.
func seedSourceIPBackfillCases(t *testing.T, db *gorm.DB) {
	t.Helper()

	now := time.Now().UTC()
	for i, c := range sourceIPBackfillCases {
		if err := db.Exec(`INSERT INTO audit_events (id, created_at, payload) VALUES (?, ?, ?)`,
			sourceIPEventID(i), now, c.payload).Error; err != nil {
			t.Fatalf("failed to seed %q: %v", c.name, err)
		}
	}
}

// assertSourceIPBackfilled checks each case's source_ip column after the
// migration.
func assertSourceIPBackfilled(t *testing.T, db *gorm.DB) {
	t.Helper()

	for i, c := range sourceIPBackfillCases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			if err := db.Raw(`SELECT source_ip FROM audit_events WHERE id = ?`, sourceIPEventID(i)).
				Row().Scan(&got); err != nil {
				t.Fatalf("failed to read source_ip back: %v", err)
			}
			if got != c.want {
				t.Errorf("source_ip = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAuditSourceIPBackfill_ShouldCopyEachReadablePayloadsObservedSource(t *testing.T) {
	t.Parallel()

	db := sqlite.ConnectAndMigrate(t)
	if err := sqlite.RunTo(t, db, versionBeforeAuditSourceIP); err != nil {
		t.Fatalf("failed to step back before the source_ip column: %v", err)
	}
	seedSourceIPBackfillCases(t, db)
	if err := sqlite.RunUp(t, db); err != nil {
		t.Fatalf("failed to apply the source_ip column: %v", err)
	}

	assertSourceIPBackfilled(t, db)
}
