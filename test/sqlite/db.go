// Package sqlite provides test infrastructure for running tests against
// SQLite databases.
package sqlite

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/golang-migrate/migrate/v4"
	sqliteMigrate "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/resources"
)

// ConnectAndMigrate opens and migrates a fresh in-memory SQLite database,
// following the same pattern as the existing db_livepg_test.go tests.
func ConnectAndMigrate(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:?cache=shared"))
	if err != nil {
		t.Fatalf("failed to open SQLite: %v", err)
	}

	// Run migrations.
	if err := migrateSQLite(t, db); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	// Register cleanup.
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err != nil {
			return
		}
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	return db
}

// RunUp applies all pending up migrations to the database. ConnectAndMigrate
// already does this; RunUp exists for tests that migrate a second time, such
// as an up/down/up round-trip.
func RunUp(t *testing.T, db *gorm.DB) error {
	t.Helper()
	return doMigrate(db, func(m *migrate.Migrate) error { return m.Up() })
}

// RunDown applies all down migrations to the database, the SQLite
// counterpart of test/postgres.RunDown.
func RunDown(t *testing.T, db *gorm.DB) error {
	t.Helper()
	return doMigrate(db, func(m *migrate.Migrate) error { return m.Down() })
}

// RunTo migrates to a specific version, in whichever direction that is from
// where the database currently stands. version is a migration filename's
// numeric prefix.
//
// It exists for testing a migration's data handling rather than its schema:
// step back to the version before one, write rows as the old schema had
// them, then RunUp and read back what the migration made of them.
func RunTo(t *testing.T, db *gorm.DB, version uint) error {
	t.Helper()
	return doMigrate(db, func(m *migrate.Migrate) error { return m.Migrate(version) })
}

// migrateSQLite applies the embedded SQLite migrations to the database.
func migrateSQLite(t *testing.T, db *gorm.DB) error {
	t.Helper()
	return doMigrate(db, func(m *migrate.Migrate) error { return m.Up() })
}

// doMigrate builds a migrator over db's pool and applies fn.
func doMigrate(db *gorm.DB, fn func(*migrate.Migrate) error) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get sql.DB: %w", err)
	}

	// NoTxWrap matches production (server/bootstrap/db.go): the migration
	// SQL carries its own BEGIN/COMMIT, so the driver wrapping it in a
	// second transaction fails with "cannot start a transaction within a
	// transaction".
	driver, err := sqliteMigrate.WithInstance(sqlDB, &sqliteMigrate.Config{NoTxWrap: true})
	if err != nil {
		return fmt.Errorf("failed to create migration driver: %w", err)
	}

	source, err := iofs.New(resources.FS, "migrations/sqlite")
	if err != nil {
		return fmt.Errorf("failed to create migration source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "ssoossh", driver)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}

	if err := fn(m); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	return nil
}

// Tables returns the list of user-created tables, excluding system tables.
func Tables(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var tables []string
	err := db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name").
		Scan(&tables).Error
	if err != nil {
		t.Fatalf("failed to list tables: %v", err)
	}
	return tables
}

// ColumnInfo holds information about a column in a table.
type ColumnInfo struct {
	TableName    string
	ColumnName   string
	DataType     string
	IsNullable   bool
	DefaultValue *string
	IsPrimaryKey bool
}

// ColumnDetails queries the schema and returns comprehensive information
// about columns in the given tables (or all tables if empty).
func ColumnDetails(t *testing.T, db *gorm.DB, tableNames ...string) map[string]map[string]*ColumnInfo {
	t.Helper()

	result := make(map[string]map[string]*ColumnInfo)

	// Determine which tables to query.
	var tables []string
	if len(tableNames) > 0 {
		tables = tableNames
	} else {
		tables = Tables(t, db)
	}

	// For each table, query its pragma_table_info.
	for _, tableName := range tables {
		rows, err := db.Raw("SELECT name, type, \"notnull\", dflt_value, pk FROM pragma_table_info(?)", tableName).
			Rows()
		if err != nil {
			t.Fatalf("failed to query columns for %s: %v", tableName, err)
		}

		result[tableName] = make(map[string]*ColumnInfo)

		for rows.Next() {
			var colName, dataType string
			var notNull int
			var dfltValue *string
			var pk int

			if err := rows.Scan(&colName, &dataType, &notNull, &dfltValue, &pk); err != nil {
				t.Fatalf("failed to scan column row: %v", err)
			}

			result[tableName][colName] = &ColumnInfo{
				TableName:    tableName,
				ColumnName:   colName,
				DataType:     dataType,
				IsNullable:   notNull == 0, // SQLite: notnull=1 means NOT NULL
				DefaultValue: dfltValue,
				IsPrimaryKey: pk > 0,
			}
		}
		_ = rows.Close()
	}

	return result
}

// Indexes returns information about indexes on a table.
type IndexInfo struct {
	IndexName string
	Unique    bool
	Columns   []string
}

// quoteIdent renders name as a quoted SQLite identifier. PRAGMA arguments
// are parsed as part of the statement and cannot be bound, so the three
// PRAGMA helpers below have to interpolate — the doubled quote keeps that
// from being an injection point even though every caller passes a name read
// back out of the schema.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Indexes queries index information for a table.
func Indexes(t *testing.T, db *gorm.DB, tableName string) []IndexInfo {
	t.Helper()

	// Two phases on purpose: index_list is drained fully before any
	// index_info runs. Issuing the second PRAGMA while the first result set
	// is still open takes a different connection out of the pool, and the
	// nested query came back empty every time — which is why every index
	// used to report no columns at all.
	rows, err := db.Raw("PRAGMA index_list(" + quoteIdent(tableName) + ")").Rows()
	if err != nil {
		t.Fatalf("failed to query indexes for %s: %v", tableName, err)
	}

	var indexes []IndexInfo
	for rows.Next() {
		// PRAGMA index_list yields (seq, name, unique, origin, partial).
		// origin is text ('c', 'u', 'pk') and partial is an integer; the
		// two were previously read in the wrong order and wrong types,
		// which the bind-parameter bug above kept from ever surfacing.
		var seq, unique, partial int
		var name, origin string

		if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			_ = rows.Close()
			t.Fatalf("failed to scan index row: %v", err)
		}
		indexes = append(indexes, IndexInfo{IndexName: name, Unique: unique != 0})
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("failed to close index rows for %s: %v", tableName, err)
	}

	for i := range indexes {
		indexes[i].Columns = indexColumns(t, db, indexes[i].IndexName)
	}
	return indexes
}

