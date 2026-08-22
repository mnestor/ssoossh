package bootstrap

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	postgresMigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/resources"
)

// livePostgresDSN returns the DSN for a disposable live Postgres instance,
// or skips the test when none is configured. These tests DROP AND RECREATE
// the public schema, so the DSN must never point at a database anyone cares
// about. Gated on an env var rather than a build tag so `go test ./...`
// stays green on machines without Docker.
//
//	docker run -d -e POSTGRES_USER=ssoossh -e POSTGRES_PASSWORD=e2etest \
//	  -e POSTGRES_DB=ssoossh -p 127.0.0.1:15432:5432 postgres:17-alpine
//	SSOOSSH_TEST_POSTGRES_DSN='postgres://ssoossh:e2etest@127.0.0.1:15432/ssoossh?sslmode=disable' \
//	  go test ./server/bootstrap/ -run TestLivePostgres
func livePostgresDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SSOOSSH_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("SSOOSSH_TEST_POSTGRES_DSN not set; skipping live Postgres test")
	}
	return dsn
}

// connectLivePostgres connects to the live instance and resets it to an
// empty schema so every test starts from nothing.
func connectLivePostgres(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	c := &config.Config{}
	c.DB.Provider = config.DBProviderPostgres
	c.DB.Connection = dsn

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("failed to connect to live postgres: %v", err)
	}
	if err := db.Exec("DROP SCHEMA public CASCADE; CREATE SCHEMA public").Error; err != nil {
		t.Fatalf("failed to reset schema: %v", err)
	}
	return db
}

// pgTables returns the user tables present in the public schema.
func pgTables(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var tables []string
	err := db.Raw(
		"SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name",
	).Scan(&tables).Error
	if err != nil {
		t.Fatalf("failed to list tables: %v", err)
	}
	return tables
}

