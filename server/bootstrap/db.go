package bootstrap

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	postgresMigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	sqliteMigrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	slogGorm "github.com/orandin/slog-gorm"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/logging"
	"github.com/mnestor/ssoossh/server/resources"
)

// initDatabase connects to the database configured on a.config and applies
// any pending embedded migrations before returning the ready-to-use handle.
func (a *app) initDatabase() (db *gorm.DB, err error) {
	db, err = connectDatabase(a.config)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Run migrations
	if err := migrateDatabase(a.config.DB.Provider, db); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

// migrateDatabase applies any embedded migrations for provider that haven't
// yet been run against db, refusing to proceed if the database's current
// migration version is newer than what this build supports (unless
// ALLOW_DOWNGRADE=true is set).
func migrateDatabase(provider config.DBProvider, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}

	// Choose the correct driver for the database provider.
	//
	// Neither driver's Close() is safe to call here without care: both are
	// built from sqlDB, the app's own connection pool (required, not just
	// convenient, for in-memory SQLite: the app restricts it to exactly one
	// open connection so every caller shares the same in-memory data — a
	// second, independently-opened *sql.DB would migrate an empty database
	// nobody else ever reads from). sqlite3.WithInstance stores sqlDB
	// directly, so its Close() would close the app's entire pool — never
	// call it. postgres.WithInstance also stores sqlDB (same hazard), but
	// additionally checks out one dedicated *sql.Conn from the pool for
	// locking, and only that dedicated conn needs to be released back to
	// the pool afterward. Building the Postgres driver via WithConnection
	// on a conn we check out and close ourselves, instead of WithInstance,
	// leaves its internal db reference nil, so releasing it is safe.
	var driver database.Driver
	switch provider {
	case config.DBProviderSqlite:
		driver, err = sqliteMigrate.WithInstance(sqlDB, &sqliteMigrate.Config{
			NoTxWrap: true,
		})
	case config.DBProviderPostgres:
		conn, connErr := sqlDB.Conn(context.Background())
		if connErr != nil {
			return fmt.Errorf("failed to check out a connection for migrations: %w", connErr)
		}
		defer func() {
			if releaseErr := conn.Close(); releaseErr != nil {
				slog.Warn("failed to release migration database connection", slog.Any("error", releaseErr))
			}
		}()
		driver, err = postgresMigrate.WithConnection(context.Background(), conn, &postgresMigrate.Config{})
	default:
		// Should never happen at this point
		return fmt.Errorf("unsupported database provider: %s", provider)
	}
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	// Embedded migrations via iofs
	path := "migrations/" + string(provider)
	source, err := iofs.New(resources.FS, path)
	if err != nil {
		return fmt.Errorf("failed to create embedded migration source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "pocket-id", driver)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}

	requiredVersion, err := getRequiredMigrationVersion(path)
	if err != nil {
		return fmt.Errorf("failed to get last migration version: %w", err)
	}

	currentVersion, _, err := m.Version()
	if err != nil && !errors.Is(err, migrate.ErrNilVersion) {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}
	if currentVersion > requiredVersion {
		slog.Warn("Database version is newer than the application supports, possible downgrade detected", slog.Uint64("db_version", uint64(currentVersion)), slog.Uint64("app_version", uint64(requiredVersion)))
		if strings.ToLower(os.Getenv("ALLOW_DOWNGRADE")) != "true" {
			return fmt.Errorf("database version (%d) is newer than application version (%d), downgrades are not allowed (set ALLOW_DOWNGRADE=true to enable)", currentVersion, requiredVersion)
		}
	}

	if err := m.Migrate(requiredVersion); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to apply embedded migrations: %w", err)
	}
	return nil
}

// connectDatabase opens a GORM connection for the configured database provider
// (SQLite or PostgreSQL), retrying according to RetryAttempts and RetryInterval.
// Both providers use the same retry loop; SQLite typically succeeds immediately
// since it's file-backed, while PostgreSQL may retry on transient network issues.
func connectDatabase(c *config.Config) (db *gorm.DB, err error) {
	if c.DB.Connection == "" {
		return nil, errors.New("missing required db.connection_string")
	}

	dialector, onConnFn, err := dbDialector(c)
	if err != nil {
		return nil, err
	}

	maxAttempts := c.DB.RetryAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1 // No retries, just one attempt
	}
	retryInterval := c.DB.RetryInterval
	if retryInterval <= 0 {
		retryInterval = 1 * time.Second // Default fallback
	}

	return openWithRetry(dialector, dbLogger(c), onConnFn, c.DB.Provider, maxAttempts, retryInterval)
}

