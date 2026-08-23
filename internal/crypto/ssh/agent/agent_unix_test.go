//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris
// +build aix darwin dragonfly freebsd linux netbsd openbsd solaris

package agent

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// should require SSH_AUTH_SOCK to be set, dial the socket it names, and wrap the connection as an OpenSSH-backed SshAgent
func TestNewOpenSSHAgent(t *testing.T) {
	t.Run("should error when SSH_AUTH_SOCK is not set", func(t *testing.T) {
		t.Setenv("SSH_AUTH_SOCK", "")

		if _, err := NewOpenSSHAgent(); err == nil {
			t.Error("NewOpenSSHAgent() error = nil, want error")
		}
	})

	t.Run("should error when the socket cannot be dialed", func(t *testing.T) {
		t.Setenv("SSH_AUTH_SOCK", filepath.Join(t.TempDir(), "does-not-exist.sock"))

		if _, err := NewOpenSSHAgent(); err == nil {
			t.Error("NewOpenSSHAgent() error = nil, want error")
		}
	})

	t.Run("should connect and return an SshAgent backed by the OpenSSH agent", func(t *testing.T) {
		// Not t.TempDir(): it embeds this subtest's 74-char name in the
		// path, which on macOS pushes the socket path past the 104-byte
		// sun_path limit and bind fails with EINVAL. MkdirTemp keeps it
		// short on every platform.
		dir, err := os.MkdirTemp("", "agent")
		if err != nil {
			t.Fatalf("os.MkdirTemp() error = %v", err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) }) //nolint:errcheck // test cleanup
		sockPath := filepath.Join(dir, "agent.sock")
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			t.Fatalf("net.Listen() error = %v", err)
		}
		t.Cleanup(func() { _ = ln.Close() }) //nolint:errcheck // test cleanup
		go func() {
			conn, err := ln.Accept()
			if err == nil {
				_ = conn.Close() //nolint:errcheck // test fixture, connection is discarded once dialed
			}
		}()

		t.Setenv("SSH_AUTH_SOCK", sockPath)

		got, err := NewOpenSSHAgent()
		if err != nil {
			t.Fatalf("NewOpenSSHAgent() error = %v", err)
		}
		t.Cleanup(func() { _ = got.Close() }) //nolint:errcheck // test cleanup

		sshAgent, ok := got.(*SshAgent)
		if !ok {
			t.Fatalf("NewOpenSSHAgent() returned %T, want *SshAgent", got)
		}
		if sshAgent.Backend() != BackendOpenSSHAgent {
			t.Errorf("Backend() = %q, want %q", sshAgent.Backend(), BackendOpenSSHAgent)
		}
	})
}

// should delegate to NewOpenSSHAgent on Unix
func TestNewSSHAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")

	if _, err := NewSSHAgent(); err == nil {
		t.Error("NewSSHAgent() error = nil, want error (mirrors NewOpenSSHAgent with SSH_AUTH_SOCK unset)")
	}
}
