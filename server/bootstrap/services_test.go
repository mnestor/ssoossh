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

	c := &config.Config{Signer: config.SignerConfig{SSHKey: testSSHKeyPEM(t)}}
	c.AuthConfig.ClientID = "test-client"
	c.AuthConfig.ProviderURL = oidcSrv.URL
	c.AuthConfig.Fields.Username = "sub"
	c.HTTP.ServerName = "ssoossh.example.com"

	ps, err := pubsub.New(&config.PubSubConfig{}, slog.Default())
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

// TestInitServices_ShouldSucceedWithInvalidSSHKeyForAPIMode verifies that
// initServices succeeds even with an invalid SSH key, since SSH key
// validation no longer happens in initServices — it's deferred to
// initSignerHandler (which is only called for full and signer-only modes).
// API mode doesn't need a valid SSH key at all.
func TestInitServices_ShouldSucceedWithInvalidSSHKeyForAPIMode(t *testing.T) {
	t.Parallel()

	oidcSrv := testutil.NewTestOIDCProvider(t)

	c := &config.Config{Signer: config.SignerConfig{SSHKey: "not a valid key"}}
	c.AuthConfig.ClientID = "test-client"
	c.AuthConfig.ProviderURL = oidcSrv.URL
	c.AuthConfig.Fields.Username = "sub"
	c.HTTP.ServerName = "ssoossh.example.com"

	ps, err := pubsub.New(&config.PubSubConfig{}, slog.Default())
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
		t.Fatalf("expected no error for API mode with invalid SSH key, got %v", err)
	}
	if svc.ca == nil {
		t.Fatal("expected a non-nil CAService")
	}
}
