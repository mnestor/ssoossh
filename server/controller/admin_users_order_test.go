package controller

// Tests for the order the admin user directory lists people in. Pages are
// keyset-based, so the order has to be total: every row has exactly one
// place, and the seek resumes after the cursor user's place in it. The cases
// run on both backends (test/testdb), so both are held to the same answers.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/test/testdb"
)

// userOrderCase seeds a directory and states the user ids it must list, in
// order, for the given URL query.
type userOrderCase struct {
	name  string
	users []model.User
	query string
	want  []string
}

// userOrderCases returns the ordering cases. Each one isolates a single rule:
// name precedence, case folding, the username and id tiebreakers, where
// nameless users go, and the same order under a search or a status filter.
func userOrderCases() []userOrderCase {
	disabledAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return []userOrderCase{
		{
			name:  "should list nothing when there are no users",
			query: "limit=100",
			want:  nil,
		},
		{
			name: "should order by display name ignoring case when names differ only in case",
			users: []model.User{
				{ID: "u-carol", Username: "carol", DisplayName: "Carol"},
				{ID: "u-bob", Username: "bob", DisplayName: "bob"},
				{ID: "u-alice", Username: "alice", DisplayName: "Alice"},
			},
			query: "limit=100",
			want:  []string{"u-alice", "u-bob", "u-carol"},
		},
		{
			name: "should break identical display names by username when names are equal",
			users: []model.User{
				{ID: "u-zed", Username: "zed", DisplayName: "Sam Lee"},
				{ID: "u-amy", Username: "amy", DisplayName: "Sam Lee"},
			},
			query: "limit=100",
			want:  []string{"u-amy", "u-zed"},
		},
		{
			name: "should break identical display names by username ignoring case",
			users: []model.User{
				{ID: "u-z", Username: "Zed", DisplayName: "Sam"},
				{ID: "u-a", Username: "amy", DisplayName: "sam"},
			},
			query: "limit=100",
			want:  []string{"u-a", "u-z"},
		},
		{
			name: "should break identical names and usernames by id",
			users: []model.User{
				{ID: "u-b", Username: "sam", DisplayName: "Sam"},
				{ID: "u-a", Username: "sam", DisplayName: "Sam"},
			},
			query: "limit=100",
			want:  []string{"u-a", "u-b"},
		},
		{
			name: "should list users with no display name after named users",
			users: []model.User{
				{ID: "u-mike", Username: "mike", DisplayName: ""},
				{ID: "u-zoe", Username: "zoe", DisplayName: "Zoe"},
				{ID: "u-adam", Username: "adam", DisplayName: ""},
			},
			query: "limit=100",
			want:  []string{"u-zoe", "u-adam", "u-mike"},
		},
		{
			name: "should order users with no display name by username ignoring case",
			users: []model.User{
				{ID: "u-bob", Username: "Bob", DisplayName: ""},
				{ID: "u-alice", Username: "alice", DisplayName: ""},
				{ID: "u-carl", Username: "Carl", DisplayName: ""},
			},
			query: "limit=100",
			want:  []string{"u-alice", "u-bob", "u-carl"},
		},
		{
			name: "should keep the same order under a search",
			users: []model.User{
				{ID: "u-z", Username: "z", DisplayName: "Zed Ann"},
				{ID: "u-b", Username: "b", DisplayName: "anna"},
				{ID: "u-c", Username: "c", DisplayName: "Bea"},
			},
			query: "limit=100&q=ann",
			want:  []string{"u-b", "u-z"},
		},
		{
			name: "should keep the same order under the active filter",
			users: []model.User{
				{ID: "u-carol", Username: "carol", DisplayName: "Carol", DisabledAt: &disabledAt},
				{ID: "u-bob", Username: "bob", DisplayName: "Bob"},
				{ID: "u-alice", Username: "alice", DisplayName: "alice"},
			},
			query: "limit=100&status=active",
			want:  []string{"u-alice", "u-bob"},
		},
	}
}

