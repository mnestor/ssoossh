package paging

import (
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/test/testdb"
)

// TestParse covers the defaults, the bounds, and the rejections. The
// rejections are the point: a silently-clamped garbage limit would page
// through a list without the caller ever learning their parameter was
// ignored.
func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		query     string
		wantLimit int
		wantAfter string
		wantQuery string
		wantErr   bool
	}{
		{
			name:      "should apply the defaults when nothing is supplied",
			query:     "",
			wantLimit: DefaultLimit,
		},
		{
			name:      "should read limit and after when both are supplied",
			query:     "limit=10&after=row-40",
			wantLimit: 10,
			wantAfter: "row-40",
		},
		{
			name:      "should trim surrounding whitespace from the cursor",
			query:     "after=" + url.QueryEscape("  row-40  "),
			wantLimit: DefaultLimit,
			wantAfter: "row-40",
		},
		{
			name:      "should clamp a limit above the maximum",
			query:     "limit=5000",
			wantLimit: MaxLimit,
		},
		{
			name:      "should trim surrounding whitespace from the search term",
			query:     "q=" + url.QueryEscape("  alice  "),
			wantLimit: DefaultLimit,
			wantQuery: "alice",
		},
		{
			name:      "should treat an empty parameter as absent",
			query:     "limit=&after=&q=",
			wantLimit: DefaultLimit,
		},
		{
			name:    "should reject a non-numeric limit",
			query:   "limit=twenty",
			wantErr: true,
		},
		{
			name:    "should reject a zero limit",
			query:   "limit=0",
			wantErr: true,
		},
		{
			name:    "should reject a negative limit",
			query:   "limit=-1",
			wantErr: true,
		},
		{
			name:    "should reject offset when it is supplied",
			query:   "offset=40",
			wantErr: true,
		},
		{
			name:    "should reject offset when it is supplied empty",
			query:   "offset=",
			wantErr: true,
		},
		{
			name:    "should reject offset when it is supplied alongside a cursor",
			query:   "offset=0&after=row-40",
			wantErr: true,
		},
		{
			name:    "should reject a cursor longer than any row id",
			query:   "after=" + strings.Repeat("a", MaxCursorLength+1),
			wantErr: true,
		},
		{
			name:    "should reject a search term longer than the maximum",
			query:   "q=" + strings.Repeat("a", MaxQueryLength+1),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatalf("ParseQuery(%q): %v", tt.query, err)
			}

			got, err := Parse(values)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) = %+v, want an error", tt.query, got)
				}
				// Every rejection has to render as a 400, not the 500 a bare
				// error would produce.
				var invalid *errorresponses.InvalidRequestError
				if !errors.As(err, &invalid) {
					t.Fatalf("Parse(%q) error = %T, want *errorresponses.InvalidRequestError", tt.query, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.query, err)
			}
			if got.Limit != tt.wantLimit {
				t.Errorf("Limit = %d, want %d", got.Limit, tt.wantLimit)
			}
			if got.After != tt.wantAfter {
				t.Errorf("After = %q, want %q", got.After, tt.wantAfter)
			}
			if got.Query != tt.wantQuery {
				t.Errorf("Query = %q, want %q", got.Query, tt.wantQuery)
			}
		})
	}
}

// TestFilter covers the SQL fragment, the wildcard escaping, and the empty
// cases. The escaping is what stops a user typing "%" into a search box from
// matching every row.
func TestFilter(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		columns  []string
		wantSQL  string
		wantArgs []any
	}{
		{
			name:    "should return nothing for an empty query",
			query:   "",
			columns: []string{"username"},
		},
		{
			name:    "should return nothing when no columns are named",
			query:   "alice",
			columns: nil,
		},
		{
			name:     "should match a single column case-insensitively",
			query:    "AlIcE",
			columns:  []string{"username"},
			wantSQL:  `LOWER(username) LIKE ? ESCAPE '\'`,
			wantArgs: []any{"%alice%"},
		},
		{
			name:     "should OR every named column together",
			query:    "ali",
			columns:  []string{"username", "email"},
			wantSQL:  `(LOWER(username) LIKE ? ESCAPE '\' OR LOWER(email) LIKE ? ESCAPE '\')`,
			wantArgs: []any{"%ali%", "%ali%"},
		},
		{
			name:     "should escape a percent so it matches literally",
			query:    "50%",
			columns:  []string{"username"},
			wantSQL:  `LOWER(username) LIKE ? ESCAPE '\'`,
			wantArgs: []any{`%50\%%`},
		},
		{
			name:     "should escape an underscore so it matches literally",
			query:    "svc_deploy",
			columns:  []string{"username"},
			wantSQL:  `LOWER(username) LIKE ? ESCAPE '\'`,
			wantArgs: []any{`%svc\_deploy%`},
		},
		{
			name:     "should escape the escape character itself",
			query:    `a\b`,
			columns:  []string{"username"},
			wantSQL:  `LOWER(username) LIKE ? ESCAPE '\'`,
			wantArgs: []any{`%a\\b%`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSQL, gotArgs := Filter(tt.query, tt.columns...)
			if gotSQL != tt.wantSQL {
				t.Errorf("sql = %q, want %q", gotSQL, tt.wantSQL)
			}
			if len(gotArgs) != len(tt.wantArgs) {
				t.Fatalf("args = %v, want %v", gotArgs, tt.wantArgs)
			}
			for i := range gotArgs {
				if gotArgs[i] != tt.wantArgs[i] {
					t.Errorf("args[%d] = %v, want %v", i, gotArgs[i], tt.wantArgs[i])
				}
			}
		})
	}
}