// newLiveMigrator builds a migrate instance against the live database from
// the embedded postgres migration files, the same way migrateDatabase does.
func newLiveMigrator(t *testing.T, db *gorm.DB) *migrate.Migrate {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql.DB: %v", err)
	}
	conn, err := sqlDB.Conn(t.Context())
	if err != nil {
		t.Fatalf("failed to get connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	driver, err := postgresMigrate.WithConnection(t.Context(), conn, &postgresMigrate.Config{})
	if err != nil {
		t.Fatalf("failed to create migration driver: %v", err)
	}
	source, err := iofs.New(resources.FS, "migrations/postgres")
	if err != nil {
		t.Fatalf("failed to create migration source: %v", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "ssoossh", driver)
	if err != nil {
		t.Fatalf("failed to create migrate instance: %v", err)
	}
	return m
}

// TestLivePostgres_MigrationsShouldApplyRollBackAndReapply proves the
// embedded postgres migrations run on a real server: up from empty, fully
// down via the .down.sql files (which had never been executed against a live
// Postgres before this test), and up again. The down migration is the
// executable half of the ALLOW_DOWNGRADE story.
func TestLivePostgres_MigrationsShouldApplyRollBackAndReapply(t *testing.T) {
	dsn := livePostgresDSN(t)
	db := connectLivePostgres(t, dsn)

	// Up, via the same path production startup uses.
	if err := migrateDatabase(config.DBProviderPostgres, db); err != nil {
		t.Fatalf("up migration failed: %v", err)
	}
	up := pgTables(t, db)
	if len(up) < 7 {
		t.Fatalf("expected at least 7 tables after up migration, got %v", up)
	}

	// Down, via the embedded .down.sql.
	m := newLiveMigrator(t, db)
	if err := m.Down(); err != nil {
		t.Fatalf("down migration failed: %v", err)
	}
	down := pgTables(t, db)
	// golang-migrate's own bookkeeping table remains; nothing else should.
	for _, tbl := range down {
		if tbl != "schema_migrations" {
			t.Errorf("table %q survived the down migration", tbl)
		}
	}

	// Up again: the down must leave the database re-migratable.
	if err := m.Up(); err != nil {
		t.Fatalf("re-applying up migration after down failed: %v", err)
	}
	reup := pgTables(t, db)
	if strings.Join(reup, ",") != strings.Join(up, ",") {
		t.Errorf("table set after down+up differs from first up:\n first: %v\n reup:  %v", up, reup)
	}
}

// TestLivePostgres_SchemaShouldMatchSqlite migrates both dialects on real
// engines and compares the resulting schemas: same tables, same columns,
// same nullability. This is the live counterpart of
// TestMigrationParity_ShouldKeepSqliteAndPostgresSchemaInSync, which parses
// the .sql files; this one catches anything the engines interpret
// differently from how the parser reads it.
func TestLivePostgres_SchemaShouldMatchSqlite(t *testing.T) {
	dsn := livePostgresDSN(t)
	pg := connectLivePostgres(t, dsn)
	if err := migrateDatabase(config.DBProviderPostgres, pg); err != nil {
		t.Fatalf("postgres migration failed: %v", err)
	}

	sq := newSqliteMigrated(t)

	pgSchema := pgColumnNullability(t, pg)
	sqSchema := sqliteColumnNullability(t, sq)

	// golang-migrate's bookkeeping differs per driver; compare app tables only.
	delete(pgSchema, "schema_migrations")
	delete(sqSchema, "schema_migrations")

	if len(pgSchema) == 0 {
		t.Fatal("postgres schema came back empty")
	}
	for _, tbl := range sortedTableNames(pgSchema, sqSchema) {
		pgCols, inPg := pgSchema[tbl]
		sqCols, inSq := sqSchema[tbl]
		if !inPg || !inSq {
			t.Errorf("table %q exists in only one dialect (postgres=%v sqlite=%v)", tbl, inPg, inSq)
			continue
		}
		for _, col := range sortedTableNames(pgCols, sqCols) {
			pgNull, inPgCol := pgCols[col]
			sqNull, inSqCol := sqCols[col]
			if !inPgCol || !inSqCol {
				t.Errorf("%s.%s exists in only one dialect (postgres=%v sqlite=%v)", tbl, col, inPgCol, inSqCol)
				continue
			}
			if pgNull != sqNull {
				t.Errorf("%s.%s nullability differs: postgres=%v sqlite=%v", tbl, col, pgNull, sqNull)
			}
		}
	}
}

// newSqliteMigrated opens and migrates a fresh in-memory sqlite database.
func newSqliteMigrated(t *testing.T) *gorm.DB {
	t.Helper()
	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ":memory:"
	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("failed to connect sqlite: %v", err)
	}
	if err := migrateDatabase(config.DBProviderSqlite, db); err != nil {
		t.Fatalf("sqlite migration failed: %v", err)
	}
	return db
}

// pgColumnNullability maps table -> column -> is-nullable from a live
// Postgres public schema.
func pgColumnNullability(t *testing.T, db *gorm.DB) map[string]map[string]bool {
	t.Helper()
	rows, err := db.Raw(
		"SELECT table_name, column_name, is_nullable FROM information_schema.columns WHERE table_schema = 'public'",
	).Rows()
	if err != nil {
		t.Fatalf("failed to query postgres columns: %v", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[string]map[string]bool{}
	for rows.Next() {
		var tbl, col, nullable string
		if err := rows.Scan(&tbl, &col, &nullable); err != nil {
			t.Fatalf("failed to scan postgres column row: %v", err)
		}
		if out[tbl] == nil {
			out[tbl] = map[string]bool{}
		}
		out[tbl][col] = nullable == "YES"
	}
	return out
}

// sqliteColumnNullability maps table -> column -> is-nullable from a live
// sqlite database via pragma_table_info.
func sqliteColumnNullability(t *testing.T, db *gorm.DB) map[string]map[string]bool {
	t.Helper()
	var tables []string
	err := db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables).Error
	if err != nil {
		t.Fatalf("failed to list sqlite tables: %v", err)
	}

	out := map[string]map[string]bool{}
	for _, tbl := range tables {
		rows, err := db.Raw("SELECT name, \"notnull\" FROM pragma_table_info(?)", tbl).Rows()
		if err != nil {
			t.Fatalf("failed to query sqlite columns for %s: %v", tbl, err)
		}
		out[tbl] = map[string]bool{}
		for rows.Next() {
			var col string
			var notNull int
			if err := rows.Scan(&col, &notNull); err != nil {
				t.Fatalf("failed to scan sqlite column row: %v", err)
			}
			out[tbl][col] = notNull == 0
		}
		_ = rows.Close()
	}
	return out
}

// sortedTableNames returns the union of both maps' keys, sorted, so the
// comparison reports one-sided entries deterministically.
func sortedTableNames[V any](a, b map[string]V) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
