package service

import (
	"context"
	"errors"

	"github.com/mnestor/ssoossh/server/config"
)

// EnrollmentProvider redeems an enrollment code into a signed certificate.
// EnrollmentService is the production implementation.
type EnrollmentProvider interface {
	Retrieve(ctx context.Context, code string) (certificate string, err error)
}

// EnrollmentService redeems an approved model.Enrollment (created by
// CertRequestService.Approve for a CertificateTypeService request) into a
// signed certificate. `service retrieve` posts only the enrollment code —
// never a public key — so a stolen code can't be paired with an attacker's
// keypair (see docs/dev/ssoossh-context.md, "Service enrollment").
type EnrollmentService struct {
	config *config.Config
	// TODO: db *gorm.DB, ca signing dependency.
}

// NewEnrollmentService constructs an EnrollmentService.
func NewEnrollmentService(c *config.Config) (*EnrollmentService, error) {
	return &EnrollmentService{config: c}, nil
}

// Retrieve signs and returns a service certificate for the enrollment
// identified by code, using the public key and option set stored at
// approval time.
//
// TODO: open question (docs/dev/ssoossh-context.md) — should this require
// proof-of-possession (a server challenge the caller signs with the private
// key)? Currently a stolen code yields an unusable certificate but can
// still burn issuances and reveal the option set.
func (s *EnrollmentService) Retrieve(ctx context.Context, code string) (certificate string, err error) {
	return "", errors.New("not implemented")
}
