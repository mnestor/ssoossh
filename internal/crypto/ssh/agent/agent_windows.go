//go:build windows
// +build windows

package agent

import (
	"net"

	"github.com/kbolino/pageant"

	"golang.org/x/crypto/ssh/agent"
)

// NewSSHAgent connects to the system SSH agent (including Pageant on Windows) and returns an SSHAgent.
func NewSSHAgent() (Agent, error) {
	// Try Pageant first
	pageantAgent, err := pageant.NewConn()
	if err == nil {
		return &SshAgent{
			agent: agent.NewClient(pageantAgent),
			conn:  nil,
		}, nil
	}

	// Fallback to OpenSSH agent named pipe
	sock := `\\.\pipe\openssh-ssh-agent`
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}
	return &SshAgent{
		agent: agent.NewClient(conn),
		conn:  conn,
	}, nil
}
