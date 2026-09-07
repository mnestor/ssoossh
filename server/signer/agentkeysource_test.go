package signer

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// startAgent runs an in-process ssh-agent on a Unix socket holding keys, and
// returns the socket path. No external ssh-agent binary and no mock: this is
// x/crypto's own keyring behind the real agent wire protocol, which is what
// AgentKeySource talks to in production.
func startAgent(t *testing.T, keys ...any) string {
	t.Helper()
	keyring := agent.NewKeyring()
	for _, k := range keys {
		if err := keyring.Add(agent.AddedKey{PrivateKey: k}); err != nil {
			t.Fatalf("add key to keyring: %v", err)
		}
	}

	// Short directory: a Unix socket path has a ~104 byte limit, which
	// t.TempDir's name can approach on some systems.
	sock := filepath.Join(t.TempDir(), "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = agent.ServeAgent(keyring, conn)
			}()
		}
	}()
	return sock
}

func mustECDSA(t *testing.T, c elliptic.Curve) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(c, rand.Reader)
	if err != nil {
		t.Fatalf("generate ecdsa: %v", err)
	}
	return k
}

func mustEd25519(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	return priv
}

func fingerprintOf(t *testing.T, key any) string {
	t.Helper()
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("wrap key: %v", err)
	}
	return ssh.FingerprintSHA256(signer.PublicKey())
}

func TestNewAgentKeySource_KeySelection(t *testing.T) {
	t.Parallel()

	ec256 := mustECDSA(t, elliptic.P256())
	ec384 := mustECDSA(t, elliptic.P384())

	t.Run("should use the only key when no fingerprint is configured", func(t *testing.T) {
		t.Parallel()
		ks, err := NewAgentKeySource(AgentParams{Socket: startAgent(t, ec384)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer ks.Close()
		s, err := ks.Signer(context.Background())
		if err != nil {
			t.Fatalf("Signer: %v", err)
		}
		if s.PublicKey().Type() != ssh.KeyAlgoECDSA384 {
			t.Errorf("key type = %s, want %s", s.PublicKey().Type(), ssh.KeyAlgoECDSA384)
		}
	})

	t.Run("should select by fingerprint when the agent holds several", func(t *testing.T) {
		t.Parallel()
		sock := startAgent(t, ec256, ec384)
		want := fingerprintOf(t, ec384)
		ks, err := NewAgentKeySource(AgentParams{Socket: sock, Fingerprint: want})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer ks.Close()
		s, _ := ks.Signer(context.Background())
		if got := ssh.FingerprintSHA256(s.PublicKey()); got != want {
			t.Errorf("selected %s, want %s", got, want)
		}
	})

	t.Run("should refuse to guess when several keys and no fingerprint", func(t *testing.T) {
		t.Parallel()
		_, err := NewAgentKeySource(AgentParams{Socket: startAgent(t, ec256, ec384)})
		if err == nil || !strings.Contains(err.Error(), "key_fingerprint is not set") {
			t.Fatalf("error = %v, want it to name key_fingerprint", err)
		}
	})

	t.Run("should report the keys it did find when the fingerprint misses", func(t *testing.T) {
		t.Parallel()
		_, err := NewAgentKeySource(AgentParams{
			Socket: startAgent(t, ec256), Fingerprint: "SHA256:nothinglikethis",
		})
		if err == nil || !strings.Contains(err.Error(), "the agent holds") {
			t.Fatalf("error = %v, want it to list the agent's keys", err)
		}
	})

	t.Run("should reject an agent holding no keys", func(t *testing.T) {
		t.Parallel()
		_, err := NewAgentKeySource(AgentParams{Socket: startAgent(t)})
		if err == nil || !strings.Contains(err.Error(), "holds no keys") {
			t.Fatalf("error = %v, want it to say the agent is empty", err)
		}
	})

	t.Run("should fail at construction when there is no agent", func(t *testing.T) {
		t.Parallel()
		_, err := NewAgentKeySource(AgentParams{Socket: filepath.Join(t.TempDir(), "absent.sock")})
		if err == nil || !strings.Contains(err.Error(), "connect to ssh-agent") {
			t.Fatalf("error = %v, want a connection error naming the socket", err)
		}
	})
}

func TestNewAgentKeySource_AlgorithmPolicy(t *testing.T) {
	t.Parallel()

	rsa2048, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa: %v", err)
	}
	// Deliberately weak: the point of the case below is that gateCASigner
	// rejects it.
	//nolint:gosec // G403: an under-strength key is the input under test.
	rsa1024, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generate rsa: %v", err)
	}

	t.Run("should accept ed25519, which the HSM path cannot", func(t *testing.T) {
		t.Parallel()
		ks, err := NewAgentKeySource(AgentParams{Socket: startAgent(t, mustEd25519(t))})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer ks.Close()
		s, _ := ks.Signer(context.Background())
		if s.PublicKey().Type() != ssh.KeyAlgoED25519 {
			t.Errorf("key type = %s, want %s", s.PublicKey().Type(), ssh.KeyAlgoED25519)
		}
	})

	t.Run("should reject an RSA key below 2048 bits", func(t *testing.T) {
		t.Parallel()
		_, err := NewAgentKeySource(AgentParams{Socket: startAgent(t, rsa1024)})
		if err == nil || !strings.Contains(err.Error(), "must be at least 2048") {
			t.Fatalf("error = %v, want a bit-length rejection", err)
		}
	})

	// The trap this whole gate exists for: an unconstrained agent RSA
	// signer offers ssh-rsa first, which is SHA-1.
	t.Run("should sign RSA with SHA-2, never SHA-1", func(t *testing.T) {
		t.Parallel()
		ks, err := NewAgentKeySource(AgentParams{Socket: startAgent(t, rsa2048)})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		defer ks.Close()
		caSigner, _ := ks.Signer(context.Background())

		userPub, err := ssh.NewPublicKey(mustEd25519(t).Public())
		if err != nil {
			t.Fatalf("wrap user key: %v", err)
		}
		cert := &ssh.Certificate{
			Key: userPub, Serial: 1, CertType: ssh.UserCert,
			KeyId: "t", ValidPrincipals: []string{"alice"},
			ValidBefore: ssh.CertTimeInfinity,
		}
		if err := cert.SignCert(rand.Reader, caSigner); err != nil {
			t.Fatalf("SignCert: %v", err)
		}
		if cert.Signature.Format == ssh.KeyAlgoRSA {
			t.Fatal("certificate was signed with SHA-1 ssh-rsa")
		}
		if cert.Signature.Format != ssh.KeyAlgoRSASHA512 {
			t.Errorf("signature format = %s, want %s", cert.Signature.Format, ssh.KeyAlgoRSASHA512)
		}
	})
}

