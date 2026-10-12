//go:build dbparity

package migration_test

// The Postgres half of audit_action_backfill_test.go: the same payloads, the
// same expectations, through the Postgres copy of the backfill, which reads
// JSON with different functions than SQLite's.

import (
	"testing"

	"github.com/mnestor/ssoossh/test/postgres"
)

func TestAuditActionBackfill_PostgresShouldCopyEachReadablePayloadsAction(t *testing.T) {
	// Not t.Parallel() — see TestMigrationParity_SchemasShouldBeIdentical.
	ctx := t.Context()

	db, _ := postgres.ConnectAndMigrate(t, ctx)
	if err := postgres.RunTo(t, ctx, db, versionBeforeAuditAction); err != nil {
		t.Fatalf("failed to step back before the action column: %v", err)
	}
	seedBackfillCases(t, db)
	if err := postgres.RunUp(t, ctx, db); err != nil {
		t.Fatalf("failed to apply the action column: %v", err)
	}

	assertBackfilled(t, db)
}
