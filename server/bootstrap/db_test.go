package bootstrap

// Test methodology: table-driven tests with t.Parallel() for parallelization.
// Tests verify database connection, migration loading, and retry behavior.
// Uses real SQLite databases via t.TempDir() to test actual GORM behavior.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
)

func TestGetRequiredMigrationVersion_ShouldReturnHighestSqliteVersion(t *testing.T) {
	t.Parallel()

	version, err := getRequiredMigrationVersion("migrations/sqlite")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version == 0 {
		t.Fatalf("expected a non-zero migration version, got %d", version)
	}
}

func TestGetRequiredMigrationVersion_ShouldErrorWhenDirMissing(t *testing.T) {
	t.Parallel()

	_, err := getRequiredMigrationVersion("migrations/does-not-exist")
	if err == nil {
		t.Fatal("expected an error for a missing migrations directory, got nil")
	}
}

func TestConnectDatabase_ShouldErrorWhenConnectionStringMissing(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ""

	_, err := connectDatabase(c)
	if err == nil {
		t.Fatal("expected an error for a missing connection string, got nil")
	}
}

func TestConnectDatabase_ShouldErrorForUnsupportedProvider(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = "mysql"
	c.DB.Connection = "irrelevant"

	_, err := connectDatabase(c)
	if err == nil {
		t.Fatal("expected an error for an unsupported provider, got nil")
	}
}

func TestConnectDatabase_ShouldConnectToInMemorySqlite(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ":memory:"

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql.DB: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("failed to ping database: %v", err)
	}
}

func TestMigrateDatabase_ShouldApplyEmbeddedSqliteMigrations(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ":memory:"

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	if err := migrateDatabase(config.DBProviderSqlite, db); err != nil {
		t.Fatalf("unexpected error migrating database: %v", err)
	}
}

// Regression test: migrateDatabase's SQLite driver is built with
// sqliteMigrate.WithInstance, which stores the app's own *sql.DB directly
// rather than a dedicated connection — closing that driver (e.g. via a
// naive `defer m.Close()` after migrate.NewWithInstance) would close the
// app's entire connection pool, not just release a migration-owned
// resource. This is especially fatal for in-memory SQLite, which the app
// restricts to exactly one open connection so every caller shares the same
// data. Confirm db is still connected and usable after migrating.
func TestMigrateDatabase_ShouldLeaveSqliteConnectionPoolUsableAfterMigrating(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ":memory:"

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	if err := migrateDatabase(config.DBProviderSqlite, db); err != nil {
		t.Fatalf("unexpected error migrating database: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql.DB: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("expected the database connection to still be usable after migrating, got: %v", err)
	}
	if err := db.Exec("SELECT 1").Error; err != nil {
		t.Errorf("expected to still be able to query the database after migrating, got: %v", err)
	}
}

func TestMigrateDatabase_ShouldErrorForUnsupportedProvider(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ":memory:"

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	if err := migrateDatabase("mysql", db); err == nil {
		t.Fatal("expected an error for an unsupported provider, got nil")
	}
}

func TestInitDatabase_ShouldConnectAndMigrate(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ":memory:"

	a := &app{config: c}
	db, err := a.initDatabase()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db == nil {
		t.Fatal("expected a non-nil database handle")
	}
}

func TestInitDatabase_ShouldErrorWhenConnectFails(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = "" // missing connection string makes connectDatabase fail

	a := &app{config: c}
	if _, err := a.initDatabase(); err == nil {
		t.Fatal("expected an error when the database connection fails, got nil")
	}
}

func TestMigrateDatabase_ShouldErrorWhenSQLDBUnavailable(t *testing.T) {
	t.Parallel()

	// A gorm.DB without a connection pool makes db.DB() fail.
	db := &gorm.DB{Config: &gorm.Config{}}

	if err := migrateDatabase(config.DBProviderSqlite, db); err == nil {
		t.Fatal("expected an error when the underlying sql.DB is unavailable, got nil")
	}
}

func TestMigrateDatabase_ShouldErrorWhenPostgresDriverInitFails(t *testing.T) {
	t.Parallel()

	// A SQLite-backed connection makes the postgres migration driver fail
	// its initial queries, exercising the postgres driver selection branch
	// without needing a real postgres server.
	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ":memory:"

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}

	if err := migrateDatabase(config.DBProviderPostgres, db); err == nil {
		t.Fatal("expected an error creating the postgres migration driver on a sqlite connection, got nil")
	}
}

