// Package postgres provides shared database testing infrastructure.
// Note: The real E2E Postgres setup is in test/e2e/harness/postgres.go.
// This package is retained for potential future use in unit tests.
package postgres

import (
	"net/url"
)

// containsSSLMode checks if the DSN already contains a sslmode parameter.
func containsSSLMode(dsn string) bool {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return false
	}
	query := parsed.Query()
	return query.Get("sslmode") != ""
}
