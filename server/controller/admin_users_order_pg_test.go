//go:build dbparity

package controller

import (
	"testing"

	"github.com/mnestor/ssoossh/test/postgres"
)

// should order the admin user directory identically on Postgres, where the
// collation behind LOWER and ORDER BY differs from SQLite. Not parallel: the
// Postgres harness binds one fixed port.
func TestListUsersHandler_ShouldOrderTheDirectoryOnPostgres(t *testing.T) {
	db, _ := postgres.ConnectAndMigrate(t, t.Context())
	checkUserOrder(t, db)
}
