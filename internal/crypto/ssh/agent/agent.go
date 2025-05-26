package agent

import (
	"encoding/base64"
	"errors"
	"net"
	"strings"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

// Agent defines the interface for an SSH agent, matching the methods implemented by FileAgent.
type Agent interface {
	Type() string
	List() ([]*ssh.PublicKey, error)
	// Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error)
	Add(key interface{}) error
	Remove(key ssh.PublicKey) error
	RemoveAll() (int, error)
	Signers() ([]ssh.Signer, error)
	Close() error
	Agent() agent.Agent
	SetCA(ca string) error
	Certificates() ([]*ssh.Certificate, error)
	AddKeypair(keypair *keypair.SshKeypair) error
	CleanupAgent() error
}

// SshAgent wraps communication with an SSH agent on any OS.
type SshAgent struct {
	agent agent.Agent
	conn  net.Conn // nil if using Pageant (Windows)
	ca    ssh.PublicKey
}

const (
	AgentTypeFile = "file-agent"
	AgentTypeSsh  = "ssh-agent"
)

func (a *SshAgent) Type() string {
	return AgentTypeSsh
}

// List returns the identities known to the agent.
func (a *SshAgent) List() ([]*ssh.PublicKey, error) {
	if a.ca == nil {
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
		// If CA is set, only include ssh.Certificate keys signed by the CA
		if cert, ok := parsed.(*ssh.Certificate); ok && cert.SignatureKey != nil {
			if publicKeysEqual(cert.SignatureKey, a.ca) {
				pubs = append(pubs, &parsed)
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
func (a *SshAgent) Add(key interface{}) error {
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
	keys, _ := a.agent.List()
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

func (a *SshAgent) CleanupAgent() error {
	keys, err := a.agent.List()
	if err != nil {
		return err
	}

	for _, key := range keys {
		parsed, err := ssh.ParsePublicKey(key.Marshal())
		if err != nil {
			continue
		}
		cert, ok := parsed.(*ssh.Certificate)
		if !ok {
			continue
		}
		if !CertificateValid(cert, a.ca) {
			a.agent.Remove(key)
		}
	}
	return nil
}

func (a *SshAgent) SetCA(caStr string) error {
	if caStr == "" {
		return errors.New("CA public key string cannot be empty")
	}
	caStr = strings.TrimSpace(caStr)
	pub, _, _, rest, err := ssh.ParseAuthorizedKey([]byte(caStr))
	if err != nil || len(rest) > 0 {
		// Try parsing as raw base64
		data, err2 := base64.StdEncoding.DecodeString(caStr)
		if err2 != nil {
			return errors.New("failed to parse CA public key string")
		}
		pub, err = ssh.ParsePublicKey(data)
		if err != nil {
			return errors.New("failed to parse CA public key from base64")
		}
	}
	a.ca = pub
	return nil
}

// Certificates returns all ssh.Certificate public keys in the agent that are signed by the CA.
func (a *SshAgent) Certificates() ([]*ssh.Certificate, error) {
	if a.ca == nil {
		return nil, errors.New("CA public key is not set")
	}

	keys, err := a.agent.List()
	if err != nil {
		return nil, err
	}
	var certs []*ssh.Certificate
	for _, k := range keys {
		parsed, err := ssh.ParsePublicKey(k.Marshal())
		if err != nil {
			continue
		}
		cert, ok := parsed.(*ssh.Certificate)
		if !ok {
			continue
		}
		if !CertificateValid(cert, a.ca) {
			continue
		}
		certs = append(certs, cert)
	}
	if len(certs) == 0 {
		return nil, errors.New("no valid certificates found signed by the CA")
	}
	return certs, nil
}

// AddKeypair adds an SshKeypair to the agent.
func (a *SshAgent) AddKeypair(keypair *keypair.SshKeypair) error {
	addedKey := agent.AddedKey{
		PrivateKey:  keypair.Private(),
		Comment:     "ssoossh",
		Certificate: keypair.Certificate(),
	}

	return a.agent.Add(addedKey)
}
