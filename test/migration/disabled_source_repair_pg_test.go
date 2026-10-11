//go:build dbparity

package migration_test

// The Postgres half of disabled_source_repair_test.go: the same rows, the
// same expectations, through the Postgres copy of the repair, which each
// dialect tree carries separately.

import (
	"testing"

	"github.com/mnestor/ssoossh/test/postgres"
)

func TestRepairDisabledSource_PostgresShouldClearOnlyStaleLDAPSyncSources(t *testing.T) {
	// Not t.Parallel() — see TestMigrationParity_SchemasShouldBeIdentical.
	ctx := t.Context()

	db, _ := postgres.ConnectAndMigrate(t, ctx)
	if err := postgres.RunTo(t, ctx, db, versionBeforeRepair); err != nil {
		t.Fatalf("failed to step back before the repair: %v", err)
	}
	seedRepairCases(t, db)
	if err := postgres.RunUp(t, ctx, db); err != nil {
		t.Fatalf("failed to apply the repair: %v", err)
	}

	assertRepaired(t, db)
}
