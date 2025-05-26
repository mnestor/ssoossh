//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris
// +build aix darwin dragonfly freebsd linux netbsd openbsd solaris

package agent

import (
	"net"
	"os"

	"golang.org/x/crypto/ssh/agent"
)

// NewSSHAgent connects to the system SSH agent and returns an SSHAgent.
// On Unix, this uses SSH_AUTH_SOCK. On Windows, see sshagent_windows.go.
func NewSSHAgent() (Agent, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	return &SshAgent{
		agent: agent.NewClient(conn),
		conn:  conn,
	}, nil
}
