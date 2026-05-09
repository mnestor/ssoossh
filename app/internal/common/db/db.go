package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/golang-migrate/migrate/v4/database"
	pgmigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	sqliteMigrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	slogGorm "github.com/orandin/slog-gorm"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormLogger "gorm.io/gorm/logger"
)

type DbProvider string

const (
	DbProviderSqlite   DbProvider = "sqlite"
	DbProviderPostgres DbProvider = "postgres"
)

type DbConfig struct {
	Provider         DbProvider `mapstructure:"provider"`
	ConnectionString string     `mapstructure:"connection_string"`
	LogLevel         string     `mapstructure:"log_level"`
}

func NewDatabase(cfg DbConfig) (db *gorm.DB, err error) {
	db, err = connectDatabase(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	sqlDb, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}

	// Choose the correct driver for the database provider
	var driver database.Driver
	switch cfg.Provider {
	case DbProviderSqlite:
		driver, err = sqliteMigrate.WithInstance(sqlDb, &sqliteMigrate.Config{
			NoTxWrap: true,
		})
	case DbProviderPostgres:
		driver, err = pgmigrate.WithInstance(sqlDb, &pgmigrate.Config{})
	default:
		// Should never happen at this point
		return nil, fmt.Errorf("unsupported database provider: %s", cfg.Provider)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create migration driver: %w", err)
	}

	// Run migrations
	migrationsPath := "migrations/" + string(cfg.Provider)
	if err := migrateDatabase(driver, migrationsPath); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	return db, nil
}

func connectDatabase(cfg DbConfig) (db *gorm.DB, err error) {
	if cfg.ConnectionString == "" {
		return nil, errors.New("missing required config value db.connection_string")
	}
	var dialector gorm.Dialector

	// Choose the correct database provider
	var onConnFn func(conn *sql.DB)
	switch cfg.Provider {
	case DbProviderSqlite:
		registerSqliteFunctions()

		connString, isMemoryDB, err := parseSqliteConnectionString(cfg.ConnectionString)
		if err != nil {
			return nil, err
		}

		if isMemoryDB {
			// For in-memory SQLite databases, we must limit to 1 open connection at the same time, or they won't see the whole data
			// The other workaround, of using shared caches, doesn't work well with multiple write transactions trying to happen at once
			onConnFn = func(conn *sql.DB) {
				conn.SetMaxOpenConns(1)
			}
		}

		dialector = sqlite.Open(connString)
	case DbProviderPostgres:
		dialector = postgres.Open(cfg.ConnectionString)
	default:
		return nil, fmt.Errorf("unsupported database provider: %s", cfg.Provider)
	}

	for i := 1; i <= 3; i++ {
		db, err = gorm.Open(dialector, &gorm.Config{
			TranslateError: true,
			Logger:         getGormLogger(cfg),
		})
		if err == nil {
			slog.Info("Connected to database", slog.String("provider", string(cfg.Provider)))

			if onConnFn != nil {
				conn, err := db.DB()
				if err != nil {
					slog.Warn("Failed to get database connection, will retry in 3s", slog.Int("attempt", i), slog.String("provider", string(cfg.Provider)), slog.Any("error", err))
					time.Sleep(3 * time.Second)
				}
				onConnFn(conn)
			}

			return db, nil
		}

		slog.Warn("Failed to connect to database, will retry in 3s", slog.Int("attempt", i), slog.String("provider", string(cfg.Provider)), slog.Any("error", err))
		time.Sleep(3 * time.Second)
	}

	slog.Error("Failed to connect to database after 3 attempts", slog.String("provider", string(cfg.Provider)), slog.Any("error", err))

	return nil, err
}

func getGormLogger(cfg DbConfig) gormLogger.Interface {
	loggerOpts := make([]slogGorm.Option, 0, 5)
	loggerOpts = append(loggerOpts,
		slogGorm.WithSlowThreshold(200*time.Millisecond),
		slogGorm.WithErrorField("error"),
	)

	if cfg.LogLevel == "debug" {
		loggerOpts = append(loggerOpts,
			slogGorm.SetLogLevel(slogGorm.DefaultLogType, slog.LevelDebug),
			slogGorm.WithRecordNotFoundError(),
			slogGorm.WithTraceAll(),
		)

	} else {
		loggerOpts = append(loggerOpts,
			slogGorm.SetLogLevel(slogGorm.DefaultLogType, slog.LevelWarn),
			slogGorm.WithIgnoreTrace(),
		)
	}

	return slogGorm.New(loggerOpts...)
}
