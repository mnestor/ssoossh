package db

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/mnestor/ssoossh/resources"
)

func migrateDatabase(driver database.Driver, migrationsPath string) error {
	source, err := iofs.New(resources.FS, migrationsPath)
	if err != nil {
		return fmt.Errorf("failed to run migrations: failed to create embedded migration source: %w", err)
	}

	// Embedded migrations via iofs
	m, err := migrate.NewWithInstance("iofs", source, "ssoossh", driver)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}

	requiredVersion, err := getRequiredMigrationVersion(migrationsPath)
	if err != nil {
		return fmt.Errorf("failed to get last migration version: %w", err)
	}

	currentVersion, _, _ := m.Version()
	if currentVersion > requiredVersion {
		return fmt.Errorf("Database version is newer than the application supports", slog.Uint64("db_version", uint64(currentVersion)), slog.Uint64("app_version", uint64(requiredVersion)))
	}

	if err := m.Migrate(requiredVersion); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to apply embedded migrations: %w", err)
	}
	return nil
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
