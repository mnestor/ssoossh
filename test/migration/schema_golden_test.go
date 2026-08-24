package migration_test

// Test methodology: apply the migrations to a real database, dump the
// resulting schema in a normalized, order-independent form, and diff it
// against a checked-in golden.
//
// The goldens exist so the migration set can be reshaped — collapsed,
// split, reordered — without anyone having to eyeball SQL to decide whether
// the schema that comes out is the same one. They record the destination,
// not the route: a migration that is rewritten but lands on the same tables,
// columns, nullability, indexes, foreign keys, and CHECK constraints leaves
// them untouched.
//
// Run `go test ./test/migration/ -update` to accept an intended change (add
// `-tags dbparity` to refresh the Postgres golden too, which needs docker).

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/sqlite"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files instead of comparing against them")

// assertSchemaGolden compares got against testdata/<name>, or rewrites it
// under -update.
func assertSchemaGolden(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatalf("failed to create testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("failed to write %s: %v", path, err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read %s (run `go test ./test/migration/ -update` to create it): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("migrated schema changed.\n--- want (%s)\n%s\n--- got\n%s\n\n"+
			"Reshaping migrations must not reach this golden. If the schema change is "+
			"intended, run `go test ./test/migration/ -update`.", path, want, got)
	}
}

// userTables drops the migration bookkeeping table and SQLite's internals,
// which say nothing about the schema the migrations build.
func userTables(tables []string) []string {
	var kept []string
	for _, name := range tables {
		if name == "schema_migrations" || strings.HasPrefix(name, "sqlite_") {
			continue
		}
		kept = append(kept, name)
	}
	sort.Strings(kept)
	return kept
}

// should keep the SQLite schema the migrations build byte-for-byte stable.
func TestMigrations_SQLiteSchemaShouldMatchGolden(t *testing.T) {
	t.Parallel()

	db := sqlite.ConnectAndMigrate(t)

	tables := userTables(sqlite.Tables(t, db))
	columns := sqlite.ColumnDetails(t, db, tables...)

	var b strings.Builder
	for _, table := range tables {
		fmt.Fprintf(&b, "table %s\n", table)

		var names []string
		for name := range columns[table] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			c := columns[table][name]
			fmt.Fprintf(&b, "  column %s type=%s nullable=%t pk=%t default=%s\n",
				c.ColumnName, c.DataType, c.IsNullable, c.IsPrimaryKey, deref(c.DefaultValue))
		}

		var lines []string
		for _, idx := range sqlite.Indexes(t, db, table) {
			lines = append(lines, fmt.Sprintf("  index %s unique=%t columns=%s",
				idx.IndexName, idx.Unique, strings.Join(idx.Columns, ",")))
		}
		for _, fk := range sqlite.ForeignKeys(t, db, table) {
			lines = append(lines, fmt.Sprintf("  fk %s -> %s.%s on_delete=%s on_update=%s",
				fk.ColumnName, fk.ReferencedTable, fk.ReferencedColumn, fk.OnDelete, fk.OnUpdate))
		}
		for _, chk := range sqlite.CheckConstraints(t, db, table) {
			lines = append(lines, "  check "+normalizeSpace(chk.SQL))
		}
		sort.Strings(lines)
		b.WriteString(strings.Join(lines, "\n"))
		if len(lines) > 0 {
			b.WriteString("\n")
		}
	}

	assertSchemaGolden(t, "schema_sqlite.golden", b.String())
}

func deref(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}

// normalizeSpace collapses runs of whitespace, so reindenting a CREATE TABLE
// does not read as a changed constraint.
func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// should leave nothing behind on the way down, and rebuild the same schema
// on the way back up. The SQLite counterpart of
// TestMigrationParity_DownShouldReverseUp, which only ever exercised
// Postgres — so the SQLite down migration went unrun by any test, including
// through the collapse that rewrote it.
func TestMigrations_SQLiteDownShouldReverseUp(t *testing.T) {
	t.Parallel()

	db := sqlite.ConnectAndMigrate(t)

	upTables := userTables(sqlite.Tables(t, db))
	if len(upTables) == 0 {
		t.Fatal("no tables after the up migration")
	}

	if err := sqlite.RunDown(t, db); err != nil {
		t.Fatalf("down migration failed: %v", err)
	}
	if left := userTables(sqlite.Tables(t, db)); len(left) != 0 {
		t.Errorf("tables survived the down migration: %v", left)
	}

	if err := sqlite.RunUp(t, db); err != nil {
		t.Fatalf("re-applying the up migration failed: %v", err)
	}
	if got := userTables(sqlite.Tables(t, db)); strings.Join(got, ",") != strings.Join(upTables, ",") {
		t.Errorf("table set after down+up differs:\n  first up:      %v\n  after down+up: %v", upTables, got)
	}
}
