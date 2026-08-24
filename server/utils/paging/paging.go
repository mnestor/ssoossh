// Package paging parses and applies the window and search parameters the
// admin and auditor list endpoints share.
//
// It exists so those endpoints agree on one contract — `limit`, `offset`,
// and `q`, with a total row count in the response — rather than each
// inventing its own. Offset paging is deliberate: the admin lists show page
// numbers and let an auditor jump to a page, which a cursor cannot do. The
// caller-facing endpoints (/api/certs) stay on cursors, where the UI only
// ever loads more.
//
// Nothing here knows about a particular table. A caller supplies the columns
// its list searches; everything else is shared.
package paging

import (
	"fmt"
	"strconv"
	"strings"

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

	// Offset is how many rows to skip, never negative.
	Offset int

	// Query is the caller's search term, trimmed. Empty means "no filter" —
	// Filter returns nothing for it, so an endpoint does not need to branch.
	Query string
}

// Values is the subset of url.Values Parse reads. Declared as an interface
// so a caller can pass gin's query values or a plain url.Values without the
// package depending on gin.
type Values interface {
	Get(key string) string
}

// Parse reads limit, offset, and q from values, applying the defaults and
// bounds.
//
// A malformed number is an error rather than a silent fallback to the
// default: a UI that sends `limit=abc` and gets 25 rows back has no way to
// learn its parameter was thrown away, and would page through the list
// wrongly forever. An oversized limit is clamped instead, since that is the
// server declining to serve more rather than the caller being wrong.
func Parse(values Values) (Params, error) {
	p := Params{Limit: DefaultLimit}

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

	if raw := values.Get("offset"); raw != "" {
		offset, err := strconv.Atoi(raw)
		if err != nil {
			return Params{}, &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("offset must be a number, got %q", raw)}
		}
		if offset < 0 {
			return Params{}, &errorresponses.InvalidRequestError{Reason: "offset cannot be negative"}
		}
		p.Offset = offset
	}

	query := strings.TrimSpace(values.Get("q"))
	if len(query) > MaxQueryLength {
		return Params{}, &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("q cannot be longer than %d characters", MaxQueryLength)}
	}
	p.Query = query

	return p, nil
}

// PageNumber reports the 1-based page the window lands on, for a response to
// echo back to a UI that renders page numbers.
func (p Params) PageNumber() int {
	if p.Limit <= 0 {
		return 1
	}
	return p.Offset/p.Limit + 1
}

// PageCount reports how many pages total rows fill at this page size. An
// empty list is one (empty) page, not zero, so a UI never has to special-case
// "page 1 of 0".
func (p Params) PageCount(total int64) int {
	if p.Limit <= 0 || total <= 0 {
		return 1
	}
	pages := int((total + int64(p.Limit) - 1) / int64(p.Limit))
	if pages < 1 {
		return 1
	}
	return pages
}

// Apply adds the window to db. The caller supplies the ordering: a paged
// query without a deterministic ORDER BY can return the same row on two
// pages and skip another entirely.
func Apply(db *gorm.DB, p Params) *gorm.DB {
	return db.Limit(p.Limit).Offset(p.Offset)
}

// Count runs COUNT(*) over db, which must already carry the filter but not
// the window — the total describes the whole result set, which is what a
// page-number UI needs.
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
