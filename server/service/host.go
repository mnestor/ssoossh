package service

import (
	"context"
	"errors"

	"github.com/mnestor/ssoossh/server/config"
)

// HostProvider handles host-certificate renewal and principal-mapping
// sync. HostService is the production implementation.
type HostProvider interface {
	Renew(ctx context.Context, hostname string, existingCert string, newPublicKey string) (certificate string, err error)
	SyncPrincipals(ctx context.Context, hostname string) (principals string, err error)
}

// HostService handles the two host-certificate operations that don't go
// through the web UI approval flow in service/certrequest.go:
//   - Renew, authenticated by the existing valid host certificate itself
//     (hosts rotate keys on their own schedule)
//   - SyncPrincipals, which `host sync` pulls down and writes locally for
//     sshd's AuthorizedPrincipalsCommand (see docs/ssoossh-context.md,
//     "Principal mapping")
//
// First issuance (`host sign`) goes through CertRequestService instead,
// since it requires the OIDC approval chain — a human vouching for the
// machine is the anti-MITM control (see docs/ssoossh-context.md,
// "Certificate types").
type HostService struct {
	config *config.Config
	// TODO: db *gorm.DB, ca signing dependency.
}

// NewHostService constructs a HostService.
func NewHostService(c *config.Config) (*HostService, error) {
	return &HostService{config: c}, nil
}

// Renew reissues a host certificate for hostname, authenticated by the
// still-valid existingCert presented by the caller (see
// middleware.HostCertAuthMiddleware) rather than a fresh OIDC approval.
//
// TODO: decide the renewal grace window for host certs that expire before
// `host renew` runs (open question in docs/ssoossh-context.md).
func (s *HostService) Renew(ctx context.Context, hostname string, existingCert string, newPublicKey string) (certificate string, err error) {
	return "", errors.New("not implemented")
}

// SyncPrincipals returns the current principal mapping for hostname, for
// `host sync` to write locally. The client never resolves cache staleness
// itself — that's the host admin's call via file mtime or `host sync` exit
// status.
func (s *HostService) SyncPrincipals(ctx context.Context, hostname string) (principals string, err error) {
	// TODO: load model.HostMapping for hostname.
	return "", errors.New("not implemented")
}
