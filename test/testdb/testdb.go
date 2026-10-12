// Package testdb gives Go tests an isolated database on whichever backend
// the run selects, so one test body runs on both SQLite and Postgres.
//
// SQLite is the default and needs no setup: every database is a private
// in-memory one. Postgres is selected with
//
//	SSOOSSH_TEST_DB=postgres
//	SSOOSSH_TEST_POSTGRES_DSN=postgres://user:pass@host:port/db?sslmode=disable
//
// where the DSN names a disposable server the tests may create and drop
// databases on. Each test then gets its own database (CREATE DATABASE, the
// same isolation the e2e harness uses since 705fd6c), dropped on cleanup, so
// t.Parallel() tests never see each other's rows. `make test-postgres` runs
// the whole suite that way against a throwaway container.
//
// Selecting Postgres without a DSN, or naming an unknown backend, fails the
// test rather than skipping it: a misconfigured CI leg must not go green
// having run nothing.
package testdb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	postgresMigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	sqliteMigrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for the admin DDL connection
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mnestor/ssoossh/server/dbtime"
	"github.com/mnestor/ssoossh/server/resources"
)

// Environment variables that select and locate the backend.
const (
	// EnvBackend selects the backend: "sqlite" (the default) or "postgres".
	EnvBackend = "SSOOSSH_TEST_DB"
	// EnvPostgresDSN is the admin DSN of a disposable Postgres server. Set
	// on its own, without EnvBackend, it also selects Postgres, which keeps
	// the older SSOOSSH_TEST_POSTGRES_DSN-only invocations working.
	EnvPostgresDSN = "SSOOSSH_TEST_POSTGRES_DSN"
)

// Backend names, as EnvBackend spells them and Connection returns them.
const (
	SQLite   = "sqlite"
	Postgres = "postgres"
)

// maxOpenConns caps each Postgres test pool. Parallel tests multiply pools
// against the server's default 100 connection slots; the whole suite at
// `go test -p 2` peaked at 14 connections.
const maxOpenConns = 8

// New returns an isolated database with the production migrations applied,
// on the backend the run selects. It is what a DB-backed test should use.
func New(t testing.TB) *gorm.DB {
	t.Helper()
	if IsPostgres(t) {
		return openPostgres(t, createPostgresDatabase(t, true))
	}
	db := openSQLite(t)
	if err := migrateSQLite(db); err != nil {
		t.Fatalf("testdb: failed to migrate sqlite: %v", err)
	}
	return db
}

// Empty returns an isolated database with no schema at all, for tests that
// build their own tables (a probe struct through AutoMigrate) or that drive
// the migrations themselves.
func Empty(t testing.TB) *gorm.DB {
	t.Helper()
	if IsPostgres(t) {
		return openPostgres(t, createPostgresDatabase(t, false))
	}
	return openSQLite(t)
}

// Connection returns a provider name and connection string for a fresh,
// empty database, for tests that hand a config to the server's own
// bootstrap (which connects and migrates by itself): ":memory:" on SQLite, a
// new database's DSN on Postgres.
func Connection(t testing.TB) (provider, connection string) {
	t.Helper()
	if IsPostgres(t) {
		return Postgres, createPostgresDatabase(t, false)
	}
	return SQLite, ":memory:"
}

// IsPostgres reports whether the run selected Postgres. A misconfigured
// selection fails the test.
func IsPostgres(t testing.TB) bool {
	t.Helper()
	backend, err := selectedBackend()
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	return backend == Postgres
}

// SkipOnPostgres skips a test that asserts SQLite-specific behaviour on
// purpose. reason says why, and shows in the -v output.
func SkipOnPostgres(t testing.TB, reason string) {
	t.Helper()
	if IsPostgres(t) {
		t.Skipf("SQLite-specific: %s", reason)
	}
}

// SkipOnSQLite skips a test that asserts Postgres-specific behaviour on
// purpose; it runs in the Postgres leg. reason says why.
func SkipOnSQLite(t testing.TB, reason string) {
	t.Helper()
	if !IsPostgres(t) {
		t.Skipf("Postgres-specific (runs with %s=%s): %s", EnvBackend, Postgres, reason)
	}
}

