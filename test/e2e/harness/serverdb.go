//go:build e2e || resilience || load

package harness

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mnestor/ssoossh/server/dbtime"
)

// OpenDB opens the running server's own database, for a test that seeds
// rows no HTTP surface writes. The server has already migrated it by the
// time StartServer returns, so the tables are there. The connection is
// closed in t.Cleanup.
//
// Needs ServerOptions.FileDB on a sqlite run: an in-memory database lives
// only inside the ssoosshd process and cannot be opened from here.
func (s *Server) OpenDB(t *testing.T) *gorm.DB {
	t.Helper()

	var dialector gorm.Dialector
	switch {
	case s.db.provider == "postgres":
		dialector = postgres.Open(s.db.dsn)
	case s.db.dsn == ":memory:":
		t.Fatalf("harness: this server's sqlite database is in memory; start it with ServerOptions.FileDB to open it from the test")
	default:
		// busy_timeout because ssoosshd holds the same file open and may be
		// mid-write when a seed lands.
		dialector = sqlite.Open("file:" + s.db.dsn + "?_pragma=busy_timeout(5000)")
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Discard,
		// The server stores every timestamp in UTC (package dbtime); rows
		// seeded here must too, or sqlite's string comparison of DATETIME
		// columns stops matching chronological order.
		NowFunc: dbtime.NowFunc,
	})
	if err != nil {
		t.Fatalf("harness: failed to open the server's database: %v", err)
	}
	if err := db.Use(dbtime.Plugin{}); err != nil {
		t.Fatalf("harness: failed to register the UTC timestamp plugin: %v", err)
	}

	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close() // best-effort teardown
		}
	})
	return db
}
