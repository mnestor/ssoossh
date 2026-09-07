package signer

import (
	"context"
	"fmt"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// AgentParams configures a connection to an ssh-agent holding the CA key.
type AgentParams struct {
	// Socket is the agent's Unix socket path.
	Socket string
	// Fingerprint is the SHA256 fingerprint of the CA key to use, in
	// ssh.FingerprintSHA256 form. Empty means "the only key the agent
	// holds", which is an error when it holds more than one.
	Fingerprint string
}

// AgentKeySource is a CAKeySource whose private key lives in an ssh-agent.
//
// This is the only key source where the private key is neither in this
// process's memory nor reachable from it: the agent protocol has no export
// operation, so a compromise of ssoosshd is a signing oracle rather than a
// key disclosure. That is the property a PKCS#11 token gives, reached
// without loading a PKCS#11 module into this process -- no cgo, no dlopen,
// and no vendor C++ library sharing an address space with the signer.
//
// It also reaches keys the token path cannot. An agent signs Ed25519
// happily, where crypto11 cannot, and `ssh-add -s <module>` puts a PKCS#11
// token behind an agent -- so an HSM-backed CA key is reachable from a
// binary built with CGO_ENABLED=0.
//
// What it does not give is least authority: the agent signs whatever blob
// it is handed, so a compromised signer can still issue any certificate the
// CA could. See the security model documentation.
type AgentKeySource struct {
	params AgentParams

	// mu guards the connection, client and cached signer. Signer is called
	// once per signing job, from pub/sub handler goroutines.
	mu   sync.Mutex
	conn net.Conn
	// ag is created once per connection and reused. agent.NewClient starts
	// a read loop goroutine on the connection, so building a fresh client
	// per call would leave two goroutines racing to read the same socket
	// and stealing each other's replies -- which deadlocks rather than
	// erroring.
	ag     agent.Agent
	signer ssh.Signer
}

// NewAgentKeySource connects to the agent and resolves the CA key, so a
// missing agent or absent key fails at startup like every other key source
// rather than at the first certificate.
func NewAgentKeySource(p AgentParams) (*AgentKeySource, error) {
	s := &AgentKeySource{params: p}
	if err := s.connect(); err != nil {
		return nil, err
	}
	return s, nil
}

// connect dials the agent and resolves the CA key. The caller holds mu, or
// is the constructor.
func (s *AgentKeySource) connect() error {
	conn, err := net.Dial("unix", s.params.Socket)
	if err != nil {
		return fmt.Errorf("connect to ssh-agent at %s: %w", s.params.Socket, err)
	}

	ag := agent.NewClient(conn)
	signer, err := selectAgentKey(ag, s.params.Fingerprint)
	if err != nil {
		_ = conn.Close()
		return err
	}

	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.conn, s.ag, s.signer = conn, ag, signer
	return nil
}

// selectAgentKey picks the CA key out of what the agent holds and applies
// the shared algorithm policy.
func selectAgentKey(ag agent.Agent, fingerprint string) (ssh.Signer, error) {
	signers, err := ag.Signers()
	if err != nil {
		return nil, fmt.Errorf("list ssh-agent keys: %w", err)
	}
	if len(signers) == 0 {
		return nil, fmt.Errorf("ssh-agent holds no keys: load the CA key with ssh-add, or ssh-add -s <pkcs11 module> for a token")
	}

	// No fingerprint configured: accept the agent's single key, and refuse
	// to guess when there is more than one. Picking the first would make
	// the CA depend on the order keys happened to be added.
	if fingerprint == "" {
		if len(signers) != 1 {
			return nil, fmt.Errorf("ssh-agent holds %d keys and ssh_key_agent.key_fingerprint is not set: name the CA key rather than leaving the choice to key order", len(signers))
		}
		return gateCASigner(signers[0], ed25519Allowed)
	}

	for _, candidate := range signers {
		if ssh.FingerprintSHA256(candidate.PublicKey()) == fingerprint {
			return gateCASigner(candidate, ed25519Allowed)
		}
	}

	held := make([]string, 0, len(signers))
	for _, candidate := range signers {
		held = append(held, ssh.FingerprintSHA256(candidate.PublicKey()))
	}
	return nil, fmt.Errorf("no key in the ssh-agent matches ssh_key_agent.key_fingerprint %q; the agent holds %v", fingerprint, held)
}

// Signer implements CAKeySource.
//
// Unlike the config- and HSM-backed sources, this one revalidates before
// handing the signer back, and reconnects if the agent has gone away. That
// costs one round trip over a Unix socket per certificate, which is orders
// of magnitude below the signature itself, and buys recovery from an agent
// restart without restarting ssoosshd. The HSM path has no equivalent and
// stays broken until the process is restarted -- see
// docs/proposals/hsm-cloud-readiness.md, finding 3.
func (s *AgentKeySource) Signer(context.Context) (ssh.Signer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.ag.Signers(); err == nil {
		return s.signer, nil
	}

	// The agent went away. One reconnect attempt: if the agent is back but
	// the key was never re-added, the error from connect says so, which is
	// the actionable message.
	if err := s.connect(); err != nil {
		return nil, fmt.Errorf("ssh-agent connection lost and could not be re-established: %w", err)
	}
	return s.signer, nil
}

// Close releases the agent connection; bootstrap runs it on shutdown.
func (s *AgentKeySource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn, s.ag, s.signer = nil, nil, nil
	return err
}
