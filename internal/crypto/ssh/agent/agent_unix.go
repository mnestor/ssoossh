//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris
// +build aix darwin dragonfly freebsd linux netbsd openbsd solaris

package agent

import (
	"errors"
	"net"
	"os"

	"golang.org/x/crypto/ssh/agent"
)

// NewSSHAgent connects to the system SSH agent and returns an SSHAgent.
// On Unix, this uses SSH_AUTH_SOCK. On Windows, see agent_windows.go, which
// also supports Pageant.
func NewSSHAgent() (Agent, error) {
	return NewOpenSSHAgent()
}

// NewOpenSSHAgent connects to the OpenSSH agent via SSH_AUTH_SOCK.
func NewOpenSSHAgent() (Agent, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, errors.New("SSH_AUTH_SOCK is not set")
	}
	conn, err := net.Dial("unix", sock) //nolint:gosec // standard local ssh-agent connection via SSH_AUTH_SOCK, not attacker-influenced network input
	if err != nil {
		return nil, err
	}
	return &SshAgent{
		agent:   agent.NewClient(conn),
		conn:    conn,
		backend: BackendOpenSSHAgent,
	}, nil
}
