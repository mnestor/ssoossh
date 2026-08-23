//go:build e2e || resilience || load

package harness

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for admin DDL
)

// NewPostgresDatabase creates a fresh, uniquely named database on the
// postgres instance SSOOSSH_E2E_POSTGRES_DSN points at and returns a DSN for
// it, dropping the database in t.Cleanup. This is what makes the isolation
// promise in renderServerConfig true: each server (or an explicit group of
// servers handed the same returned DSN) owns its database outright, the same
// way the sqlite backend owns its :memory: store.
//
// Skips the test when SSOOSSH_E2E_POSTGRES_DSN is unset: postgres-backed
// tests are meaningless without an instance, and every environment that has
// one (CI service container, local docker) advertises it via that variable.
func NewPostgresDatabase(t *testing.T) string {
	t.Helper()

	adminDSN := os.Getenv("SSOOSSH_E2E_POSTGRES_DSN")
	if adminDSN == "" {
		t.Skip("SSOOSSH_E2E_POSTGRES_DSN is not set; skipping postgres-backed test")
	}

	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("harness: failed to generate database name suffix: %v", err)
	}
	name := "e2e_" + hex.EncodeToString(suffix[:])

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("harness: failed to open admin postgres connection: %v", err)
	}
	// The database name is harness-generated hex, not external input, so
	// identifier interpolation is safe; DDL cannot be parameterized anyway.
	if _, err := admin.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE %s", name)); err != nil {
		_ = admin.Close()
		t.Fatalf("harness: failed to create database %s: %v", name, err)
	}
	_ = admin.Close()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := sql.Open("pgx", adminDSN)
		if err != nil {
			t.Logf("harness: failed to reopen admin connection to drop %s: %v", name, err)
			return
		}
		defer admin.Close()
		// FORCE terminates any connection a just-killed server still holds.
		if _, err := admin.ExecContext(ctx, fmt.Sprintf("DROP DATABASE %s WITH (FORCE)", name)); err != nil {
			t.Logf("harness: failed to drop database %s: %v", name, err)
		}
	})

	dsn, err := replaceDatabaseName(adminDSN, name)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	return dsn
}

// replaceDatabaseName swaps the database (path) component of a postgres://
// URL DSN, keeping credentials, host, and query parameters.
func replaceDatabaseName(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse postgres DSN: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}
