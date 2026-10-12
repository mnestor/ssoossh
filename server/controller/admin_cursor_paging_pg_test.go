//go:build dbparity

package controller

import (
	"testing"

	"github.com/mnestor/ssoossh/test/postgres"
)

// should page all five admin lists by cursor identically on Postgres, where
// timestamps are microsecond TIMESTAMPTZ and the collation behind LOWER and
// ORDER BY differs from SQLite. Not parallel: the Postgres harness binds one
// fixed port.
func TestAdminLists_ShouldPageByCursorOnPostgres(t *testing.T) {
	db, _ := postgres.ConnectAndMigrate(t, t.Context())
	checkCursorPaging(t, db)
}
