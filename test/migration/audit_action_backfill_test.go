package migration_test

// Test methodology: step a database back to the version before
// 20261012000000_audit_event_action, write audit_events rows the way the
// recorder used to (payload only), migrate up, and read back each row's
// action column. What matters: every readable payload's string action is
// copied, wherever the key sits in the document, and a payload that cannot
// be read leaves '' rather than failing the migration.
//
// The Postgres half is audit_action_backfill_pg_test.go.

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/test/sqlite"
)

// versionBeforeAuditAction is the migration the action column follows.
const versionBeforeAuditAction = 20261011120000

// backfillCase is one stored payload and the action the backfill should
// give its row.
type backfillCase struct {
	name    string
	payload string
	want    string
}

// backfillCases covers the payloads the recorder writes and the ones a
// damaged or foreign row could hold.
var backfillCases = []backfillCase{
	{
		name:    "should copy the action of a recorded login",
		payload: `{"action":"auth.login","actor":{"user_id":"u-1","groups":["ssh-admins"]},"occurred_at":"2026-10-01T00:00:00Z","v":1}`,
		want:    "auth.login",
	},
	{
		name:    "should copy a denied login without confusing it with a login",
		payload: `{"action":"auth.login_denied","occurred_at":"2026-10-01T00:00:00Z","v":1}`,
		want:    "auth.login_denied",
	},
	{
		name:    "should copy the action when it is not the first key",
		payload: `{"v":1,"system":true,"action":"user.auto_disabled"}`,
		want:    "user.auto_disabled",
	},
	{
		name:    "should copy the action when a detail string holds a NUL escape",
		payload: `{"action":"cert.denied","detail":{"note":"a\u0000b"},"v":1}`,
		want:    "cert.denied",
	},
	{
		name:    "should copy the action when an escaped backslash precedes u0000",
		payload: `{"action":"cert.denied","detail":{"note":"a\\u0000b"},"v":1}`,
		want:    "cert.denied",
	},
	{
		name:    "should leave empty a payload that is not JSON",
		payload: `{"action":"auth.login",not json`,
		want:    "",
	},
	{
		name:    "should leave empty an empty payload",
		payload: ``,
		want:    "",
	},
	{
		name:    "should leave empty a payload with no action",
		payload: `{"v":1}`,
		want:    "",
	},
	{
		name:    "should leave empty a null action",
		payload: `{"action":null}`,
		want:    "",
	},
	{
		name:    "should leave empty an action that is not a string",
		payload: `{"action":7}`,
		want:    "",
	},
	{
		name:    "should leave empty a payload that is an array",
		payload: `["auth.login"]`,
		want:    "",
	},
	{
		name:    "should leave empty a payload that is a bare string",
		payload: `"auth.login"`,
		want:    "",
	},
}

// backfillEventID is the audit_events.id case i is seeded under.
func backfillEventID(i int) string {
	return fmt.Sprintf("ae-case-%d", i)
}

// seedBackfillCases writes one audit_events row per case, with only the
// columns the table had before the migration. Bound parameters, so both
// dialects share it.
func seedBackfillCases(t *testing.T, db *gorm.DB) {
	t.Helper()

	now := time.Now().UTC()
	for i, c := range backfillCases {
		if err := db.Exec(`INSERT INTO audit_events (id, created_at, payload) VALUES (?, ?, ?)`,
			backfillEventID(i), now, c.payload).Error; err != nil {
			t.Fatalf("failed to seed %q: %v", c.name, err)
		}
	}
}

// assertBackfilled checks each case's action column after the migration.
func assertBackfilled(t *testing.T, db *gorm.DB) {
	t.Helper()

	for i, c := range backfillCases {
		t.Run(c.name, func(t *testing.T) {
			var got string
			if err := db.Raw(`SELECT action FROM audit_events WHERE id = ?`, backfillEventID(i)).
				Row().Scan(&got); err != nil {
				t.Fatalf("failed to read the action back: %v", err)
			}
			if got != c.want {
				t.Errorf("action = %q, want %q", got, c.want)
			}
		})
	}
}

func TestAuditActionBackfill_ShouldCopyEachReadablePayloadsAction(t *testing.T) {
	t.Parallel()

	db := sqlite.ConnectAndMigrate(t)
	if err := sqlite.RunTo(t, db, versionBeforeAuditAction); err != nil {
		t.Fatalf("failed to step back before the action column: %v", err)
	}
	seedBackfillCases(t, db)
	if err := sqlite.RunUp(t, db); err != nil {
		t.Fatalf("failed to apply the action column: %v", err)
	}

	assertBackfilled(t, db)
}