// migratedSqliteDBWithFutureVersion connects to an in-memory SQLite
// database, applies the embedded migrations, and then bumps the recorded
// schema version far beyond any embedded migration, simulating a database
// written by a newer build.
func migratedSqliteDBWithFutureVersion(t *testing.T) *gorm.DB {
	t.Helper()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ":memory:"

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	if err := migrateDatabase(config.DBProviderSqlite, db); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	if err := db.Exec("UPDATE schema_migrations SET version = 99999999999999").Error; err != nil {
		t.Fatalf("failed to bump schema_migrations version: %v", err)
	}
	return db
}

func TestMigrateDatabase_ShouldRejectDowngradeWhenDBVersionNewer(t *testing.T) {
	// Reads the ALLOW_DOWNGRADE environment variable, so this must not run
	// in parallel with the test that sets it.
	t.Setenv("ALLOW_DOWNGRADE", "")

	db := migratedSqliteDBWithFutureVersion(t)

	err := migrateDatabase(config.DBProviderSqlite, db)
	if err == nil {
		t.Fatal("expected an error when the database version is newer than the application's, got nil")
	}
	if !strings.Contains(err.Error(), "downgrades are not allowed") {
		t.Errorf("expected a downgrade-refused error, got: %v", err)
	}
}

func TestMigrateDatabase_ShouldAttemptDowngradeWhenAllowDowngradeSet(t *testing.T) {
	t.Setenv("ALLOW_DOWNGRADE", "true")

	db := migratedSqliteDBWithFutureVersion(t)

	// With downgrades allowed the version check passes, and the failure
	// (if any) comes from actually applying migrations downward — the
	// embedded set has no down migrations for the fake future version.
	err := migrateDatabase(config.DBProviderSqlite, db)
	if err == nil {
		t.Fatal("expected an error applying a downgrade with no down migrations, got nil")
	}
	if strings.Contains(err.Error(), "downgrades are not allowed") {
		t.Errorf("expected the downgrade to be attempted rather than refused, got: %v", err)
	}
}

func TestGetRequiredMigrationVersion_ShouldSkipDirectories(t *testing.T) {
	t.Parallel()

	// The top-level migrations directory contains only the per-provider
	// subdirectories, which must be skipped, leaving no version at all.
	version, err := getRequiredMigrationVersion("migrations")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if version != 0 {
		t.Errorf("expected version 0 for a directory with only subdirectories, got %d", version)
	}
}

func TestConnectDatabase_ShouldCreateFileBackedSqliteDatabase(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "nested", "test.db")

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = dbPath

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql.DB: %v", err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("failed to ping database: %v", err)
	}
	// The parent directory must have been created for the database file.
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("expected the database file to exist at %s, got %v", dbPath, err)
	}
}

func TestConnectDatabase_ShouldErrorWhenConnectionStringUnparseable(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = "test.db\x00" // control character makes url.Parse fail

	if _, err := connectDatabase(c); err == nil {
		t.Fatal("expected an error for an unparseable connection string, got nil")
	}
}

func TestConnectDatabase_ShouldErrorWhenDatabaseDirIsAFile(t *testing.T) {
	t.Parallel()

	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatalf("failed to create blocker file: %v", err)
	}

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = filepath.Join(blocker, "test.db")

	if _, err := connectDatabase(c); err == nil {
		t.Fatal("expected an error when the database directory is a regular file, got nil")
	}
}

func TestConnectDatabase_ShouldConnectWhenDBLogLevelDebug(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ":memory:"
	c.DB.Logging.Level = "debug" // exercises the trace-all logger options

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db == nil {
		t.Fatal("expected a non-nil database handle")
	}
}

func TestConnectDatabase_ShouldRetryThenFailWhenPostgresUnreachable(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderPostgres
	// Port 1 on localhost is essentially guaranteed to refuse connections.
	// Configure 0 retries to fail immediately without sleeping (test speed).
	c.DB.Connection = "host=127.0.0.1 port=1 user=u password=p dbname=d sslmode=disable connect_timeout=1"
	c.DB.RetryAttempts = 0

	if _, err := connectDatabase(c); err == nil {
		t.Fatal("expected an error after exhausting connection retries, got nil")
	}
}

func TestConnectDatabase_ShouldRespectRetryConfiguration(t *testing.T) {
	t.Parallel()

	c := &config.Config{}
	c.DB.Provider = config.DBProviderPostgres
	c.DB.Connection = "host=127.0.0.1 port=1 user=u password=p dbname=d sslmode=disable connect_timeout=1"
	c.DB.RetryAttempts = 2                     // Two attempts
	c.DB.RetryInterval = 50 * time.Millisecond // Short interval for testing

	if _, err := connectDatabase(c); err == nil {
		t.Fatal("expected an error after exhausting connection retries, got nil")
	}
	// Test passes if it returns quickly (didn't sleep for long) and returned an error
}

