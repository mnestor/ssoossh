package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
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

// mockCAKeyRegistry is a mock implementation of CAKeyRegistryReader for testing.
type mockCAKeyRegistry struct {
	keys []string
	err  error
}

func (m *mockCAKeyRegistry) ActiveKeys(ctx context.Context) ([]string, error) {
	return m.keys, m.err
}

func TestNewCAService_ShouldSucceedWithValidRegistry(t *testing.T) {
	t.Parallel()

	registry := &mockCAKeyRegistry{keys: []string{"ssh-ed25519 AAAA..."}}

	svc, err := NewCAService(nil, registry)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if svc == nil {
		t.Fatal("expected non-nil CAService")
	}
}

func TestNewCAService_ShouldErrorWithNilRegistry(t *testing.T) {
	t.Parallel()

	_, err := NewCAService(nil, nil)
	if err == nil {
		t.Fatal("expected an error for nil registry, got nil")
	}
}

func TestGetCAPublicKey_ShouldReturnSingleKey(t *testing.T) {
	t.Parallel()

	key := "ssh-ed25519 AAAA..."
	registry := &mockCAKeyRegistry{keys: []string{key}}

	svc, err := NewCAService(nil, registry)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got, err := svc.GetCAPublicKey(context.Background())
	if err != nil {
		t.Fatalf("expected no error from GetCAPublicKey, got %v", err)
	}
	if got != key {
		t.Errorf("got %q, want %q", got, key)
	}
}

func TestGetCAPublicKey_ShouldReturnMultipleKeysJoined(t *testing.T) {
	t.Parallel()

	keys := []string{"ssh-ed25519 AAAA...", "ssh-ed25519 BBBB..."}
	registry := &mockCAKeyRegistry{keys: keys}

	svc, err := NewCAService(nil, registry)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got, err := svc.GetCAPublicKey(context.Background())
	if err != nil {
		t.Fatalf("expected no error from GetCAPublicKey, got %v", err)
	}
	expected := "ssh-ed25519 AAAA...\nssh-ed25519 BBBB..."
	if got != expected {
		t.Errorf("got %q, want %q", got, expected)
	}
}

func TestGetCAPublicKey_ShouldErrorWhenNoActiveKeys(t *testing.T) {
	t.Parallel()

	registry := &mockCAKeyRegistry{keys: []string{}}

	svc, err := NewCAService(nil, registry)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	_, err = svc.GetCAPublicKey(context.Background())
	if err == nil {
		t.Fatal("expected an error for empty key list, got nil")
	}
}
