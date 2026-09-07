package signer

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// encryptedKeyPEM returns an Ed25519 private key encrypted under passphrase,
// in the OpenSSH container ssh-keygen produces.
func encryptedKeyPEM(t *testing.T, passphrase string) (string, ssh.PublicKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(passphrase))
	if err != nil {
		t.Fatalf("marshal encrypted key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("wrap public key: %v", err)
	}
	return string(pem.EncodeToMemory(block)), sshPub
}

// plainKeyPEM returns an unencrypted Ed25519 private key.
func plainKeyPEM(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return string(pem.EncodeToMemory(block))
}

func TestNewConfigKeySource_Passphrase(t *testing.T) {
	t.Parallel()

	encrypted, wantPub := encryptedKeyPEM(t, "hunter2")
	plain := plainKeyPEM(t)

	tests := []struct {
		name       string
		pem        string
		passphrase string
		wantErr    string
	}{
		{
			name:       "should decrypt a passphrase-protected key with the right passphrase",
			pem:        encrypted,
			passphrase: "hunter2",
		},
		{
			name:    "should say what to configure when an encrypted key has no passphrase",
			pem:     encrypted,
			wantErr: "passphrase protected: set ssh_key_passphrase_file",
		},
		{
			name:       "should reject a wrong passphrase",
			pem:        encrypted,
			passphrase: "wrong",
			wantErr:    "failed to parse CA private key",
		},
		{
			name: "should still accept an unencrypted key with no passphrase",
			pem:  plain,
		},
		{
			name:       "should reject a passphrase supplied for an unencrypted key",
			pem:        plain,
			passphrase: "hunter2",
			wantErr:    "failed to parse CA private key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ks, err := NewConfigKeySource(tt.pem, tt.passphrase)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, err := ks.Signer(context.Background()); err != nil {
				t.Fatalf("Signer: %v", err)
			}
		})
	}

	t.Run("should yield the signer matching the encrypted key", func(t *testing.T) {
		t.Parallel()
		ks, err := NewConfigKeySource(encrypted, "hunter2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got, err := ks.Signer(context.Background())
		if err != nil {
			t.Fatalf("Signer: %v", err)
		}
		if string(got.PublicKey().Marshal()) != string(wantPub.Marshal()) {
			t.Error("signer's public key does not match the key that was encrypted")
		}
	})
}