// WithoutForeignKeys runs fn with foreign key enforcement off, for a test
// that has to seed a row the schema would refuse: state an older schema, or
// a database restored by hand, could have left behind. Enforcement is back
// on when it returns. db must come from New or Empty.
//
// On Postgres this needs a superuser (session_replication_role), which the
// disposable test server always is.
func WithoutForeignKeys(t testing.TB, db *gorm.DB, fn func(tx *gorm.DB) error) {
	t.Helper()
	if IsPostgres(t) {
		err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL session_replication_role = replica").Error; err != nil {
				return fmt.Errorf("disable foreign keys: %w", err)
			}
			return fn(tx)
		})
		if err != nil {
			t.Fatalf("testdb: without foreign keys: %v", err)
		}
		return
	}
	// SQLite ignores the pragma inside a transaction, so it is set on the
	// pool's one connection around fn instead.
	if err := db.Exec("PRAGMA foreign_keys = OFF").Error; err != nil {
		t.Fatalf("testdb: disable foreign keys: %v", err)
	}
	defer func() {
		if err := db.Exec("PRAGMA foreign_keys = ON").Error; err != nil {
			t.Errorf("testdb: re-enable foreign keys: %v", err)
		}
	}()
	if err := fn(db); err != nil {
		t.Fatalf("testdb: without foreign keys: %v", err)
	}
}

// selectedBackend resolves the backend from the environment.
func selectedBackend() (string, error) {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv(EnvBackend)))
	dsn := os.Getenv(EnvPostgresDSN)
	switch backend {
	case "":
		if dsn != "" {
			return Postgres, nil
		}
		return SQLite, nil
	case SQLite:
		return SQLite, nil
	case Postgres:
		if dsn == "" {
			return "", fmt.Errorf("%s=%s but %s is not set", EnvBackend, Postgres, EnvPostgresDSN)
		}
		return Postgres, nil
	default:
		return "", fmt.Errorf("%s=%q: want %q or %q", EnvBackend, backend, SQLite, Postgres)
	}
}

// gormConfig mirrors bootstrap.openWithRetry, so tests run the same GORM
// configuration production does: translated errors, UTC timestamps, and a
// logger that, like production's below debug level, leaves "record not
// found" (an answer, not an error) out of the output.
func gormConfig() *gorm.Config {
	return &gorm.Config{
		TranslateError: true,
		NowFunc:        dbtime.NowFunc,
		Logger: logger.New(log.New(os.Stderr, "\r\n", log.LstdFlags), logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
		}),
	}
}

// sqliteDSN is a private in-memory database with the pragmas production
// always adds (bootstrap.addSqliteDefaultParameters): foreign keys enforced,
// as Postgres enforces them, and the same busy timeout.
const sqliteDSN = "file::memory:?_pragma=busy_timeout(2500)&_pragma=foreign_keys(1)"

