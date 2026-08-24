package paging

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// TestParse covers the defaults, the bounds, and the rejections. The
// rejections are the point: a silently-clamped garbage limit would page
// through a list without the caller ever learning their parameter was
// ignored.
func TestParse(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantLimit  int
		wantOffset int
		wantQuery  string
		wantErr    bool
	}{
		{
			name:      "should apply the defaults when nothing is supplied",
			query:     "",
			wantLimit: DefaultLimit,
		},
		{
			name:       "should read limit and offset when both are supplied",
			query:      "limit=10&offset=40",
			wantLimit:  10,
			wantOffset: 40,
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
			query:     "limit=&offset=&q=",
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
			name:    "should reject a non-numeric offset",
			query:   "offset=far",
			wantErr: true,
		},
		{
			name:    "should reject a negative offset",
			query:   "offset=-1",
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
			if got.Offset != tt.wantOffset {
				t.Errorf("Offset = %d, want %d", got.Offset, tt.wantOffset)
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

// TestParamsPageNumber covers the page arithmetic the UI renders, including
// the boundary an exact multiple of the page size produces.
func TestParamsPageNumber(t *testing.T) {
	tests := []struct {
		name      string
		params    Params
		total     int64
		wantPage  int
		wantPages int
	}{
		{
			name:      "should report page one of one when the list is empty",
			params:    Params{Limit: 25},
			total:     0,
			wantPage:  1,
			wantPages: 1,
		},
		{
			name:      "should report page one when the offset is zero",
			params:    Params{Limit: 25},
			total:     60,
			wantPage:  1,
			wantPages: 3,
		},
		{
			name:      "should report the page the offset lands on",
			params:    Params{Limit: 25, Offset: 50},
			total:     60,
			wantPage:  3,
			wantPages: 3,
		},
		{
			name:      "should not round an exact multiple up to an empty page",
			params:    Params{Limit: 25},
			total:     50,
			wantPage:  1,
			wantPages: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.params.PageNumber(); got != tt.wantPage {
				t.Errorf("PageNumber() = %d, want %d", got, tt.wantPage)
			}
			if got := tt.params.PageCount(tt.total); got != tt.wantPages {
				t.Errorf("PageCount(%d) = %d, want %d", tt.total, got, tt.wantPages)
			}
		})
	}
}

// pagingRow is a throwaway table for the query-building tests. The helpers
// under test are driver-agnostic string builders, so proving them against
// SQLite proves the SQL is well-formed; the Postgres parity the LOWER()/
// ESCAPE choice exists for is covered by test/migration.
type pagingRow struct {
	ID       int    `gorm:"column:id;primaryKey"`
	Username string `gorm:"column:username"`
	Email    string `gorm:"column:email"`
}

// TableName pins the table so GORM does not pluralize it.
func (pagingRow) TableName() string { return "paging_rows" }

// newPagingDB returns an in-memory database holding rows, for the
// query-building tests.
func newPagingDB(t *testing.T, rows []pagingRow) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
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

// TestApplyAndCount covers the window and the total against a real database,
// so a malformed fragment fails here rather than at the first admin page
// load.
func TestApplyAndCount(t *testing.T) {
	rows := []pagingRow{
		{ID: 1, Username: "alice", Email: "alice@corp.example"},
		{ID: 2, Username: "bob", Email: "bob@corp.example"},
		{ID: 3, Username: "carol", Email: "carol@other.example"},
		{ID: 4, Username: "svc_deploy", Email: "deploy@corp.example"},
	}

	t.Run("should return only the requested window", func(t *testing.T) {
		db := newPagingDB(t, rows)

		var got []pagingRow
		err := Apply(db.Model(&pagingRow{}).Order("id"), Params{Limit: 2, Offset: 1}).Find(&got).Error
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if len(got) != 2 || got[0].Username != "bob" || got[1].Username != "carol" {
			t.Fatalf("got %+v, want bob and carol", got)
		}
	})

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
