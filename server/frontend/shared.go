// Package frontend provides shared frontend-related errors and utilities.
package frontend

// taken from pocket-id... again
// https://github.com/pocket-id/pocket-id/tree/main/backend/frontend

import "errors"

// ErrFrontendNotIncluded is returned by RegisterFrontend when the binary was
// built with the exclude_frontend build tag, so no embedded frontend assets
// are available to serve.
var ErrFrontendNotIncluded = errors.New("frontend is not included")

// RateLimitSkipURLPrefixes are the request URL path prefixes (with the
// leading slash, as they appear in c.Request.URL.Path) for content-hashed
// static assets that bypass the global per-IP rate limiter wired in
// server/bootstrap. These are immutable embedded files with no database
// access and no audit writes, so they are not what that limiter defends
// (its purpose is throttling unauthenticated audit-writing endpoints), yet
// a cold first paint of the bundled app pulls dozens of them at once, which
// alone could exhaust the per-IP burst (issue #210).
//
// Each entry is immutableAssetPrefixes with a leading slash; the frontend
// test keeps the two in step. Lives here, not in frontend_included.go, so
// the bootstrap router can read it under either build tag; it is harmless
// under exclude_frontend, where nothing serves these paths.
func RateLimitSkipURLPrefixes() []string {
	return []string{"/_app/immutable/"}
}
