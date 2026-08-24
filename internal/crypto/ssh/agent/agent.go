// Package agent provides a backend-agnostic way to manage an SSH keypair and
// certificate, backed either by a live ssh-agent (OpenSSH agent, Windows
// Pageant, or a WSL ssh-agent relay) or by files on disk. See the package
// README (README.md) for a full usage guide.
package agent

import (
	"errors"
	"net"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// Agent is the generic interface for managing an SSH keypair and certificate,
// regardless of whether the backing store is a running ssh-agent (OpenSSH
// agent or Windows Pageant) or a pair of files on disk. Callers should
// program against this interface and use Type()/Backend() only for
// diagnostics or backend-specific behavior (e.g. rendering ssh_config); they
// should not otherwise need to know which concrete implementation they hold.
type Agent interface {
	// Type returns the coarse classification of this agent: AgentTypeSsh for
	// any live agent connection (OpenSSH agent or Pageant), or AgentTypeFile
	// for file-backed storage.
	Type() string
	// Backend returns the specific backend in use, e.g. BackendOpenSSHAgent,
	// BackendPageant, or BackendFile. Useful for logging/diagnostics.
	Backend() string
	// List returns known identities. When filterByCA is true, only
	// ssh.Certificate identities signed by one of the trusted CAs (see
	// SetCA) are returned; when false, all identities are returned
	// unfiltered.
	List(filterByCA bool) ([]*ssh.PublicKey, error)
	// Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error)
	Add(key any) error
	Remove(key ssh.PublicKey) error
	RemoveAll() (int, error)
	Signers() ([]ssh.Signer, error)
	Close() error
	Agent() agent.Agent
	// SetCA registers one or more trusted CA public keys (authorized_keys
	// format or raw base64), in addition to any already registered.
	SetCA(cas ...string) error
	Certificates() ([]*ssh.Certificate, error)
	AddKeypair(keypair *keypair.SSHKeypair) error
	CleanupAgent() error
}

// SshAgent wraps communication with a live SSH agent (OpenSSH agent or
// Windows Pageant) on any OS.
type SshAgent struct {
	agent   agent.Agent
	conn    net.Conn // nil if using Pageant (Windows)
	cas     []ssh.PublicKey
	backend string
}

const (
	// AgentTypeFile classifies file-based backends (see FileAgent).
	AgentTypeFile = "file-agent"
	// AgentTypeSsh classifies any live ssh-agent backend (see SshAgent),
	// whether that's an OpenSSH agent or Windows Pageant.
	AgentTypeSsh = "ssh-agent"

	// BackendOpenSSHAgent identifies a connection to a standard OpenSSH agent
	// (SSH_AUTH_SOCK on Unix, the openssh-ssh-agent named pipe on Windows).
	BackendOpenSSHAgent = "openssh-agent"
	// BackendPageant identifies a connection to PuTTY's Pageant on Windows.
	BackendPageant = "pageant"
	// BackendWSLAgent identifies a connection to an ssh-agent running inside
	// WSL, reached via a relay named pipe on the Windows side (e.g.
	// wsl-ssh-agent-relay/npiperelay).
	BackendWSLAgent = "wsl-ssh-agent"
	// BackendFile identifies file-based key/certificate storage.
	BackendFile = "file"
)

// Type always returns AgentTypeSsh for a live agent connection. Use
// Backend() to tell OpenSSH agent, Pageant, and WSL relay connections apart.
func (a *SshAgent) Type() string {
	return AgentTypeSsh
}

// Backend reports which concrete ssh-agent implementation this SshAgent is
// talking to (OpenSSH agent or Pageant).
func (a *SshAgent) Backend() string {
	if a.backend == "" {
		return BackendOpenSSHAgent
	}
	return a.backend
}

// List returns the identities known to the agent. When filterByCA is true,
// only ssh.Certificate identities signed by one of the trusted CAs (see
// SetCA) are returned.
func (a *SshAgent) List(filterByCA bool) ([]*ssh.PublicKey, error) {
	if filterByCA && len(a.cas) == 0 {
		return nil, errors.New("CA public key is not set")
	}

	keys, err := a.agent.List()
	if err != nil {
		return nil, err
	}
	var pubs []*ssh.PublicKey
	for _, k := range keys {
		parsed, err := ssh.ParsePublicKey(k.Marshal())
		if err != nil {
			return nil, err
		}
		if !filterByCA {
			pubs = append(pubs, &parsed)
			continue
		}
		// Only include ssh.Certificate keys signed by one of the trusted CAs
		if cert, ok := parsed.(*ssh.Certificate); ok && cert.SignatureKey != nil {
			for _, ca := range a.cas {
				if publicKeysEqual(cert.SignatureKey, ca) {
					pubs = append(pubs, &parsed)
					break
				}
			}
		}
	}
	return pubs, nil
}

// Sign has the agent sign the data using the given key.
func (a *SshAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	return a.agent.Sign(key, data)
}

// Add adds a private key to the agent.
func (a *SshAgent) Add(key any) error {
	addedKey, ok := key.(agent.AddedKey)
	if !ok {
		return errors.New("unsupported key type: expected agent.AddedKey")
	}
	return a.agent.Add(addedKey)
}

// Close closes the connection to the SSH agent.
func (a *SshAgent) Close() error {
	if a.conn != nil {
		return a.conn.Close()
	}
	return nil
}

// Agent returns the underlying agent.Agent interface.
func (a *SshAgent) Agent() agent.Agent {
	return a.agent
}

// Remove removes all identities with the given public key.
func (a *SshAgent) Remove(key ssh.PublicKey) error {
	return a.agent.Remove(key)
}

// RemoveAll removes all identities.
func (a *SshAgent) RemoveAll() (int, error) {
	keys, err := a.agent.List()
	if err != nil {
		return -1, err
	}
	c := 0
	for _, key := range keys {
		if err := a.agent.Remove(key); err != nil {
			return -1, err
		}
		c++
	}
	return c, nil
}

// Signers returns signers for all the known keys.
func (a *SshAgent) Signers() ([]ssh.Signer, error) {
	return a.agent.Signers()
}

// CleanupAgent removes any certificate identities from the agent that are
// not time-valid or not signed by a trusted CA (see SetCA). Identities that
// aren't ssh.Certificate keys are left untouched. With no CAs registered it
// refuses to run rather than judging every certificate invalid and
// removing identities that may be perfectly good.
func (a *SshAgent) CleanupAgent() error {
	if len(a.cas) == 0 {
		return errors.New("refusing to clean up agent identities: no trusted CAs registered (call SetCA first)")
	}
	keys, err := a.agent.List()
	if err != nil {
		return err
	}

	for _, key := range keys {
		cert, ok := parseAgentCertificate(key)
		if !ok {
			continue
		}
		if !CertificateValid(cert, a.cas) {
			// Best-effort cleanup: keep trying the rest of the identities
			// even if one removal fails.
			_ = a.agent.Remove(key) //nolint:errcheck // best-effort cleanup, see comment above
		}
	}
	return nil
}

// parseAgentCertificate reparses an agent identity as an ssh.Certificate,
// reporting false for identities that don't parse or aren't certificates.
// Shared by List, CleanupAgent, and Certificates, which all walk the
// agent's identities looking for certificates.
func parseAgentCertificate(key *agent.Key) (*ssh.Certificate, bool) {
	parsed, err := ssh.ParsePublicKey(key.Marshal())
	if err != nil {
		return nil, false
	}
	cert, ok := parsed.(*ssh.Certificate)
	return cert, ok
}

// SetCA registers one or more trusted CA public keys, in addition to any
// already registered via previous calls.
func (a *SshAgent) SetCA(cas ...string) error {
	if len(cas) == 0 {
		return errors.New("at least one CA public key string is required")
	}
	parsed, err := parseCAPublicKeys(cas)
	if err != nil {
		return err
	}
	a.cas = append(a.cas, parsed...)
	return nil
}

// Certificates returns all ssh.Certificate public keys in the agent that are signed by any trusted CA.
func (a *SshAgent) Certificates() ([]*ssh.Certificate, error) {
	if len(a.cas) == 0 {
		return nil, errors.New("CA public key is not set")
	}

	keys, err := a.agent.List()
	if err != nil {
		return nil, err
	}
	var certs []*ssh.Certificate
	for _, k := range keys {
		cert, ok := parseAgentCertificate(k)
		if !ok {
			continue
		}
		if !CertificateValid(cert, a.cas) {
			continue
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, errors.New("no valid certificates found signed by the CA")
	}
	return certs, nil
}

// dialAgent connects to sock — a Unix domain socket path or, on Windows, a
// named pipe path — and wraps the connection as an SshAgent reporting the
// given backend. Shared by the OpenSSH-agent and WSL-relay constructors on
// both Unix (agent_unix.go) and Windows (agent_windows.go); Pageant is
// dialed differently and does not use this helper. The transport differs per
// platform, so the dial itself lives in dialSocket (agent_unix.go /
// agent_windows.go).
func dialAgent(sock, backend string) (Agent, error) {
	conn, err := dialSocket(sock)
	if err != nil {
		return nil, err
	}
	return &SshAgent{
		agent:   agent.NewClient(conn),
		conn:    conn,
		backend: backend,
	}, nil
}

// AddKeypair adds an SSHKeypair to the agent.
func (a *SshAgent) AddKeypair(keypair *keypair.SSHKeypair) error {
	addedKey := agent.AddedKey{
		PrivateKey:  keypair.Private(),
		Comment:     "ssoossh",
		Certificate: keypair.Certificate(),
	}

	return a.agent.Add(addedKey)
}