func TestAgentKeySource_ShouldSignAndVerifyACertificate(t *testing.T) {
	t.Parallel()

	caKey := mustECDSA(t, elliptic.P384())
	ks, err := NewAgentKeySource(AgentParams{Socket: startAgent(t, caKey)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ks.Close()

	caSigner, err := ks.Signer(context.Background())
	if err != nil {
		t.Fatalf("Signer: %v", err)
	}
	userPub, err := ssh.NewPublicKey(mustEd25519(t).Public())
	if err != nil {
		t.Fatalf("wrap user key: %v", err)
	}
	cert := &ssh.Certificate{
		Key: userPub, Serial: 1, CertType: ssh.UserCert,
		KeyId: "t", ValidPrincipals: []string{"alice"},
		ValidBefore: ssh.CertTimeInfinity,
	}
	if err := cert.SignCert(rand.Reader, caSigner); err != nil {
		t.Fatalf("SignCert: %v", err)
	}
	checker := &ssh.CertChecker{IsUserAuthority: func(k ssh.PublicKey) bool {
		return string(k.Marshal()) == string(caSigner.PublicKey().Marshal())
	}}
	if err := checker.CheckCert("alice", cert); err != nil {
		t.Errorf("CheckCert: %v", err)
	}
}

func TestAgentKeySource_Close_ShouldBeIdempotent(t *testing.T) {
	t.Parallel()

	ks, err := NewAgentKeySource(AgentParams{Socket: startAgent(t, mustECDSA(t, elliptic.P256()))})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := ks.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := ks.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}
