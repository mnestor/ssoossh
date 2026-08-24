//go:build dbparity

package migration_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/postgres"
)

// should keep the Postgres schema the migrations build stable, the same way
// TestMigrations_SQLiteSchemaShouldMatchGolden does for SQLite. Separate
// goldens rather than one shared dump: the two dialects legitimately differ
// in type names and constraint naming, and the cross-dialect comparison that
// matters is already TestMigrationParity_SchemasShouldBeIdentical's job.
func TestMigrations_PostgresSchemaShouldMatchGolden(t *testing.T) {
	// Not t.Parallel() — see TestMigrationParity_SchemasShouldBeIdentical.
	ctx := t.Context()

	db, _ := postgres.ConnectAndMigrate(t, ctx)

	tables := userTables(postgres.Tables(t, db))
	columns := postgres.ColumnDetails(t, db, tables...)

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
			// ConstraintType is deliberately not dumped: the view it comes
			// from yields one row per constraint the column takes part in
			// and ColumnDetails keeps whichever arrived last. Primary keys
			// and foreign keys are recorded below from sources that do not
			// have that ambiguity.
			fmt.Fprintf(&b, "  column %s type=%s nullable=%t default=%s\n",
				c.ColumnName, c.DataType, c.IsNullable, deref(c.ColumnDefault))
		}

		var lines []string
		for name, defs := range postgres.Indexes(t, db, table) {
			lines = append(lines, fmt.Sprintf("  index %s %s", name, normalizeSpace(strings.Join(defs, " "))))
		}
		for _, fk := range postgres.ForeignKeys(t, db, table) {
			lines = append(lines, fmt.Sprintf("  fk %s -> %s.%s",
				fk.ColumnName, fk.ReferencedTable, fk.ReferencedColumn))
		}
		for _, chk := range postgres.CheckConstraints(t, db, table) {
			lines = append(lines, fmt.Sprintf("  check %s %s", chk.ConstraintName, normalizeSpace(chk.Expression)))
		}
		sort.Strings(lines)
		b.WriteString(strings.Join(lines, "\n"))
		if len(lines) > 0 {
			b.WriteString("\n")
		}
	}

	assertSchemaGolden(t, "schema_postgres.golden", b.String())
}
