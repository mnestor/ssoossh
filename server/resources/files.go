// Package resources provides access to embedded static resources used by the
// server, such as database migration files and mail templates.
package resources

import "embed"

// FS holds the embedded static resources, each under its own subdirectory:
// migrations/<provider>/ (see server/bootstrap's migrateDatabase) and
// mail/ (see server/mail's NewRenderer).
//
//go:embed migrations mail
var FS embed.FS
