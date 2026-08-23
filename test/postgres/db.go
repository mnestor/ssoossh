package postgres

import (
	"testing"

	"gorm.io/gorm"
)

// Note: Postgres container setup for E2E tests is in test/e2e/harness/postgres.go.
// This package provides schema utilities and query helpers for testing.

// Tables returns the list of user-created tables in the public schema,
// excluding system tables like schema_migrations.
func Tables(t *testing.T, db *gorm.DB) []string {
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

// ColumnInfo holds information about a column in a table.
type ColumnInfo struct {
	TableName      string
	ColumnName     string
	DataType       string
	IsNullable     bool
	ColumnDefault  *string
	ConstraintType *string
}

// ColumnDetails queries the schema and returns comprehensive information
// about columns in the given tables (or all tables if empty).
func ColumnDetails(t *testing.T, db *gorm.DB, tableNames ...string) map[string]map[string]*ColumnInfo {
	t.Helper()

	// Build the query to fetch columns and their details.
	query := `
		SELECT
			t.table_name,
			c.column_name,
			c.data_type,
			CASE WHEN c.is_nullable = 'YES' THEN true ELSE false END as is_nullable,
			c.column_default,
			tc.constraint_type
		FROM information_schema.tables t
		JOIN information_schema.columns c ON t.table_name = c.table_name AND t.table_schema = c.table_schema
		LEFT JOIN information_schema.constraint_column_usage ccu ON c.table_name = ccu.table_name
			AND c.column_name = ccu.column_name AND c.table_schema = ccu.table_schema
		LEFT JOIN information_schema.table_constraints tc ON ccu.constraint_name = tc.constraint_name
			AND tc.table_schema = c.table_schema
		WHERE t.table_schema = 'public'
		AND c.table_name NOT LIKE 'pg_%'
	`

	if len(tableNames) > 0 {
		query += " AND t.table_name = ANY($1)"
	}

	query += " ORDER BY t.table_name, c.ordinal_position"

	rows, err := db.Raw(query, tableNames).Rows()
	if err != nil {
		t.Fatalf("failed to query column details: %v", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]map[string]*ColumnInfo)
	for rows.Next() {
		var tableNa, colName, dataType string
		var isNull bool
		var colDefault *string
		var constraintType *string

		if err := rows.Scan(&tableNa, &colName, &dataType, &isNull, &colDefault, &constraintType); err != nil {
			t.Fatalf("failed to scan column row: %v", err)
		}

		if result[tableNa] == nil {
			result[tableNa] = make(map[string]*ColumnInfo)
		}

		result[tableNa][colName] = &ColumnInfo{
			TableName:      tableNa,
			ColumnName:     colName,
			DataType:       dataType,
			IsNullable:     isNull,
			ColumnDefault:  colDefault,
			ConstraintType: constraintType,
		}
	}

	return result
}

// Indexes returns the indexes on the given table.
func Indexes(t *testing.T, db *gorm.DB, tableName string) map[string][]string {
	t.Helper()
	rows, err := db.Raw(
		`SELECT indexname, indexdef FROM pg_indexes WHERE tablename = ? AND schemaname = 'public'`,
		tableName,
	).Rows()
	if err != nil {
		t.Fatalf("failed to query indexes for %s: %v", tableName, err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string][]string)
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatalf("failed to scan index row: %v", err)
		}
		result[name] = []string{def}
	}
	return result
}

// ForeignKeys returns information about foreign keys in the given table.
type ForeignKeyInfo struct {
	ConstraintName   string
	ColumnName       string
	ReferencedTable  string
	ReferencedColumn string
}

// ForeignKeys queries foreign key constraints.
func ForeignKeys(t *testing.T, db *gorm.DB, tableName string) []ForeignKeyInfo {
	t.Helper()
	rows, err := db.Raw(`
		SELECT
			tc.constraint_name,
			kcu.column_name,
			ccu.table_name AS referenced_table,
			ccu.column_name AS referenced_column
		FROM information_schema.table_constraints AS tc
		JOIN information_schema.key_column_usage AS kcu ON tc.constraint_name = kcu.constraint_name
		JOIN information_schema.constraint_column_usage AS ccu ON ccu.constraint_name = tc.constraint_name
		WHERE tc.constraint_type = 'FOREIGN KEY' AND tc.table_name = ?
	`, tableName).Rows()
	if err != nil {
		t.Fatalf("failed to query foreign keys for %s: %v", tableName, err)
	}
	defer func() { _ = rows.Close() }()

	var result []ForeignKeyInfo
	for rows.Next() {
		var fk ForeignKeyInfo
		if err := rows.Scan(&fk.ConstraintName, &fk.ColumnName, &fk.ReferencedTable, &fk.ReferencedColumn); err != nil {
			t.Fatalf("failed to scan foreign key row: %v", err)
		}
		result = append(result, fk)
	}
	return result
}

// CheckConstraints returns check constraints on the given table.
type CheckConstraint struct {
	ConstraintName string
	Expression     string
}

// CheckConstraints queries check constraints.
func CheckConstraints(t *testing.T, db *gorm.DB, tableName string) []CheckConstraint {
	t.Helper()
	rows, err := db.Raw(`
		SELECT constraint_name, check_clause
		FROM information_schema.constraint_column_usage ccu
		JOIN information_schema.table_constraints tc ON tc.constraint_name = ccu.constraint_name
		WHERE tc.constraint_type = 'CHECK' AND tc.table_name = ? AND tc.table_schema = 'public'
	`, tableName).Rows()
	if err != nil {
		t.Fatalf("failed to query check constraints for %s: %v", tableName, err)
	}
	defer func() { _ = rows.Close() }()

	var result []CheckConstraint
	for rows.Next() {
		var cc CheckConstraint
		if err := rows.Scan(&cc.ConstraintName, &cc.Expression); err != nil {
			t.Fatalf("failed to scan check constraint row: %v", err)
		}
		result = append(result, cc)
	}
	return result
}
