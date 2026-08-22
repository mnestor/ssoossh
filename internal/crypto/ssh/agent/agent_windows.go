//go:build windows
// +build windows

package agent

import (
	"os"

	"github.com/kbolino/pageant"

	"golang.org/x/crypto/ssh/agent"
)

// defaultWSLAgentPipe is the named pipe exposed by common WSL ssh-agent
// relay tools (e.g. wsl-ssh-agent-relay, npiperelay-based bridges) that
// forward a native Windows connection into an ssh-agent running inside WSL.
// It can be overridden via the WSL_SSH_AGENT_PIPE environment variable for
// setups that use a different pipe name.
const defaultWSLAgentPipe = `\\.\pipe\wsl-ssh-agent`

// NewSSHAgent connects to the system SSH agent on Windows, trying (in
// order) Pageant, the native OpenSSH agent named pipe, and a WSL ssh-agent
// relay named pipe (e.g. wsl-ssh-agent-relay/npiperelay bridging into a WSL
// distro's agent). Use Backend() on the returned Agent to find out which one
// was used.
func NewSSHAgent() (Agent, error) {
	if a, err := NewPageantAgent(); err == nil {
		return a, nil
	}
	if a, err := NewOpenSSHAgent(); err == nil {
		return a, nil
	}
	return NewWSLAgent()
}

// NewPageantAgent connects specifically to PuTTY's Pageant.
func NewPageantAgent() (Agent, error) {
	pageantAgent, err := pageant.NewConn()
	if err != nil {
		return nil, err
	}
	return &SshAgent{
		agent:   agent.NewClient(pageantAgent),
		conn:    nil,
		backend: BackendPageant,
	}, nil
}

// NewOpenSSHAgent connects to the OpenSSH agent named pipe on Windows.
func NewOpenSSHAgent() (Agent, error) {
	return dialAgent(`\\.\pipe\openssh-ssh-agent`, BackendOpenSSHAgent)
}

// NewWSLAgent connects to a WSL ssh-agent relay named pipe, letting a native
// Windows process use an ssh-agent running inside a WSL distro. The pipe
// name defaults to defaultWSLAgentPipe and can be overridden with the
// WSL_SSH_AGENT_PIPE environment variable.
func NewWSLAgent() (Agent, error) {
	sock := os.Getenv("WSL_SSH_AGENT_PIPE")
	if sock == "" {
		sock = defaultWSLAgentPipe
	}
	return dialAgent(sock, BackendWSLAgent)
}
