// Package frontend provides shared frontend-related errors and utilities.
package frontend

// taken from pocket-id... again
// https://github.com/pocket-id/pocket-id/tree/main/backend/frontend

import "errors"

// ErrFrontendNotIncluded is returned by RegisterFrontend when the binary was
// built with the exclude_frontend build tag, so no embedded frontend assets
// are available to serve.
var ErrFrontendNotIncluded = errors.New("frontend is not included")
