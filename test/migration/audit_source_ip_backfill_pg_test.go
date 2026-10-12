//go:build dbparity

package migration_test

// The Postgres half of audit_source_ip_backfill_test.go: the same payloads,
// the same expectations, through the Postgres copy of the backfill, which
// reads JSON with different functions than SQLite's.

import (
	"testing"

	"github.com/mnestor/ssoossh/test/postgres"
)

func TestAuditSourceIPBackfill_PostgresShouldCopyEachReadablePayloadsObservedSource(t *testing.T) {
	// Not t.Parallel() — see TestMigrationParity_SchemasShouldBeIdentical.
	ctx := t.Context()

	db, _ := postgres.ConnectAndMigrate(t, ctx)
	if err := postgres.RunTo(t, ctx, db, versionBeforeAuditSourceIP); err != nil {
		t.Fatalf("failed to step back before the source_ip column: %v", err)
	}
	seedSourceIPBackfillCases(t, db)
	if err := postgres.RunUp(t, ctx, db); err != nil {
		t.Fatalf("failed to apply the source_ip column: %v", err)
	}

	assertSourceIPBackfilled(t, db)
}