// indexColumns returns the column names an index covers, in index order.
func indexColumns(t *testing.T, db *gorm.DB, indexName string) []string {
	t.Helper()

	rows, err := db.Raw("PRAGMA index_info(" + quoteIdent(indexName) + ")").Rows()
	if err != nil {
		t.Fatalf("failed to query index columns for %s: %v", indexName, err)
	}
	defer func() { _ = rows.Close() }()

	var columns []string
	for rows.Next() {
		var seqno, cid int
		var colName *string

		if err := rows.Scan(&seqno, &cid, &colName); err != nil {
			t.Fatalf("failed to scan index column row: %v", err)
		}
		if colName != nil {
			columns = append(columns, *colName)
		}
	}
	return columns
}

// ForeignKeyInfo holds information about a foreign key constraint.
type ForeignKeyInfo struct {
	ID               int
	Sequence         int
	ReferencedTable  string
	ColumnName       string
	ReferencedColumn string
	OnDelete         string
	OnUpdate         string
	Match            string
}

// ForeignKeys queries foreign key constraints for a table.
func ForeignKeys(t *testing.T, db *gorm.DB, tableName string) []ForeignKeyInfo {
	t.Helper()
	rows, err := db.Raw("PRAGMA foreign_key_list(" + quoteIdent(tableName) + ")").Rows()
	if err != nil {
		t.Fatalf("failed to query foreign keys for %s: %v", tableName, err)
	}
	defer func() { _ = rows.Close() }()

	var fks []ForeignKeyInfo
	for rows.Next() {
		var fk ForeignKeyInfo
		if err := rows.Scan(&fk.ID, &fk.Sequence, &fk.ReferencedTable, &fk.ColumnName,
			&fk.ReferencedColumn, &fk.OnDelete, &fk.OnUpdate, &fk.Match); err != nil {
			t.Fatalf("failed to scan foreign key row: %v", err)
		}
		fks = append(fks, fk)
	}

	return fks
}

// CheckConstraints returns check constraint definitions for a table.
type CheckConstraint struct {
	SQL string
}

// CheckConstraints queries check constraints by parsing the table's SQL definition.
func CheckConstraints(t *testing.T, db *gorm.DB, tableName string) []CheckConstraint {
	t.Helper()
	var sql *string
	err := db.Raw("SELECT sql FROM sqlite_master WHERE type='table' AND name=?", tableName).
		Scan(&sql).Error
	if err != nil {
		t.Fatalf("failed to query table SQL for %s: %v", tableName, err)
	}

	if sql == nil {
		return nil
	}

	// Extract CHECK constraints from the CREATE TABLE statement.
	// This is a simple regex-free approach that looks for "CHECK (" patterns.
	var checks []CheckConstraint
	lines := strings.Split(*sql, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "CHECK") {
			// Extract the constraint expression.
			if idx := strings.Index(trimmed, "CHECK"); idx != -1 {
				expr := strings.TrimSpace(trimmed[idx:])
				// Remove trailing comma if present.
				expr = strings.TrimSuffix(expr, ",")
				checks = append(checks, CheckConstraint{SQL: expr})
			}
		}
	}

	return checks
}