// openSQLite opens a private in-memory SQLite database. One connection
// only, as production does for ":memory:": every connection to ":memory:"
// is a separate database, so a second one would see no tables.
func openSQLite(t testing.TB) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(sqliteDSN), gormConfig())
	if err != nil {
		t.Fatalf("testdb: failed to open in-memory sqlite: %v", err)
	}
	if err := db.Use(dbtime.Plugin{}); err != nil {
		t.Fatalf("testdb: failed to register the UTC timestamp plugin: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("testdb: failed to get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// migrateSQLite applies the embedded SQLite migrations over db's own pool.
// NoTxWrap matches production: the migration files carry their own
// BEGIN/COMMIT.
func migrateSQLite(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get sql.DB: %w", err)
	}
	driver, err := sqliteMigrate.WithInstance(sqlDB, &sqliteMigrate.Config{NoTxWrap: true})
	if err != nil {
		return fmt.Errorf("create sqlite migration driver: %w", err)
	}
	// Never closed: the sqlite driver's Close would close the test's pool,
	// and with it the only connection to its in-memory database.
	_, err = runUp(driver, "migrations/sqlite")
	return err
}

// runUp applies every up migration under dir through driver and returns
// the migrator, which the caller closes or not depending on who owns the
// connection beneath it.
func runUp(driver database.Driver, dir string) (*migrate.Migrate, error) {
	source, err := iofs.New(resources.FS, dir)
	if err != nil {
		return nil, fmt.Errorf("open migration source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "ssoossh", driver)
	if err != nil {
		return nil, fmt.Errorf("create migrator: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return m, fmt.Errorf("apply migrations: %w", err)
	}
	return m, nil
}

// openPostgres opens dsn with the production GORM configuration and a
// capped pool, closed on cleanup.
func openPostgres(t testing.TB, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(dsn), gormConfig())
	if err != nil {
		t.Fatalf("testdb: failed to open postgres: %v", err)
	}
	if err := db.Use(dbtime.Plugin{}); err != nil {
		t.Fatalf("testdb: failed to register the UTC timestamp plugin: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("testdb: failed to get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(2)
	// Registered after createPostgresDatabase's DROP, so it runs first
	// (cleanups are last-in-first-out) and the drop finds no pool of ours.
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// createPostgresDatabase creates a uniquely named database on the server
// EnvPostgresDSN names, migrated (a copy of the migrated template) or empty,
// registers its drop on cleanup, and returns its DSN.
func createPostgresDatabase(t testing.TB, migrated bool) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	adminDSN := os.Getenv(EnvPostgresDSN)
	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("testdb: failed to open the admin connection: %v", err)
	}
	defer func() { _ = admin.Close() }()

	name, err := randomName("t_")
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	create := "CREATE DATABASE " + name
	if migrated {
		template, err := ensureTemplate(ctx, admin, adminDSN)
		if err != nil {
			t.Fatalf("testdb: failed to prepare the migrated template: %v", err)
		}
		create += " TEMPLATE " + template
	}
	if err := execRetryingBusy(ctx, admin, create); err != nil {
		t.Fatalf("testdb: failed to create database %s: %v", name, err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := sql.Open("pgx", adminDSN)
		if err != nil {
			t.Errorf("testdb: failed to reopen the admin connection to drop %s: %v", name, err)
			return
		}
		defer func() { _ = admin.Close() }()
		// FORCE ends any connection the test leaked, such as a pool it
		// opened itself from Connection's DSN.
		if _, err := admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("testdb: failed to drop database %s: %v", name, err)
		}
	})

	dsn, err := withDatabase(adminDSN, name)
	if err != nil {
		t.Fatalf("testdb: %v", err)
	}
	return dsn
}

// execer is what execRetryingBusy runs statements on: a pool or one of its
// connections.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// execRetryingBusy runs database-level DDL, retrying while Postgres reports
// the database in use. Copying or renaming a database needs no other session
// in it, and a concurrent copy from the same template, or a session that has
// just closed, can briefly be one.
func execRetryingBusy(ctx context.Context, admin execer, stmt string) error {
	var err error
	for range 20 {
		if _, err = admin.ExecContext(ctx, stmt); err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "is being accessed by other users") {
			return err
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
	return err
}

// ensureTemplate returns the name of a database holding the up migrations,
// built once per migration set and shared by every package's test process.
// Copying it is far cheaper than migrating each test's database. The name
// carries a hash of the migration files, so a changed migration builds a
// new template rather than reusing a stale one.
func ensureTemplate(ctx context.Context, admin *sql.DB, adminDSN string) (string, error) {
	sum, err := migrationsHash("migrations/postgres")
	if err != nil {
		return "", err
	}
	name := "ssoossh_tmpl_" + sum

	exists, err := databaseExists(ctx, admin, name)
	if err != nil || exists {
		return name, err
	}

	// Serialise builders across processes: go test runs packages as
	// separate processes, all starting at once. A session-level advisory
	// lock lives on one connection, so pin one.
	conn, err := admin.Conn(ctx)
	if err != nil {
		return "", fmt.Errorf("check out a connection: %w", err)
	}
	defer func() { _ = conn.Close() }()
	const lockKey = 0x5500_55aa // arbitrary, fixed: "the testdb template lock"
	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return "", fmt.Errorf("take the template lock: %w", err)
	}
	// Best effort: closing the connection, deferred above, releases a
	// session lock regardless.
	defer func() { _, _ = conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey) }() //nolint:errcheck // see above

	if exists, err := databaseExists(ctx, admin, name); err != nil || exists {
		return name, err
	}

	// Build under a scratch name and rename at the end, so a builder that
	// dies half way leaves no database under the final name.
	scratch, err := randomName("ssoossh_tmpl_build_")
	if err != nil {
		return "", err
	}
	if _, err := conn.ExecContext(ctx, "CREATE DATABASE "+scratch); err != nil {
		return "", fmt.Errorf("create %s: %w", scratch, err)
	}
	if err := migratePostgres(ctx, adminDSN, scratch); err != nil {
		return "", errors.Join(err, dropDatabase(conn, scratch))
	}
	// ALLOW_CONNECTIONS false keeps sessions, autovacuum's included, out
	// of the template, which a copy requires.
	for _, stmt := range []string{
		"ALTER DATABASE " + scratch + " RENAME TO " + name,
		"ALTER DATABASE " + name + " WITH IS_TEMPLATE true ALLOW_CONNECTIONS false",
	} {
		// A closed session's backend can outlive the close by a moment.
		if err := execRetryingBusy(ctx, conn, stmt); err != nil {
			return "", errors.Join(fmt.Errorf("finish template %s: %w", name, err), dropDatabase(conn, scratch))
		}
	}
	return name, nil
}

// dropDatabase drops a half-built template so a failed build leaves nothing
// behind. It runs on a fresh context: the build's own may be what expired.
func dropDatabase(conn execer, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := conn.ExecContext(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("drop %s: %w", name, err)
	}
	return nil
}

// migratePostgres applies the embedded Postgres migrations to database
// name, through a connection of its own that is closed before returning.
func migratePostgres(ctx context.Context, adminDSN, name string) error {
	dsn, err := withDatabase(adminDSN, name)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	defer func() { _ = db.Close() }()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to %s: %w", name, err)
	}
	driver, err := postgresMigrate.WithInstance(db, &postgresMigrate.Config{})
	if err != nil {
		return fmt.Errorf("create postgres migration driver: %w", err)
	}
	m, err := runUp(driver, "migrations/postgres")
	if m != nil {
		// The driver holds a dedicated connection of its own; closing the
		// migrator is what releases it, and the template cannot be renamed
		// or copied while any session is in it.
		if _, closeErr := m.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("close the migrator: %w", closeErr)
		}
	}
	return err
}

