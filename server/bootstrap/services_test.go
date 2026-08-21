package bootstrap

// Test methodology: Unit tests for service initialization. Tests run in
// parallel (t.Parallel()). Uses table-driven tests where appropriate and
// helper functions to generate test data (SSH keys, config objects).

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"log/slog"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/pubsub"
	"github.com/mnestor/ssoossh/server/testutil"
)

func testSSHKeyPEM(t *testing.T) string {
	t.Helper()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "test-key")
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}

func TestInitServices_ShouldConstructCAService(t *testing.T) {
	t.Parallel()

	oidcSrv := testutil.NewTestOIDCProvider(t)

	c := &config.Config{SSHKey: testSSHKeyPEM(t)}
	c.AuthConfig.ClientID = "test-client"
	c.AuthConfig.ProviderURL = oidcSrv.URL
	c.AuthConfig.Fields.Username = "sub"
	c.HTTP.ServerName = "ssoossh.example.com"

	ps, err := pubsub.New(slog.Default())
	if err != nil {
		t.Fatalf("failed to build pub/sub: %v", err)
	}
	t.Cleanup(func() {
		if err := ps.Close(t.Context()); err != nil {
			t.Errorf("unexpected error closing pub/sub: %v", err)
		}
	})
	a := &app{config: c, pubSub: ps}

	svc, err := a.initServices()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.ca == nil {
		t.Fatal("expected a non-nil CAService")
	}
}

// TestInitServices_ShouldErrorOnInvalidSSHKey exercises initServices with a
// fixture matching what Bootstrap actually passes it — a fully populated
// app, same as TestInitServices_ShouldConstructCAService — because
// initServices now builds every service concurrently: an app missing
// dependencies the other constructors need (db, pubSub) would fail before
// ever reaching the invalid SSH key this test means to exercise.
func TestInitServices_ShouldErrorOnInvalidSSHKey(t *testing.T) {
	t.Parallel()

	oidcSrv := testutil.NewTestOIDCProvider(t)

	c := &config.Config{SSHKey: "not a valid key"}
	c.AuthConfig.ClientID = "test-client"
	c.AuthConfig.ProviderURL = oidcSrv.URL
	c.AuthConfig.Fields.Username = "sub"
	c.HTTP.ServerName = "ssoossh.example.com"

	ps, err := pubsub.New(slog.Default())
	if err != nil {
		t.Fatalf("failed to build pub/sub: %v", err)
	}
	t.Cleanup(func() {
		if err := ps.Close(t.Context()); err != nil {
			t.Errorf("unexpected error closing pub/sub: %v", err)
		}
	})
	a := &app{config: c, pubSub: ps}

	if _, err := a.initServices(); err == nil {
		t.Fatal("expected an error for an invalid SSH key, got nil")
	}
}