// dbDialector builds the GORM dialector for c.DB.Provider, along with an
// optional callback to run against the raw *sql.DB immediately after a
// successful connection (used by SQLite in-memory databases).
func dbDialector(c *config.Config) (dialector gorm.Dialector, onConnFn func(conn *sql.DB), err error) {
	switch c.DB.Provider {
	case config.DBProviderSqlite:
		registerSqliteFunctions()

		connString, dbPath, isMemoryDB, err := parseSqliteConnectionString(c.DB.Connection)
		if err != nil {
			return nil, nil, err
		}

		if !isMemoryDB {
			if err := ensureSqliteDatabaseDir(dbPath); err != nil {
				return nil, nil, err
			}
		}

		// Before we connect, also make sure that there's a temporary folder for SQLite to write its data
		if err := ensureSqliteTempDir(filepath.Dir(dbPath)); err != nil {
			return nil, nil, err
		}

		if isMemoryDB {
			// For in-memory SQLite databases, we must limit to 1 open connection at the same time, or they won't see the whole data
			// The other workaround, of using shared caches, doesn't work well with multiple write transactions trying to happen at once
			onConnFn = func(conn *sql.DB) {
				conn.SetMaxOpenConns(1)
			}
		}

		return sqlite.Open(connString), onConnFn, nil
	case config.DBProviderPostgres:
		return postgres.Open(c.DB.Connection), nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported database provider: %s", c.DB.Provider)
	}
}

// dbLogger builds the slog-backed GORM logger, with verbosity driven by
// c.DB.Logging.Level.
func dbLogger(c *config.Config) logger.Interface {
	loggerOpts := make([]slogGorm.Option, 0, 6)
	dbLogLevel := logging.LevelFromString(c.DB.Logging.Level)
	loggerOpts = append(loggerOpts,
		slogGorm.WithHandler(slog.With("type", "db").Handler()),
		slogGorm.WithSlowThreshold(200*time.Millisecond),
		slogGorm.WithErrorField("error"),
		slogGorm.SetLogLevel(slogGorm.DefaultLogType, dbLogLevel),
	)
	if dbLogLevel == slog.LevelDebug {
		loggerOpts = append(loggerOpts,
			slogGorm.WithRecordNotFoundError(),
			slogGorm.WithTraceAll(),
		)
	} else {
		loggerOpts = append(loggerOpts,
			slogGorm.WithIgnoreTrace(),
		)
	}
	return slogGorm.New(loggerOpts...)
}

// openWithRetry attempts to open a GORM connection via dialector up to
// maxAttempts times, sleeping retryInterval between attempts. On success,
// onConnFn (if non-nil) is run against the underlying *sql.DB before the
// connection is returned.
func openWithRetry(dialector gorm.Dialector, glogger logger.Interface, onConnFn func(conn *sql.DB), provider config.DBProvider, maxAttempts int, retryInterval time.Duration) (db *gorm.DB, err error) {
	for i := 1; i <= maxAttempts; i++ {
		db, err = gorm.Open(dialector, &gorm.Config{
			TranslateError: true,
			Logger:         glogger,
		})
		if err == nil {
			slog.Info("Connected to database", slog.String("provider", string(provider)))

			if onConnFn != nil {
				conn, connErr := db.DB()
				if connErr != nil {
					return nil, fmt.Errorf("failed to get underlying sql.DB: %w", connErr)
				}
				onConnFn(conn)
			}

			return db, nil
		}

		if i < maxAttempts {
			slog.Warn("Failed to connect to database, will retry",
				slog.Int("attempt", i),
				slog.Int("max_attempts", maxAttempts),
				slog.Duration("retry_interval", retryInterval),
				slog.String("provider", string(provider)),
				slog.Any("error", err),
			)
			time.Sleep(retryInterval)
		}
	}

	slog.Error("Failed to connect to database after all retry attempts",
		slog.Int("max_attempts", maxAttempts),
		slog.String("provider", string(provider)),
		slog.Any("error", err),
	)

	return nil, err
}

// getRequiredMigrationVersion reads the embedded migration files and returns the highest version number found.
func getRequiredMigrationVersion(path string) (uint, error) {
	entries, err := resources.FS.ReadDir(path)
	if err != nil {
		return 0, fmt.Errorf("failed to read migration directory: %w", err)
	}

	var maxVersion uint
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		var version uint
		n, err := fmt.Sscanf(name, "%d_", &version)
		if err == nil && n == 1 {
			if version > maxVersion {
				maxVersion = version
			}
		}
	}

	return maxVersion, nil
}
