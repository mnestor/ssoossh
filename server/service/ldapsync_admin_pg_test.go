//go:build dbparity

package service

import (
	"testing"

	"github.com/mnestor/ssoossh/test/postgres"
)

// should read each user's latest login on Postgres, where created_at is a
// TIMESTAMPTZ rather than SQLite's UTC text and MAX and the join compare
// instants. Not parallel: the Postgres harness binds one fixed port.
func TestLatestLoginGroups_shouldReadEachUsersLatestLoginOnPostgres(t *testing.T) {
	db, _ := postgres.ConnectAndMigrate(t, t.Context())
	checkLatestLoginGroups(t, db)
}
