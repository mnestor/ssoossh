// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/pkg/crypto/sshutil"
)

type AgentI interface {
	HasKeys() bool
	ListCertificates() ([]*ssh.Certificate, error)
	LoadCA(string) bool
	CleanupAgent() (string, error)
	AddCertificate(KeyPairI) error
}
type Agent struct {
	*sshutil.Agent
	ca []ssh.PublicKey
}

func NewAgent() (*Agent, error) {
	a, err := sshutil.DialAgent()
	if err != nil {
		return nil, err
	}
	return &Agent{a, []ssh.PublicKey{}}, nil
}

func (a *Agent) HasKeys() bool {

	exists, err := a.Agent.HasKeys(
		sshutil.WithSignatureKey(a.ca),
		sshutil.WithRemoveExpiredCerts(time.Now()),
	)

	if err != nil {
		return false
	}
	return exists
}

func (a *Agent) ListCertificates() ([]*ssh.Certificate, error) {

	certs, err := a.Agent.ListCertificates(
		sshutil.WithSignatureKey(a.ca),
		sshutil.WithRemoveExpiredCerts(time.Now()),
	)

	if err != nil {
		return nil, err
	}

	// there should be only 1
	// unless they're running this command more than once within 30 seconds
	return certs, nil
}

func (a *Agent) LoadCA(ca string) bool {
	userKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(ca))
	if err != nil {
		return false
	}
	a.ca = append(a.ca, []ssh.PublicKey{userKey}...)
	return true
}

func (a *Agent) CleanupAgent() (string, error) {
	found, err := a.RemoveKeys(
		sshutil.WithSignatureKey(a.ca),
		sshutil.WithRemoveExpiredCerts(time.Now()),
	)

	if err != nil {
		return "", err
	}

	if found {
		return "Keys are removed", nil
	}

	return "No key signed by your CA present", nil
}

func (a *Agent) AddCertificate(k KeyPairI) error {
	return a.Agent.AddCertificate(
		"ssoossh",
		k.GetCertficiate(),
		k.GetPrivate(),
	)
}