// paginationDirectory is one directory with ties at the page boundaries:
// three users share the name "Sam", and two have no name at all.
func paginationDirectory() []model.User {
	return []model.User{
		{ID: "u-sc", Username: "c", DisplayName: "Sam"},
		{ID: "u-m", Username: "m", DisplayName: ""},
		{ID: "u-sa", Username: "a", DisplayName: "Sam"},
		{ID: "u-x", Username: "x", DisplayName: "anna"},
		{ID: "u-d", Username: "d", DisplayName: ""},
		{ID: "u-sb", Username: "b", DisplayName: "Sam"},
		{ID: "u-y", Username: "y", DisplayName: "Zed"},
	}
}

// paginationWant is the full directory order for paginationDirectory.
var paginationWant = []string{"u-x", "u-sa", "u-sb", "u-sc", "u-y", "u-d", "u-m"}

// checkUserOrder runs every ordering case and the paging check against db.
// It clears the users table before each case, so one database can serve
// them all.
func checkUserOrder(t *testing.T, db *gorm.DB) {
	t.Helper()

	cfg := newTestConfig(t)
	r := routerWithAuth(t, cfg, db, adminIdentity(cfg.Admin.RequireGroup))

	for _, tc := range userOrderCases() {
		t.Run(tc.name, func(t *testing.T) {
			resetUsers(t, db)
			seedDirectory(t, db, tc.users)

			got, _ := listUserIDs(t, r, tc.query)
			if !equalStrings(got, tc.want) {
				t.Errorf("GET /admin/users?%s listed %v, want %v", tc.query, got, tc.want)
			}
		})
	}

	for _, limit := range []int{1, 2, 3, 4, 7, 100} {
		t.Run(fmt.Sprintf("should page through every user exactly once when limit is %d", limit), func(t *testing.T) {
			resetUsers(t, db)
			seedDirectory(t, db, paginationDirectory())

			var paged []string
			query := fmt.Sprintf("limit=%d", limit)
			for range len(paginationWant) + 1 {
				page, next := listUserIDs(t, r, query)
				paged = append(paged, page...)
				if next == "" {
					break
				}
				query = fmt.Sprintf("limit=%d&after=%s", limit, next)
			}

			if !equalStrings(paged, paginationWant) {
				t.Errorf("paging with limit %d listed %v, want %v", limit, paged, paginationWant)
			}
		})
	}
}

// resetUsers empties the users table so a case starts from an empty
// directory.
func resetUsers(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec("DELETE FROM users").Error; err != nil {
		t.Fatalf("clear users: %v", err)
	}
}

// seedDirectory inserts users, giving each a subject derived from its id so
// the unique subject index never collides.
func seedDirectory(t *testing.T, db *gorm.DB, users []model.User) {
	t.Helper()
	for _, u := range users {
		u.Subject = "sub-" + u.ID
		if err := db.Create(&u).Error; err != nil {
			t.Fatalf("seed user %s: %v", u.ID, err)
		}
	}
}

// listUserIDs calls GET /admin/users with query and returns the ids in the
// order the response lists them, and the next cursor ("" on the last page).
func listUserIDs(t *testing.T, r http.Handler, query string) ([]string, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/admin/users?"+query, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /admin/users?%s = %d, want 200; body: %s", query, w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Users []struct {
				ID string `json:"id"`
			} `json:"users"`
			NextCursor *string `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v, body: %s", err, w.Body.String())
	}
	ids := make([]string, 0, len(resp.Data.Users))
	for _, u := range resp.Data.Users {
		ids = append(ids, u.ID)
	}
	next := ""
	if resp.Data.NextCursor != nil {
		next = *resp.Data.NextCursor
	}
	return ids, next
}

// equalStrings reports whether a and b hold the same strings in the same
// order. Nil and empty are equal.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// should order the admin user directory by name, case-insensitively, with
// username and id as tiebreakers, and keep that order under search and
// filters, on whichever backend the run selects (Postgres's collation behind
// LOWER and ORDER BY differs from SQLite's).
func TestListUsersHandler_ShouldOrderTheDirectory(t *testing.T) {
	checkUserOrder(t, testdb.New(t))
}