// databaseExists reports whether a database named name exists.
func databaseExists(ctx context.Context, admin *sql.DB, name string) (bool, error) {
	var found int
	err := admin.QueryRowContext(ctx, "SELECT 1 FROM pg_database WHERE datname = $1", name).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look up database %s: %w", name, err)
	}
	return true, nil
}

// migrationsHash returns a short hex digest of every file under dir in the
// embedded migrations, names and contents both.
func migrationsHash(dir string) (string, error) {
	entries, err := fs.ReadDir(resources.FS, dir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", dir, err)
	}
	h := sha256.New()
	for _, e := range entries { // ReadDir returns entries sorted by name.
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(resources.FS, path.Join(dir, e.Name()))
		if err != nil {
			return "", fmt.Errorf("read %s: %w", e.Name(), err)
		}
		fmt.Fprintf(h, "%s\x00%d\x00", e.Name(), len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))[:16], nil
}

// randomName returns prefix plus 16 random hex characters: a valid,
// unquoted Postgres identifier, generated here rather than taken from
// input, so interpolating it into DDL (which cannot be parameterised) is
// safe.
func randomName(prefix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// not covered: crypto/rand does not fail on supported platforms.
		return "", fmt.Errorf("generate a database name: %w", err)
	}
	return prefix + hex.EncodeToString(b[:]), nil
}

// withDatabase swaps the database (path) component of a postgres:// URL
// DSN, keeping credentials, host and query parameters.
func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse %s: %w", EnvPostgresDSN, err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", fmt.Errorf("%s must be a postgres:// URL, got scheme %q", EnvPostgresDSN, u.Scheme)
	}
	u.Path = "/" + name
	return u.String(), nil
}
