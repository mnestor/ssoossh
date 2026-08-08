package service

import (
	"context"
	"errors"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// NewCertRequestParams are the client-supplied inputs to CreateRequest.
// RequestedOptions is narrowed by server config before anything is shown in
// the web UI (server config is the outer bound — see root CLAUDE.md Hard
// Constraints).
type NewCertRequestParams struct {
	Type      model.CertificateType
	PublicKey string
	Hostname  string // set for CertificateTypeHost only
	SourceIP  string
}

// CertRequestProvider manages the pending-approval lifecycle for
// certificate requests. CertRequestService is the production
// implementation.
type CertRequestProvider interface {
	CreateRequest(ctx context.Context, p NewCertRequestParams) (requestID string, err error)
	ListPending(ctx context.Context) ([]model.CertificateRequest, error)
	Approve(ctx context.Context, requestID string, identity *Identity) (certificate string, err error)
	Deny(ctx context.Context, requestID string) error
	Wait(ctx context.Context, requestID string) (status model.CertificateRequestStatus, certificate string, err error)
}

// CertRequestService manages the pending-approval lifecycle shared by all
// three certificate types: a client creates a request (`ssh login`,
// `host sign`, `service enroll`), the web UI lists/approves/denies it, and
// the client observes the outcome over SSE.
//
// Approving a request behaves differently per Type:
//   - user, host: sign and persist a model.Certificate immediately
//   - service: create a model.Enrollment instead (see service/enrollment.go) —
//     the certificate itself isn't issued until `service retrieve`
type CertRequestService struct {
	config *config.Config
	// TODO: db *gorm.DB, ca signing dependency (reuse/extend CAService),
	// and whatever broker backs Wait (see docs/ssoossh-context.md open
	// questions on proof-of-possession, and the SSE handler stub in
	// server/controller/certrequests.go).
}

// NewCertRequestService constructs a CertRequestService.
func NewCertRequestService(c *config.Config) (*CertRequestService, error) {
	return &CertRequestService{config: c}, nil
}

// CreateRequest persists a new pending model.CertificateRequest for p and
// returns its ID, which the client then waits on via Wait/SSE and the web
// UI resolves via Approve/Deny.
func (s *CertRequestService) CreateRequest(ctx context.Context, p NewCertRequestParams) (requestID string, err error) {
	// TODO: apply s.config.CertOptions as the outer bound on requested
	// options, persist a model.CertificateRequest with
	// model.CertificateRequestStatusPending.
	return "", errors.New("not implemented")
}

// ListPending returns the pending requests visible to the approving user in
// the web UI.
//
// TODO: decide the visibility rule — all pending requests, or only ones the
// current user is entitled to approve (see docs/ssoossh-context.md open
// question on host-admin scope).
func (s *CertRequestService) ListPending(ctx context.Context) ([]model.CertificateRequest, error) {
	return nil, errors.New("not implemented")
}

// Approve marks requestID approved by identity and, per its Type, either
// issues a certificate (user, host) or creates an Enrollment (service).
// Returns the certificate in PEM/authorized-format for user/host requests,
// or empty for service requests (retrieved later via `service retrieve`).
func (s *CertRequestService) Approve(ctx context.Context, requestID string, identity *Identity) (certificate string, err error) {
	// TODO: load the request, compute lifetime per policy (see
	// docs/ssoossh-context.md "Certificate lifetime policy" — default deny,
	// matched rules intersect to the shortest lifetime / narrowest
	// principal set), sign with the CA for user/host, or create a
	// model.Enrollment for service, persist the result, mark the request
	// resolved, and notify anything waiting in Wait.
	return "", errors.New("not implemented")
}

// Deny marks requestID denied and notifies anything waiting in Wait.
func (s *CertRequestService) Deny(ctx context.Context, requestID string) error {
	// TODO: mark the request denied.
	return errors.New("not implemented")
}

// Wait blocks until requestID resolves (approved/denied/expired), for the
// SSE handler to relay to the client.
//
// TODO: needs the pub/sub or channel-based broker discussed but not yet
// designed — see server/controller/certrequests.go's SSE handler stub.
func (s *CertRequestService) Wait(ctx context.Context, requestID string) (status model.CertificateRequestStatus, certificate string, err error) {
	return "", "", errors.New("not implemented")
}
