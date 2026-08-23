// Package postgres provides shared Postgres testing infrastructure for the
// dialect-parity suite (test/migration). The e2e harness has its own,
// separate Postgres bring-up in test/e2e/harness/postgres.go; this package
// exists so schema-inspection tests can run without the e2e build tag.
package postgres

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ConnectAndMigrate starts a throwaway postgres:17-alpine container, waits
// for it, opens a gorm handle, and applies the up migrations. The second
// return is the container id, which RunUp/RunDown callers thread back in
// only for symmetry - the migrations run over the db handle.
//
// Networking mirrors server/pubsub's NATS integration test: inside a
// devcontainer talking to the host docker daemon, published ports bind on
// the HOST's loopback and the bridge is a different network, so the
// container joins this process's own network namespace instead; on a bare
// host it publishes a port normally. Skips only when the docker daemon
// itself is absent.
func ConnectAndMigrate(t *testing.T, ctx context.Context) (*gorm.DB, string) {
	t.Helper()

	if err := exec.Command("docker", "info").Run(); err != nil {
		// Skipping is right on a laptop without docker and wrong in CI,
		// where a silent skip is a green check for a suite that never ran
		// -- the same shape of problem as a gate that compares a generated
		// file to itself. SSOOSSH_REQUIRE_DOCKER=1 turns the skip into the
		// failure it should be anywhere the daemon is supposed to exist.
		if os.Getenv("SSOOSSH_REQUIRE_DOCKER") == "1" {
			t.Fatalf("docker daemon unavailable and SSOOSSH_REQUIRE_DOCKER=1: %v", err)
		}
		t.Skipf("docker daemon unavailable: %v", err)
	}

	const port = 54329 // fixed: the suite runs sequentially in one package
	args := []string{"run", "-d", "--rm",
		"-e", "POSTGRES_PASSWORD=parity",
		"-e", "POSTGRES_DB=parity",
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		self, err := os.Hostname()
		if err != nil {
			t.Fatalf("hostname: %v", err)
		}
		args = append(args, "--network", "container:"+self)
	} else {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", port, port))
	}
	args = append(args, "postgres:17-alpine", "-p", fmt.Sprintf("%d", port))

	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker run postgres: %v\n%s", err, out)
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() }) //nolint:errcheck // best-effort teardown; --rm reaps it regardless.

	dsn := fmt.Sprintf("postgres://postgres:parity@127.0.0.1:%d/parity?sslmode=disable", port)

	// Readiness: retry a real connection plus ping. Postgres restarts once
	// during init, so the first successful TCP accept is not readiness.
	var db *gorm.DB
	deadline := time.Now().Add(60 * time.Second)
	for {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
		if err == nil {
			if sqlDB, derr := db.DB(); derr == nil && sqlDB.PingContext(ctx) == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("postgres never became ready at %s: %v", dsn, err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	if err := RunUp(t, ctx, db); err != nil {
		t.Fatalf("apply postgres migrations: %v", err)
	}
	return db, id
}
