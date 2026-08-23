// Package service implements the server's business logic, independent of
// the HTTP transport layer.
package service

import (
	"context"
	"net/http"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/server/config"
)

// CAPublicKeyProvider exposes the CA's public key. CAService is the
// production implementation.
type CAPublicKeyProvider interface {
	GetCAPublicKey(ctx context.Context) (string, error)
}

// CAService signs and exposes information about the SSH certificate
// authority used to issue user, host, and service certificates.
type CAService struct {
	httpClient *http.Client
	// ca         string
	capubkey string
}

// NewCAService loads the CA private key from c.Signer.SSHKey and derives its
// public key, which is served via GetCAPublicKey.
func NewCAService(c *config.Config, httpClient *http.Client) (*CAService, error) {
	signer, err := ssh.ParsePrivateKey([]byte(c.Signer.SSHKey))
	if err != nil {
		return nil, err
	}

	return &CAService{
		httpClient: httpClient,
		capubkey: strings.Trim(
			string(ssh.MarshalAuthorizedKey(signer.PublicKey())),
			"\n",
		),
	}, nil
}

// GetCAPublicKey returns the CA's public key in authorized_keys format.
func (s *CAService) GetCAPublicKey(ctx context.Context) (string, error) {
	return s.capubkey, nil
}
