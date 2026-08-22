// Package postgres provides test infrastructure for running tests against
// a live Postgres database using testcontainers.
package postgres

import (
	"context"
	"net/url"
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

	// Start the container.
	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "postgres:17-alpine",
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_USER":     "ssoossh",
				"POSTGRES_PASSWORD": "testpassword",
				"POSTGRES_DB":       "ssoossh",
			},
			WaitingFor: wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(testcontainers.DefaultStartupTimeout),
		},
		Started: true,
	}

	pgContainer, err := postgres.RunContainer(ctx, req)
	if err != nil {
		if testcontainers.IsContainerNotFoundError(err) || testcontainers.IsDockerUnavailableError(err) {
			t.Skip("Docker unavailable or Postgres image not found; skipping live Postgres test")
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

	// Append SSL mode.
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

// IP returns the container's IP address on the network. Useful for
// container-to-container networking.
func (c *Container) IP(ctx context.Context) (string, error) {
	return c.container.ContainerIP(ctx)
}

// Port returns the mapped host port for the given container port.
func (c *Container) Port(ctx context.Context, port string) (string, error) {
	return c.container.MappedPort(ctx, port)
}
