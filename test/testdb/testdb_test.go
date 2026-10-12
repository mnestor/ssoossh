package testdb

import (
	"strings"
	"testing"

	"gorm.io/gorm"
)

// Tests for the selection logic run as plain unit tests on either leg; the
// tests that open databases exercise whichever backend the run selected, so
// the Postgres leg covers the template and CREATE DATABASE paths.

func TestSelectedBackend(t *testing.T) {
	tests := []struct {
		name    string
		backend string
		dsn     string
		want    string
		wantErr string
	}{
		{name: "should default to sqlite when nothing is set", want: SQLite},
		{name: "should pick postgres when only the DSN is set", dsn: "postgres://x@h/db", want: Postgres},
		{name: "should honour an explicit sqlite even with a DSN set", backend: "sqlite", dsn: "postgres://x@h/db", want: SQLite},
		{name: "should pick postgres when named with a DSN", backend: "postgres", dsn: "postgres://x@h/db", want: Postgres},
		{name: "should ignore case and surrounding space", backend: " Postgres ", dsn: "postgres://x@h/db", want: Postgres},
		{name: "should fail when postgres is named without a DSN", backend: "postgres", wantErr: EnvPostgresDSN + " is not set"},
		{name: "should fail on an unknown backend", backend: "mysql", wantErr: `"mysql"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvBackend, tt.backend)
			t.Setenv(EnvPostgresDSN, tt.dsn)

			got, err := selectedBackend()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("selectedBackend() error = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectedBackend() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("selectedBackend() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWithDatabase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		dsn     string
		want    string
		wantErr bool
	}{
		{
			name: "should swap the database and keep credentials and parameters",
			dsn:  "postgres://u:p@127.0.0.1:5432/postgres?sslmode=disable",
			want: "postgres://u:p@127.0.0.1:5432/t_1?sslmode=disable",
		},
		{
			name: "should add a database when the DSN names none",
			dsn:  "postgresql://u@db",
			want: "postgresql://u@db/t_1",
		},
		{name: "should refuse a DSN that is not a URL", dsn: "host=db user=u", wantErr: true},
		{name: "should refuse an unparsable DSN", dsn: "postgres://u@[::1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := withDatabase(tt.dsn, "t_1")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("withDatabase(%q) = %q, want an error", tt.dsn, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("withDatabase(%q) error = %v", tt.dsn, err)
			}
			if got != tt.want {
				t.Errorf("withDatabase(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}

func TestMigrationsHash(t *testing.T) {
	t.Parallel()

	t.Run("should be stable across calls", func(t *testing.T) {
		t.Parallel()
		first, err := migrationsHash("migrations/postgres")
		if err != nil {
			t.Fatalf("migrationsHash: %v", err)
		}
		second, err := migrationsHash("migrations/postgres")
		if err != nil {
			t.Fatalf("migrationsHash: %v", err)
		}
		if first != second || len(first) != 16 {
			t.Errorf("hashes %q and %q, want two equal 16-character digests", first, second)
		}
	})

	t.Run("should differ between migration sets", func(t *testing.T) {
		t.Parallel()
		pg, err := migrationsHash("migrations/postgres")
		if err != nil {
			t.Fatalf("migrationsHash: %v", err)
		}
		sq, err := migrationsHash("migrations/sqlite")
		if err != nil {
			t.Fatalf("migrationsHash: %v", err)
		}
		if pg == sq {
			t.Errorf("both sets hash to %q", pg)
		}
	})

	t.Run("should fail on a directory that does not exist", func(t *testing.T) {
		t.Parallel()
		if _, err := migrationsHash("migrations/nope"); err == nil {
			t.Error("migrationsHash on a missing directory returned no error")
		}
	})
}

func TestRandomName(t *testing.T) {
	t.Parallel()

	a, err := randomName("t_")
	if err != nil {
		t.Fatalf("randomName: %v", err)
	}
	b, err := randomName("t_")
	if err != nil {
		t.Fatalf("randomName: %v", err)
	}
	if a == b || !strings.HasPrefix(a, "t_") || len(a) != len("t_")+16 {
		t.Errorf("randomName gave %q and %q, want two distinct t_ names with 16 hex characters", a, b)
	}
}

// countUsers counts the users table, which exists only once migrated.
func countUsers(db *gorm.DB) (int64, error) {
	var n int64
	err := db.Raw("SELECT COUNT(*) FROM users").Scan(&n).Error
	return n, err
}

// orphanLDAP inserts a user_ldap row for the user id it is given; the row
// is otherwise complete, so only the foreign key can refuse it.
const orphanLDAP = `INSERT INTO user_ldap (user_id, dn, created_at, updated_at)
	VALUES (?, 'uid=ghost', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`

// seedUser writes one users row.
func seedUser(t *testing.T, db *gorm.DB, id string) {
	t.Helper()
	err := db.Exec(`INSERT INTO users (id, subject, username, created_at, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, id, "sub-"+id, id).Error
	if err != nil {
		t.Fatalf("seed user %s: %v", id, err)
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("should return a migrated database", func(t *testing.T) {
		t.Parallel()
		if _, err := countUsers(New(t)); err != nil {
			t.Errorf("users table missing from a New database: %v", err)
		}
	})

	t.Run("should isolate one test's rows from another's", func(t *testing.T) {
		t.Parallel()
		a, b := New(t), New(t)
		seedUser(t, a, "only-in-a")

		n, err := countUsers(b)
		if err != nil {
			t.Fatalf("count users: %v", err)
		}
		if n != 0 {
			t.Errorf("second database holds %d users, want 0", n)
		}
	})

	t.Run("should enforce foreign keys as production does", func(t *testing.T) {
		t.Parallel()
		db := New(t)
		seedUser(t, db, "somebody")
		// The same row is accepted for a user that exists, so the refusal
		// below is the foreign key and nothing else.
		if err := db.Exec(orphanLDAP, "somebody").Error; err != nil {
			t.Fatalf("a valid user_ldap row was refused: %v", err)
		}
		if err := db.Exec(orphanLDAP, "nobody").Error; err == nil {
			t.Error("an orphan user_ldap row was accepted")
		}
	})
}

func TestEmpty(t *testing.T) {
	t.Parallel()

	if _, err := countUsers(Empty(t)); err == nil {
		t.Error("an Empty database has a users table, want no schema")
	}
}

func TestConnection(t *testing.T) {
	t.Parallel()

	provider, connection := Connection(t)
	switch {
	case IsPostgres(t) && (provider != Postgres || !strings.HasPrefix(connection, "postgres")):
		t.Errorf("Connection() = %q, %q, want a postgres DSN", provider, connection)
	case !IsPostgres(t) && (provider != SQLite || connection != ":memory:"):
		t.Errorf("Connection() = %q, %q, want sqlite :memory:", provider, connection)
	}
}

func TestSkips(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		skip     func(testing.TB, string)
		skipOnPG bool
	}{
		{name: "should skip on postgres only when asked to", skip: SkipOnPostgres, skipOnPG: true},
		{name: "should skip on sqlite only when asked to", skip: SkipOnSQLite, skipOnPG: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ran := false
			t.Run("inner", func(t *testing.T) {
				tt.skip(t, "testing the skip")
				ran = true
			})
			wantRan := IsPostgres(t) != tt.skipOnPG
			if ran != wantRan {
				t.Errorf("body ran = %v, want %v on this backend", ran, wantRan)
			}
		})
	}
}

func TestWithoutForeignKeys(t *testing.T) {
	t.Parallel()

	t.Run("should admit an orphan row inside fn", func(t *testing.T) {
		t.Parallel()
		db := New(t)
		WithoutForeignKeys(t, db, func(tx *gorm.DB) error { return tx.Exec(orphanLDAP, "ghost").Error })

		var n int64
		if err := db.Raw("SELECT COUNT(*) FROM user_ldap").Scan(&n).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		if n != 1 {
			t.Errorf("user_ldap holds %d rows, want the 1 orphan", n)
		}
	})

	t.Run("should enforce foreign keys again afterwards", func(t *testing.T) {
		t.Parallel()
		db := New(t)
		WithoutForeignKeys(t, db, func(tx *gorm.DB) error { return nil })

		if err := db.Exec(orphanLDAP, "ghost").Error; err == nil {
			t.Error("an orphan was accepted after WithoutForeignKeys returned")
		}
	})
}