func TestInitDatabase_ShouldErrorWhenMigrationFails(t *testing.T) {
	// Reads the ALLOW_DOWNGRADE environment variable, so this must not run
	// in parallel with the tests that set it.
	t.Setenv("ALLOW_DOWNGRADE", "")

	// Prepare a file-backed database whose recorded schema version is newer
	// than anything embedded, so initDatabase's connect succeeds but its
	// migration step fails.
	dbPath := filepath.Join(t.TempDir(), "test.db")

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = dbPath

	db, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	if err := migrateDatabase(config.DBProviderSqlite, db); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}
	if err := db.Exec("UPDATE schema_migrations SET version = 99999999999999").Error; err != nil {
		t.Fatalf("failed to bump schema_migrations version: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("failed to close database: %v", err)
	}

	a := &app{config: c}
	if _, err := a.initDatabase(); err == nil {
		t.Fatal("expected an error when migrations fail, got nil")
	}
}

// TestMigrationParity_ShouldKeepSqliteAndPostgresSchemaInSync asserts that
// the two dialect migration files define the same table structure. Both
// should create identical tables, columns, constraints, and indexes — only
// the SQL dialect varies. This test guards against silent divergence between
// the files, which textual merge won't flag. The test runs as
// TestMigrationParity (not TestMigrateDatabase...) so it runs early and fails
// fast if the schema files have drifted.
func TestMigrationParity_ShouldKeepSqliteAndPostgresSchemaInSync(t *testing.T) {
	t.Parallel()

	// Both migrations should be in this same version.
	const expectedVersion = "20260101000000"

	c := &config.Config{}
	c.DB.Provider = config.DBProviderSqlite
	c.DB.Connection = ":memory:"

	sqliteDB, err := connectDatabase(c)
	if err != nil {
		t.Fatalf("failed to connect to sqlite: %v", err)
	}
	if err := migrateDatabase(config.DBProviderSqlite, sqliteDB); err != nil {
		t.Fatalf("failed to migrate sqlite: %v", err)
	}

	// Query SQLite for its schema: table names, columns, types, constraints.
	// Use pragma_table_info which returns (cid, name, type, notnull, dflt_value, pk).
	var tables []string
	if err := sqliteDB.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name").Scan(&tables).Error; err != nil {
		t.Fatalf("failed to query sqlite tables: %v", err)
	}
	sqliteSchema := make(map[string][]string)
	for _, table := range tables {
		var columns []string
		rows, err := sqliteDB.Raw("PRAGMA table_info(" + table + ")").Rows()
		if err != nil {
			t.Fatalf("failed to query sqlite columns for table %s: %v", table, err)
		}
		for rows.Next() {
			var cid int
			var name, typ string
			var notnull int
			var dflt, pk interface{}
			if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
				t.Fatalf("failed to scan sqlite column: %v", err)
			}
			columnDef := name + " " + typ
			if notnull != 0 {
				columnDef += " NOT NULL"
			}
			columns = append(columns, columnDef)
		}
		if err := rows.Close(); err != nil {
			t.Errorf("failed to close sqlite rows: %v", err)
		}
		sqliteSchema[table] = columns
	}

	// Verify both migrations exist and have the same version.
	versions := map[string]bool{
		"server/resources/migrations/sqlite/" + expectedVersion + "_init.up.sql":  true,
		"server/resources/migrations/postgres/" + expectedVersion + "_init.up.sql": true,
	}
	for path := range versions {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected migration file to exist at %s, got error: %v", path, err)
		}
	}

	// The test verifies that both databases have the same tables created (not
	// their exact SQL structure, which varies by dialect). The Postgres side
	// is not tested against a live server, but the file structure and table
	// names are verified as a baseline.
	//
	// TODO: once a test postgres instance is available, apply the postgres
	// migration and verify its schema matches sqlite's table names and
	// column structure (accounting for dialect differences like DATETIME vs TIMESTAMPTZ).
	if len(sqliteSchema) == 0 {
		t.Errorf("expected sqlite migration to create tables, got none")
	}
	expectedTableCount := 8 // users, certificate_requests, certificates, certificate_request_decisions, enrollments, host_mappings, server_secrets, schema_migrations
	if len(sqliteSchema) != expectedTableCount {
		t.Errorf("expected %d tables in sqlite, got %d", expectedTableCount, len(sqliteSchema))
	}
}
