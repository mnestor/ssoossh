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
	"strconv"
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

	// The port postgres listens on *inside* the container. Fixed is safe:
	// it is namespaced to that container, or to this process's own
	// namespace in the joined-network case below.
	const containerPort = 54329
	args := []string{"run", "-d", "--rm",
		"-e", "POSTGRES_PASSWORD=parity",
		"-e", "POSTGRES_DB=parity",
	}
	joinedNetwork := false
	if _, err := os.Stat("/.dockerenv"); err == nil {
		joinedNetwork = true
		self, err := os.Hostname()
		if err != nil {
			t.Fatalf("hostname: %v", err)
		}
		args = append(args, "--network", "container:"+self)
	} else {
		// Host port 0 makes the daemon pick a free one. A fixed host port
		// raced this package's own teardown: `docker rm -f` returns before
		// the daemon releases the published port, so the next test's
		// `docker run` failed with "port is already allocated" even though
		// the tests run sequentially.
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:0:%d", containerPort))
	}
	args = append(args, "postgres:17-alpine", "-p", fmt.Sprintf("%d", containerPort))

	// Capture the container ID from stdout alone: on a cache miss docker
	// run writes pull progress to stderr while still exiting 0, and a
	// CombinedOutput capture would corrupt the ID with that noise -- the
	// same trap the NATS harnesses hit (test/e2e/harness/nats.go).
	run := exec.Command("docker", args...)
	var runErr strings.Builder
	run.Stderr = &runErr
	out, err := run.Output()
	if err != nil {
		t.Fatalf("docker run postgres: %v\n%s", err, runErr.String())
	}
	id := strings.TrimSpace(string(out))
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", id).Run() }) //nolint:errcheck // best-effort teardown; --rm reaps it regardless.

	// Joining the namespace publishes nothing, so the container port is
	// reachable as-is; otherwise ask the daemon what it assigned.
	port := containerPort
	if !joinedNetwork {
		port = publishedPort(t, id, containerPort)
	}

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

// publishedPort asks the daemon which host port it bound to the container's
// published port. `docker port` prints one "host:port" line per binding, and
// the host half can itself contain colons (an IPv6 bind), so the port is
// everything after the last one.
func publishedPort(t *testing.T, id string, containerPort int) int {
	t.Helper()

	out, err := exec.Command("docker", "port", id, fmt.Sprintf("%d/tcp", containerPort)).CombinedOutput()
	if err != nil {
		t.Fatalf("docker port %s: %v\n%s", id, err, out)
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	i := strings.LastIndex(line, ":")
	if i < 0 {
		t.Fatalf("docker port returned %q, want host:port", line)
	}
	port, err := strconv.Atoi(line[i+1:])
	if err != nil {
		t.Fatalf("docker port returned an unparsable port in %q: %v", line, err)
	}
	return port
}
