// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/pkg/crypto/sshutil"
)

type Agent struct {
	*sshutil.Agent
	ca []ssh.PublicKey
}

func GetAgent() (*Agent, error) {
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

func (a *Agent) LoadCA(ca string) {
	userKey, _, _, _, _ := ssh.ParseAuthorizedKey([]byte(ca))
	a.ca = append(a.ca, []ssh.PublicKey{userKey}...)
}

func (a *Agent) CleanupAgent() error {

	found, err := a.RemoveKeys(
		sshutil.WithSignatureKey(a.ca),
	)

	if err != nil {
		return err
	}
	if found {
		fmt.Fprintf(os.Stdout, "Keys are removed\n")
	} else {
		fmt.Fprintf(os.Stdout, "No key signed by your CA present\n")
	}

	return nil
}

func (a *Agent) AddCertificate(k *KeyPair) error {
	return a.Agent.AddCertificate("ssoossh", k.Cert, k.Private)
}
