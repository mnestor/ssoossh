package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// should report file as the backend and agent type for a FileAgent
func TestFileAgent_TypeAndBackend(t *testing.T) {
	t.Parallel()

	f := &FileAgent{}

	if got := f.Type(); got != AgentTypeFile {
		t.Errorf("Type() = %q, want %q", got, AgentTypeFile)
	}
	if got := f.Backend(); got != BackendFile {
		t.Errorf("Backend() = %q, want %q", got, BackendFile)
	}
}

// should create a FileAgent for a path with no existing key material
func TestNewFileAgent_WhenNoFilesExist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "id_ssoossh")

	ag, err := NewFileAgent(path)
	if err != nil {
		t.Fatalf("NewFileAgent() error = %v, want nil", err)
	}

	fa, ok := ag.(*FileAgent)
	if !ok {
		t.Fatalf("NewFileAgent() returned %T, want *FileAgent", ag)
	}
	if fa.HasPrivKey || fa.HasPubKey || fa.HasCert {
		t.Errorf("expected no existing key material, got HasPrivKey=%v HasPubKey=%v HasCert=%v", fa.HasPrivKey, fa.HasPubKey, fa.HasCert)
	}
}

// should write private key, public key, and certificate files when adding a keypair
func TestFileAgent_AddKeypair_WritesFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "id_ssoossh")

	ag, err := NewFileAgent(path)
	if err != nil {
		t.Fatalf("NewFileAgent() error = %v", err)
	}

	kp, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	if err := ag.AddKeypair(kp); err != nil {
		t.Fatalf("AddKeypair() error = %v", err)
	}

	for _, suffix := range []string{"", ".pub", "-cert.pub"} {
		if _, err := os.Stat(path + suffix); err != nil {
			t.Errorf("expected file %s to exist, got error: %v", path+suffix, err)
		}
	}
}

// should refuse to add or remove keys directly, since FileAgent is not a live agent
func TestFileAgent_AddAndRemove_Unsupported(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	f := &FileAgent{privKey: filepath.Join(dir, "id_ssoossh")}

	if err := f.Add(nil); err == nil {
		t.Error("Add() error = nil, want error for unsupported operation")
	}
}
