package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
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
	db     *gorm.DB
	// TODO: ca signing dependency.
}

// NewHostService constructs a HostService.
func NewHostService(c *config.Config, db *gorm.DB) (*HostService, error) {
	return &HostService{config: c, db: db}, nil
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
	var mapping model.HostMapping
	if err := s.db.WithContext(ctx).First(&mapping, "hostname = ?", hostname).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("principal mapping for %q not found", hostname)
		}
		return "", fmt.Errorf("failed to load principal mapping for %q: %w", hostname, err)
	}
	return mapping.Principals, nil
}
