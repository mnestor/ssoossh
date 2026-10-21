package bootstrap

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/test/testdb"
)

// identityEchoTestConfig is a config over a fresh empty database on the
// run's backend: a file for SQLite, so the command's pool and the server's
// would see the same rows.
func identityEchoTestConfig(t *testing.T) *config.Config {
	t.Helper()

	c := &config.Config{}
	if testdb.IsPostgres(t) {
		provider, connection := testdb.Connection(t)
		c.DB.Provider, c.DB.Connection = config.DBProvider(provider), connection
	} else {
		c.DB.Provider = config.DBProviderSqlite
		c.DB.Connection = filepath.Join(t.TempDir(), "ssoossh.db")
	}
	return c
}

func TestOpenIdentityEcho_ShouldRefuseWhatItCannotRunAgainst(t *testing.T) {
	saveSlogDefault(t)

	tests := []struct {
		name    string
		mutate  func(c *config.Config)
		wantErr string
	}{
		{name: "should refuse a database the server never created", wantErr: "no schema yet"},
		{name: "should refuse a database it cannot open", mutate: func(c *config.Config) {
			// A path under a regular file: no directory can be made there,
			// root or not.
			file := filepath.Join(t.TempDir(), "not-a-dir")
			if err := os.WriteFile(file, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			c.DB.Provider, c.DB.Connection = config.DBProviderSqlite, filepath.Join(file, "ssoossh.db")
		}, wantErr: "failed to connect to database"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := identityEchoTestConfig(t)
			if tt.mutate != nil {
				tt.mutate(c)
			}

			e, err := OpenIdentityEcho(c, &bytes.Buffer{})
			if err == nil {
				_ = e.Close(context.Background())
				t.Fatalf("OpenIdentityEcho() error = nil, want one containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("OpenIdentityEcho() error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestOpenIdentityEcho_ShouldMintReadAndEndACodeWhenTheSchemaIsCurrent(t *testing.T) {
	saveSlogDefault(t)

	c := identityEchoTestConfig(t)
	migrateTestDatabase(t, c)

	e, err := OpenIdentityEcho(c, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("OpenIdentityEcho() error = %v", err)
	}
	ctx := context.Background()

	minted, err := e.Mint(ctx, service.MintRequest{TTL: time.Minute, RequestedBy: "ssoossh@test"})
	if err != nil {
		t.Fatalf("Mint() error = %v", err)
	}
	row, err := e.Get(ctx, minted.Row.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if row.RequestedBy != "ssoossh@test" || row.UsedAt != nil {
		t.Errorf("Get() = requested by %q, used %v; want the minted, unused code", row.RequestedBy, row.UsedAt)
	}
	ended, err := e.End(ctx, minted.Row.ID, "cancelled", nil)
	if err != nil || !ended {
		t.Errorf("End() = %t, %v; want the unused code retired", ended, err)
	}

	if err := e.Close(ctx); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if err := e.Close(ctx); err != nil {
		t.Errorf("second Close() error = %v, want nothing left to release", err)
	}
}
