// Package resources provides access to embedded static resources used by the
// server, such as database migration files.
package resources

import "embed"

// FS holds the embedded database migration files, organized under
// migrations/<provider>/ (see server/bootstrap's migrateDatabase).
//
//go:embed migrations
var FS embed.FS
