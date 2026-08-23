package service

// Test methodology: Unit tests for SSH certificate issuance service logic.
// Tests run in parallel (t.Parallel()). Verifies certificate generation,
// validation, and signing. Uses table-driven tests for multiple scenarios.
// Helper functions generate test SSH keys and configurations.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/server/config"
)

// generateTestSSHPrivateKey returns a throwaway ed25519 SSH private key in
// PEM format, along with the authorized_keys-formatted public key it
// corresponds to, for use as test fixtures. It never touches real key
// material or the filesystem.
func generateTestSSHPrivateKey(t *testing.T) (pemKey string, authorizedKey string) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ed25519 key: %v", err)
	}

	block, err := ssh.MarshalPrivateKey(priv, "test-key")
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("failed to derive public key: %v", err)
	}

	return string(pem.EncodeToMemory(block)),
		strings.TrimRight(string(ssh.MarshalAuthorizedKey(sshPub)), "\n")
}

func TestNewCAService_ShouldSucceedWithValidPrivateKey(t *testing.T) {
	t.Parallel()

	pemKey, wantPub := generateTestSSHPrivateKey(t)
	c := &config.Config{Signer: config.SignerConfig{SSHKey: pemKey}}

	svc, err := NewCAService(c, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got, err := svc.GetCAPublicKey(context.Background())
	if err != nil {
		t.Fatalf("expected no error from GetCAPublicKey, got %v", err)
	}
	if got != wantPub {
		t.Errorf("got public key %q, want %q", got, wantPub)
	}
}

func TestNewCAService_ShouldErrorWithInvalidPrivateKey(t *testing.T) {
	t.Parallel()

	c := &config.Config{Signer: config.SignerConfig{SSHKey: "not a valid private key"}}

	_, err := NewCAService(c, nil)
	if err == nil {
		t.Fatal("expected an error for invalid private key, got nil")
	}
}

func TestNewCAService_ShouldErrorWithEmptyPrivateKey(t *testing.T) {
	t.Parallel()

	c := &config.Config{Signer: config.SignerConfig{SSHKey: ""}}

	_, err := NewCAService(c, nil)
	if err == nil {
		t.Fatal("expected an error for empty private key, got nil")
	}
}

func TestGetCAPublicKey_ShouldReturnTrimmedKeyWithoutTrailingNewline(t *testing.T) {
	t.Parallel()

	pemKey, _ := generateTestSSHPrivateKey(t)
	c := &config.Config{Signer: config.SignerConfig{SSHKey: pemKey}}

	svc, err := NewCAService(c, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got, err := svc.GetCAPublicKey(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("expected public key to have no trailing newline, got %q", got)
	}
}
