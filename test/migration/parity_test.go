//go:build dbparity

// Package migration_test provides comprehensive tests for database migrations
// across SQLite and Postgres, verifying that both dialects produce equivalent
// schemas and behave identically.
package migration_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/resources"
	"github.com/mnestor/ssoossh/test/postgres"
	"github.com/mnestor/ssoossh/test/sqlite"
)

// TestMigrationParity_SchemasShouldBeIdentical verifies that the Postgres and
// SQLite migrations produce tables and columns with matching names and nullability.
// This is the live counterpart to static SQL parsing tests — it catches divergences
// that the database engines interpret differently.
func TestMigrationParity_SchemasShouldBeIdentical(t *testing.T) {
	// Deliberately NOT t.Parallel(): the postgres harness binds one fixed
	// port (test/postgres/container.go), so two container-backed tests
	// running at once read each other's databases — this one saw
	// DownShouldReverseUp's mid-cycle schema, resurrected columns and
	// all. The harness comment always said sequential; this enforces it.
	ctx := t.Context()

	pgDB, _ := postgres.ConnectAndMigrate(t, ctx)
	sqliteDB := sqlite.ConnectAndMigrate(t)

	// Query schemas.
	pgTables := postgres.Tables(t, pgDB)
	sqliteTables := sqlite.Tables(t, sqliteDB)

	// Extract user tables (exclude schema_migrations).
	pgUserTables := filterSystemTables(pgTables)
	sqliteUserTables := filterSystemTables(sqliteTables)

	// Verify table sets match.
	pgTableSet := toSet(pgUserTables)
	sqliteTableSet := toSet(sqliteUserTables)

	if !mapsEqual(pgTableSet, sqliteTableSet) {
		t.Errorf("table sets differ:\n  postgres: %v\n  sqlite:   %v",
			sortedKeys(pgTableSet), sortedKeys(sqliteTableSet))
	}

	// For each table, verify columns match (both existence and nullability).
	pgColumns := postgres.ColumnDetails(t, pgDB, pgUserTables...)
	sqliteColumns := sqlite.ColumnDetails(t, sqliteDB, sqliteUserTables...)

	for tableName := range pgTableSet {
		pgCols := pgColumns[tableName]
		sqliteCols := sqliteColumns[tableName]

		if len(pgCols) == 0 && len(sqliteCols) == 0 {
			continue
		}

		// Build column name sets.
		pgColNames := make(map[string]bool)
		for colName := range pgCols {
			pgColNames[colName] = true
		}
		sqliteColNames := make(map[string]bool)
		for colName := range sqliteCols {
			sqliteColNames[colName] = true
		}

		if !mapsEqual(pgColNames, sqliteColNames) {
			t.Errorf("%s: column names differ:\n  postgres: %v\n  sqlite:   %v",
				tableName, sortedKeys(pgColNames), sortedKeys(sqliteColNames))
			continue
		}

		// Check nullability for each column.
		for colName := range pgColNames {
			pgInfo := pgCols[colName]
			sqliteInfo := sqliteCols[colName]

			if pgInfo == nil || sqliteInfo == nil {
				continue
			}

			if pgInfo.IsNullable != sqliteInfo.IsNullable {
				t.Errorf("%s.%s: nullability differs (postgres=%v, sqlite=%v)",
					tableName, colName, pgInfo.IsNullable, sqliteInfo.IsNullable)
			}
		}
	}
}

// TestMigrationParity_DownShouldReverseUp verifies that running down migrations
// after up migrations returns to a clean state, and that a subsequent up reaches
// the same schema as the first up.
func TestMigrationParity_DownShouldReverseUp(t *testing.T) {
	// Not t.Parallel() — see TestMigrationParity_SchemasShouldBeIdentical.
	ctx := t.Context()

	pgDB, _ := postgres.ConnectAndMigrate(t, ctx)

	// Get the tables after up.
	upTables := postgres.Tables(t, pgDB)
	if len(upTables) == 0 {
		t.Fatal("no tables after up migration")
	}

	// Run down migrations.
	if err := postgres.RunDown(t, ctx, pgDB); err != nil {
		t.Fatalf("down migration failed: %v", err)
	}

	// Verify only schema_migrations remains.
	downTables := postgres.Tables(t, pgDB)
	for _, tbl := range downTables {
		if tbl != "schema_migrations" {
			t.Errorf("table %q survived down migration (expected only schema_migrations)", tbl)
		}
	}

	// Run up again.
	if err := postgres.RunUp(t, ctx, pgDB); err != nil {
		t.Fatalf("re-applying up migration failed: %v", err)
	}

	// Verify tables match the first up.
	reupTables := postgres.Tables(t, pgDB)
	if strings.Join(reupTables, ",") != strings.Join(upTables, ",") {
		t.Errorf("table set after down+up differs:\n  first up: %v\n  after down+up: %v",
			upTables, reupTables)
	}
}

// TestMigrationParity_MigrationFilesShouldHaveDualDialects verifies that for
// every .up.sql file in one dialect, there is a corresponding .up.sql file in
// the other dialect with the same migration number.
func TestMigrationParity_MigrationFilesShouldHaveDualDialects(t *testing.T) {
	// Parse the embedded migration file lists.
	pgUPFiles := listMigrationFiles(t, "migrations/postgres", "up.sql")
	sqliteUPFiles := listMigrationFiles(t, "migrations/sqlite", "up.sql")

	// Verify the sets match.
	if strings.Join(pgUPFiles, ",") != strings.Join(sqliteUPFiles, ",") {
		t.Errorf("migration file sets differ:\n  postgres: %v\n  sqlite:   %v",
			pgUPFiles, sqliteUPFiles)
	}

	pgDownFiles := listMigrationFiles(t, "migrations/postgres", "down.sql")
	sqliteDownFiles := listMigrationFiles(t, "migrations/sqlite", "down.sql")

	if strings.Join(pgDownFiles, ",") != strings.Join(sqliteDownFiles, ",") {
		t.Errorf("down migration file sets differ:\n  postgres: %v\n  sqlite:   %v",
			pgDownFiles, sqliteDownFiles)
	}
}

// Helper functions

func filterSystemTables(tables []string) []string {
	var result []string
	for _, t := range tables {
		if t != "schema_migrations" && !strings.HasPrefix(t, "sqlite_") {
			result = append(result, t)
		}
	}
	return result
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool)
	for _, item := range items {
		s[item] = true
	}
	return s
}

func mapsEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]bool) []string {
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func listMigrationFiles(t *testing.T, dir, suffix string) []string {
	t.Helper()
	entries, err := resources.FS.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read migration dir %s: %v", dir, err)
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), suffix) {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	return files
}
