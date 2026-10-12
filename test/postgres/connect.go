// Package postgres provides Postgres schema-inspection helpers for the
// dialect-parity suite (test/migration). The database itself comes from
// test/testdb, which every DB-backed test shares; this package adds what
// only a schema test needs.
package postgres

import (
	"context"
	"testing"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/test/testdb"
)

// ConnectAndMigrate returns a fresh, migrated database of the test's own on
// the run's Postgres (test/testdb), and skips the test on the SQLite
// backend. Each call is isolated, so callers may run in parallel.
//
// The second return is always empty. It once named the container this
// function started, and stays so callers written against that keep
// compiling; the Postgres server now comes from SSOOSSH_TEST_POSTGRES_DSN
// (`make test-postgres`, or the CI Postgres leg).
func ConnectAndMigrate(t *testing.T, _ context.Context) (*gorm.DB, string) {
	t.Helper()
	testdb.SkipOnSQLite(t, "inspects or migrates a Postgres schema")
	return testdb.New(t), ""
}