// pagingRow is a throwaway table for the query-building tests. The helpers
// under test are driver-agnostic string builders, run against both backends
// (test/testdb), which is where the LOWER()/ESCAPE choice has to hold.
type pagingRow struct {
	ID       int    `gorm:"column:id;primaryKey"`
	Username string `gorm:"column:username"`
	Email    string `gorm:"column:email"`
}

// TableName pins the table so GORM does not pluralize it.
func (pagingRow) TableName() string { return "paging_rows" }

// newPagingDB returns an isolated database holding rows, for the
// query-building tests.
func newPagingDB(t *testing.T, rows []pagingRow) *gorm.DB {
	t.Helper()

	db := testdb.Empty(t)
	if err := db.AutoMigrate(&pagingRow{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if len(rows) > 0 {
		if err := db.Create(&rows).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return db
}

// TestCount covers the total against a real database, so a malformed
// fragment fails here rather than at the first admin page load.
func TestCount(t *testing.T) {
	rows := []pagingRow{
		{ID: 1, Username: "alice", Email: "alice@corp.example"},
		{ID: 2, Username: "bob", Email: "bob@corp.example"},
		{ID: 3, Username: "carol", Email: "carol@other.example"},
		{ID: 4, Username: "svc_deploy", Email: "deploy@corp.example"},
	}

	t.Run("should count every row matching the filter, not just the page", func(t *testing.T) {
		db := newPagingDB(t, rows)

		where, args := Filter("corp", "username", "email")
		total, err := Count(db.Model(&pagingRow{}).Where(where, args...))
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if total != 3 {
			t.Fatalf("total = %d, want 3", total)
		}
	})

	t.Run("should match a literal underscore rather than any character", func(t *testing.T) {
		db := newPagingDB(t, rows)

		where, args := Filter("svc_d", "username")
		total, err := Count(db.Model(&pagingRow{}).Where(where, args...))
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		if total != 1 {
			t.Fatalf("total = %d, want 1", total)
		}
	})
}

// seekRow is a throwaway table for the keyset tests: a timestamp and a
// string id, the shape every time-ordered admin list pages over.
type seekRow struct {
	ID        string    `gorm:"column:id;primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

// TableName pins the table so GORM does not pluralize it.
func (seekRow) TableName() string { return "seek_rows" }

// TestSeekBefore pages a real table through the seek fragment, including two
// rows that share a timestamp, so the id tiebreak is what keeps them apart.
func TestSeekBefore(t *testing.T) {
	base := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	rows := []seekRow{
		{ID: "a", CreatedAt: base},
		{ID: "b", CreatedAt: base.Add(time.Minute)},
		{ID: "c", CreatedAt: base.Add(time.Minute)},
		{ID: "d", CreatedAt: base.Add(2 * time.Minute)},
	}

	tests := []struct {
		name   string
		cursor seekRow
		want   []string
	}{
		{
			name:   "should resume after the newest row when the cursor is the first row",
			cursor: rows[3],
			want:   []string{"c", "b", "a"},
		},
		{
			name:   "should keep the tied row with the smaller id when the cursor shares its timestamp",
			cursor: rows[2],
			want:   []string{"b", "a"},
		},
		{
			name:   "should return nothing when the cursor is the oldest row",
			cursor: rows[0],
			want:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := testdb.Empty(t)
			if err := db.AutoMigrate(&seekRow{}); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			if err := db.Create(&rows).Error; err != nil {
				t.Fatalf("seed: %v", err)
			}

			where, args := SeekBefore("created_at", "id", tt.cursor.CreatedAt, tt.cursor.ID)
			var got []seekRow
			if err := db.Where(where, args...).Order("created_at DESC, id DESC").Find(&got).Error; err != nil {
				t.Fatalf("find: %v", err)
			}
			ids := make([]string, 0, len(got))
			for _, r := range got {
				ids = append(ids, r.ID)
			}
			if !slices.Equal(ids, tt.want) {
				t.Errorf("ids = %v, want %v", ids, tt.want)
			}
		})
	}
}

// TestTrim covers the limit+1 convention: the extra row only signals that a
// next page exists, and is never served.
func TestTrim(t *testing.T) {
	id := func(s string) string { return s }
	tests := []struct {
		name     string
		rows     []string
		limit    int
		wantRows []string
		wantNext string
	}{
		{name: "should return no cursor when the list is empty", rows: []string{}, limit: 2, wantRows: []string{}},
		{name: "should return no cursor when the rows fit the page exactly", rows: []string{"a", "b"}, limit: 2, wantRows: []string{"a", "b"}},
		{name: "should drop the extra row and point at the last served row when there is more", rows: []string{"a", "b", "c"}, limit: 2, wantRows: []string{"a", "b"}, wantNext: "b"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, next := Trim(tt.rows, tt.limit, id)
			if !slices.Equal(got, tt.wantRows) {
				t.Errorf("rows = %v, want %v", got, tt.wantRows)
			}
			gotNext := ""
			if next != nil {
				gotNext = *next
			}
			if gotNext != tt.wantNext {
				t.Errorf("next = %q, want %q", gotNext, tt.wantNext)
			}
		})
	}
}
