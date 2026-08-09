package bootstrap

// Test methodology: Unit tests for service initialization. Tests run in
// parallel (t.Parallel()). Uses table-driven tests where appropriate and
// helper functions to generate test data (SSH keys, config objects).

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/server/config"
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

	oidcSrv := newTestOIDCProvider(t)

	c := &config.Config{SSHKey: testSSHKeyPEM(t)}
	c.AuthConfig.ClientID = "test-client"
	c.AuthConfig.ProviderURL = oidcSrv.URL
	c.AuthConfig.Fields.Username = "sub"
	a := &app{config: c}

	svc, err := a.initServices()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if svc.ca == nil {
		t.Fatal("expected a non-nil CAService")
	}
}

func TestInitServices_ShouldErrorOnInvalidSSHKey(t *testing.T) {
	t.Parallel()

	c := &config.Config{SSHKey: "not a valid key"}
	a := &app{config: c}

	_, err := a.initServices()
	if err == nil {
		t.Fatal("expected an error for an invalid SSH key, got nil")
	}
}
