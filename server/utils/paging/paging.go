// Package paging parses the window and search parameters the admin and
// auditor list endpoints share, and builds the keyset seek they page with.
//
// It exists so those endpoints agree on one contract — `after`, `limit`, and
// `q`, with `next_cursor` and a total row count in the response — rather
// than each inventing its own. The contract is the one the caller's own
// history (/api/certs) already uses: `after` is the id of the last row of
// the previous page, and each list resumes from that row's place in its own
// order with the id as the tiebreaker. A row written or deleted between two
// requests therefore neither repeats nor skips a row, which offset paging
// over a live table cannot promise.
//
// Nothing here knows about a particular table. A caller supplies the columns
// its list searches and orders by; everything else is shared.
package paging

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

const (
	// DefaultLimit is the page size an endpoint serves when the caller names
	// none.
	DefaultLimit = 25

	// MaxLimit bounds the page size a caller may ask for. A request above it
	// is clamped rather than rejected: an oversized page is a UI asking for
	// more than it should get, not a malformed request.
	MaxLimit = 100

	// MaxQueryLength bounds the search term. Long terms match nothing useful
	// and only cost a scan, and the bound keeps a pathological input out of
	// the query plan.
	MaxQueryLength = 200

	// MaxCursorLength bounds `after`. A cursor is a row id (a UUID), so
	// anything this long is not one, and is refused before it reaches a
	// query.
	MaxCursorLength = 128

	// likeEscape is the character Filter escapes LIKE wildcards with. It is
	// spelled into the SQL as `ESCAPE '\'`, which both SQLite and Postgres
	// accept (Postgres with standard_conforming_strings on, the default
	// since 9.1).
	likeEscape = `\`
)

// Params is one page request: a bounded window plus an optional search term.
type Params struct {
	// Limit is the page size, always between 1 and MaxLimit.
	Limit int

	// After is the id of the last row of the previous page, trimmed. Empty
	// means the first page.
	After string

	// Query is the caller's search term, trimmed. Empty means "no filter" —
	// Filter returns nothing for it, so an endpoint does not need to branch.
	Query string
}

// Values is the subset of url.Values Parse reads. Declared as an interface
// so a caller can pass gin's query values or a plain url.Values without the
// package depending on gin.
type Values interface {
	Get(key string) string
	Has(key string) bool
}

// Parse reads after, limit, and q from values, applying the defaults and
// bounds.
//
// A malformed number is an error rather than a silent fallback to the
// default: a UI that sends `limit=abc` and gets 25 rows back has no way to
// learn its parameter was thrown away, and would page through the list
// wrongly forever. An oversized limit is clamped instead, since that is the
// server declining to serve more rather than the caller being wrong.
//
// `offset` is refused outright, empty or not. These lists paged by offset
// once, and a caller still sending it would otherwise be handed the first
// page every time and never learn why.
func Parse(values Values) (Params, error) {
	p := Params{Limit: DefaultLimit}

	if values.Has("offset") {
		return Params{}, &errorresponses.InvalidRequestError{Reason: "offset is not supported: page with after and the next_cursor of the previous page"}
	}

	if raw := values.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			return Params{}, &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("limit must be a number, got %q", raw)}
		}
		if limit < 1 {
			return Params{}, &errorresponses.InvalidRequestError{Reason: "limit must be at least 1"}
		}
		if limit > MaxLimit {
			limit = MaxLimit
		}
		p.Limit = limit
	}

	after := strings.TrimSpace(values.Get("after"))
	if len(after) > MaxCursorLength {
		return Params{}, UnknownCursor()
	}
	p.After = after

	query := strings.TrimSpace(values.Get("q"))
	if len(query) > MaxQueryLength {
		return Params{}, &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("q cannot be longer than %d characters", MaxQueryLength)}
	}
	p.Query = query

	return p, nil
}

// UnknownCursor is the error for an `after` that names no row of the list
// being paged: a malformed value, an id from another list, or a row that no
// longer exists. A 400 rather than a first page, for the same reason a
// malformed limit is one: a UI handed page one again would show it as page
// two.
func UnknownCursor() error {
	return &errorresponses.InvalidRequestError{Reason: "after does not name a row in this list"}
}

// SeekBefore builds the WHERE fragment that resumes a list ordered
// (timeColumn DESC, idColumn DESC) after the row with sort time at and id.
// The id breaks ties between rows written in the same instant, so a row
// sharing the cursor's timestamp is neither repeated nor skipped.
//
// The leading "timeColumn <= at" is implied by the rest and is there for the
// planner: Postgres cannot turn the OR into an index bound, and without a
// plain range it walks the index from the newest row and filters its way
// down to the cursor, which costs what an offset did. With it, the scan
// starts at the cursor on both backends.
//
// Column names are interpolated into the SQL, so they must be literals from
// the calling package — never a caller-supplied string.
func SeekBefore(timeColumn, idColumn string, at time.Time, id string) (string, []any) {
	sql := fmt.Sprintf("(%[1]s <= ? AND (%[1]s < ? OR (%[1]s = ? AND %[2]s < ?)))", timeColumn, idColumn)
	return sql, []any{at, at, at, id}
}

// Trim cuts rows, fetched with Limit+1, down to one page and reports the
// cursor for the next: the id of the page's last row, or nil when the extra
// row was not there and this is the last page.
func Trim[T any](rows []T, limit int, id func(T) string) ([]T, *string) {
	if len(rows) <= limit {
		return rows, nil
	}
	rows = rows[:limit]
	next := id(rows[len(rows)-1])
	return rows, &next
}

// Count runs COUNT(*) over db, which must already carry the filter but
// neither the seek nor the window: the total describes the whole filtered
// list, which is what "25 of 120" is read from.
func Count(db *gorm.DB) (int64, error) {
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return 0, err
	}
	return total, nil
}

// Filter builds the WHERE fragment and bind arguments matching query as a
// case-insensitive substring of any of columns, along with the arguments to
// bind. It returns "" and nil when there is nothing to filter on, which
// gorm's Where accepts as a no-op.
//
// LOWER(col) LIKE lower-pattern rather than ILIKE: ILIKE is Postgres-only,
// and SQLite's LIKE is case-insensitive for ASCII but not beyond it, so
// neither behaves the same on both drivers on its own. Lowering both sides
// does.
//
// Column names are interpolated into the SQL, so they must be literals from
// the calling package — never a caller-supplied string.
func Filter(query string, columns ...string) (string, []any) {
	if query == "" || len(columns) == 0 {
		return "", nil
	}

	pattern := "%" + escapeLike(strings.ToLower(query)) + "%"

	clauses := make([]string, 0, len(columns))
	args := make([]any, 0, len(columns))
	for _, column := range columns {
		clauses = append(clauses, fmt.Sprintf("LOWER(%s) LIKE ? ESCAPE '%s'", column, likeEscape))
		args = append(args, pattern)
	}

	if len(clauses) == 1 {
		return clauses[0], args
	}
	return "(" + strings.Join(clauses, " OR ") + ")", args
}

// escapeLike neutralizes the LIKE wildcards in s so a search term matches
// itself literally. Without it, a user typing "%" into a search box matches
// every row, and "_" matches any character — both of which read as the
// search being broken.
//
// The escape character goes first: escaping it after the wildcards would
// double the backslashes this function had just introduced.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, likeEscape, likeEscape+likeEscape)
	s = strings.ReplaceAll(s, "%", likeEscape+"%")
	s = strings.ReplaceAll(s, "_", likeEscape+"_")
	return s
}
