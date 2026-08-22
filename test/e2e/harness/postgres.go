//go:build e2e

package harness

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// PostgresContainer wraps a running Postgres testcontainer and provides
// test infrastructure for E2E tests against a live Postgres backend.
// Tests can use StartPostgres to get a container and run tests against it.
type PostgresContainer struct {
	container *postgres.PostgresContainer
	dsn       string
}

// StartPostgres starts a fresh Postgres container for E2E testing. If Docker
// is unavailable, the test is skipped. The container is automatically
// stopped via t.Cleanup when the test ends. The DSN is set in
// SSOOSSH_E2E_POSTGRES_DSN so the harness server picks it up automatically.
func StartPostgres(t *testing.T) *PostgresContainer {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Start the Postgres container using the module's high-level API.
	pgContainer, err := postgres.RunContainer(
		ctx,
		postgres.WithImage("postgres:17-alpine"),
		postgres.WithDatabase("ssoossh"),
		postgres.WithUsername("ssoossh"),
		postgres.WithPassword("testpassword"),
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

	return &PostgresContainer{
		container: pgContainer,
		dsn:       dsn,
	}
}

// DSN returns the connection string for the Postgres container.
func (c *PostgresContainer) DSN() string {
	return c.dsn
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
