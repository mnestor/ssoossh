//go:build e2e || resilience || load

package harness

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	postgresModule "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// StartPostgres starts a fresh Postgres container for E2E testing. If Docker
// is unavailable, the test is skipped. The container is automatically
// stopped via t.Cleanup when the test ends. The DSN is set in
// SSOOSSH_E2E_POSTGRES_DSN so the harness server picks it up automatically.
//
// Note: The container setup is minimal to keep the harness fast. For detailed
// container management, see test/postgres package.
func StartPostgres(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Use the postgres module which provides a simplified setup for Postgres.
	// Documentation: github.com/testcontainers/testcontainers-go/modules/postgres
	pgContainer, err := postgresModule.RunContainer(ctx,
		postgresModule.WithDatabase("ssoossh"),
		postgresModule.WithUsername("ssoossh"),
		postgresModule.WithPassword("testpassword"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2),
		),
	)
	if err != nil {
		// Check if Docker is unavailable by examining the error.
		if strings.Contains(err.Error(), "Docker") ||
			strings.Contains(err.Error(), "docker") ||
			strings.Contains(err.Error(), "socket") {
			t.Skip("Docker unavailable; skipping Postgres-backed E2E test")
		}
		t.Fatalf("harness: failed to start Postgres container: %v", err)
	}

	// Register cleanup to stop the container.
	t.Cleanup(func() {
		if err := pgContainer.Terminate(context.Background()); err != nil {
			t.Logf("harness: failed to terminate Postgres container: %v", err)
		}
	})

	// Get the DSN for connecting to the database.
	dsn, err := pgContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("harness: failed to get Postgres connection string: %v", err)
	}

	// Append sslmode=disable if not already present, since the test container
	// doesn't use SSL.
	if !containsSSLMode(dsn) {
		dsn += "?sslmode=disable"
	}

	// Set the environment variable so the harness server picks it up.
	os.Setenv("SSOOSSH_E2E_POSTGRES_DSN", dsn)
}

// containsSSLMode checks if the DSN already contains a sslmode parameter.
func containsSSLMode(dsn string) bool {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return false
	}
	query := parsed.Query()
	return query.Get("sslmode") != ""
}
