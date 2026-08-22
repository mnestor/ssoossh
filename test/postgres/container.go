// Package postgres provides test infrastructure for running tests against
// a live Postgres database using testcontainers.
package postgres

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Container holds a running Postgres testcontainer and its connection details.
type Container struct {
	container *postgres.PostgresContainer
	dsn       string
}

// New starts a fresh Postgres container and returns it. If Docker is unavailable,
// the test is skipped with an explanatory message. The container is automatically
// stopped via t.Cleanup when the test ends.
func New(t *testing.T, ctx context.Context) *Container {
	t.Helper()

	// Start the container using the postgres module's simplified API.
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
		// Check if Docker is unavailable by looking at the error message.
		if strings.Contains(err.Error(), "Docker") || strings.Contains(err.Error(), "docker") {
			t.Skip("Docker unavailable; skipping live Postgres test")
		}
		t.Fatalf("failed to start Postgres container: %v", err)
	}

	// Register cleanup to stop the container.
	t.Cleanup(func() {
		if err := pgContainer.Terminate(context.Background()); err != nil {
			t.Logf("failed to terminate Postgres container: %v", err)
		}
	})

	// Get the DSN.
	dsn, err := pgContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get Postgres connection string: %v", err)
	}

	// Append SSL mode if not already present.
	if !containsSSLMode(dsn) {
		dsn += "?sslmode=disable"
	}

	return &Container{
		container: pgContainer,
		dsn:       dsn,
	}
}

// DSN returns the connection string for the container.
func (c *Container) DSN() string {
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
