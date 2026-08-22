package postgres

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	postgresMigrate "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/resources"
)

// RunUp applies all pending up migrations to the database.
func RunUp(t *testing.T, ctx context.Context, pgc *Container, db *gorm.DB) error {
	t.Helper()
	return doMigrate(ctx, db, func(m *migrate.Migrate) error {
		return m.Up()
	})
}

// RunDown applies all down migrations to the database.
func RunDown(t *testing.T, ctx context.Context, pgc *Container, db *gorm.DB) error {
	t.Helper()
	return doMigrate(ctx, db, func(m *migrate.Migrate) error {
		return m.Down()
	})
}

// doMigrate is a helper that sets up a migrator and applies fn.
func doMigrate(ctx context.Context, db *gorm.DB, fn func(*migrate.Migrate) error) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}

	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("failed to get connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	driver, err := postgresMigrate.WithConnection(ctx, conn, &postgresMigrate.Config{})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	source, err := iofs.New(resources.FS, "migrations/postgres")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "ssoossh", driver)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	if err := fn(m); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to apply migration: %w", err)
	}

	return nil
}
