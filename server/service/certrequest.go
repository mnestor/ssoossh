package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// NewCertRequestParams are the client-supplied inputs to CreateRequest.
// RequestedOptions is narrowed by server config before anything is shown in
// the web UI (server config is the outer bound — see root CLAUDE.md Hard
// Constraints). That narrowing isn't implemented yet (see Approve) — for
// now RequestedOptions is stored as submitted.
type NewCertRequestParams struct {
	Type             model.CertificateType
	PublicKey        string
	Hostname         string // set for CertificateTypeHost only
	SourceIP         string
	RequestedOptions RequestedOptions
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

// requestOutcome is what Approve/Deny hands to a goroutine blocked in Wait.
type requestOutcome struct {
	status      model.CertificateRequestStatus
	certificate string
}

// CertRequestService manages the pending-approval lifecycle shared by all
// three certificate types: a client creates a request (`ssh login`,
// `host sign`, `service enroll`) and holds an SSE connection open on the
// same call waiting for it to resolve (see server/controller/certrequests.go);
// the web UI lists/approves/denies it out-of-band, which is what unblocks
// that waiting connection via the in-process broker below.
//
// Approving a request behaves differently per Type:
//   - user, host: sign and persist a model.Certificate immediately
//   - service: create a model.Enrollment instead (see service/enrollment.go) —
//     the certificate itself isn't issued until `service retrieve`
//
// The broker (waiters, below) is in-process only, matching the v1
// single-process deployment target noted in docs/ssoossh-context.md; a
// multi-instance deployment would need this replaced with something
// shared (e.g. LISTEN/NOTIFY on Postgres, or polling the DB).
type CertRequestService struct {
	config     *config.Config
	db         *gorm.DB
	keyIDTmpls *keyIDTemplates

	mu      sync.Mutex
	waiters map[string]chan requestOutcome
	// TODO: ca signing dependency (reuse/extend CAService — it currently
	// only exposes GetCAPublicKey, not signing) and the lifetime-policy
	// computation (see docs/ssoossh-context.md "Certificate lifetime
	// policy") are still needed before Approve can actually issue a
	// certificate.
}

// NewCertRequestService constructs a CertRequestService, parsing
// c.CertOptions' per-type key ID templates (see
// docs/certificate-keyid-template.md) so a bad template fails startup.
func NewCertRequestService(c *config.Config, db *gorm.DB) (*CertRequestService, error) {
	keyIDTmpls, err := newKeyIDTemplates(c.CertOptions)
	if err != nil {
		return nil, err
	}

	return &CertRequestService{
		config:     c,
		db:         db,
		keyIDTmpls: keyIDTmpls,
		waiters:    make(map[string]chan requestOutcome),
	}, nil
}

// CreateRequest persists a new pending model.CertificateRequest for p and
// returns its ID, which the client then waits on via Wait and the web UI
// resolves via Approve/Deny.
func (s *CertRequestService) CreateRequest(ctx context.Context, p NewCertRequestParams) (requestID string, err error) {
	optionsJSON, err := json.Marshal(p.RequestedOptions)
	if err != nil {
		return "", fmt.Errorf("failed to encode requested options: %w", err)
	}

	req := model.CertificateRequest{
		ID:               uuid.NewString(),
		Type:             p.Type,
		PublicKey:        p.PublicKey,
		Hostname:         p.Hostname,
		RequestedOptions: string(optionsJSON),
		SourceIP:         p.SourceIP,
		Status:           model.CertificateRequestStatusPending,
		CreatedAt:        time.Now(),
	}

	if err := s.db.WithContext(ctx).Create(&req).Error; err != nil {
		return "", fmt.Errorf("failed to persist certificate request: %w", err)
	}

	s.registerWaiter(req.ID)

	return req.ID, nil
}

// ListPending returns the pending requests visible to the approving user in
// the web UI.
//
// TODO: decide the visibility rule — all pending requests, or only ones the
// current user is entitled to approve (see docs/ssoossh-context.md open
// question on host-admin scope).
func (s *CertRequestService) ListPending(ctx context.Context) ([]model.CertificateRequest, error) {
	var requests []model.CertificateRequest
	err := s.db.WithContext(ctx).
		Where("status = ?", model.CertificateRequestStatusPending).
		Order("created_at").
		Find(&requests).Error
	if err != nil {
		return nil, fmt.Errorf("failed to list pending certificate requests: %w", err)
	}
	return requests, nil
}

// Approve marks requestID approved by identity and, per its Type, either
// issues a certificate (user, host) or creates an Enrollment (service).
// Returns the certificate in PEM/authorized-format for user/host requests,
// or empty for service requests (retrieved later via `service retrieve`).
//
// TODO: not implemented — needs CA signing and the lifetime-policy
// computation (see the TODO on CertRequestService). Once it is, this must
// compute the key ID via s.keyIDTmpls.execute(req.Type, keyIDTemplateData{...})
// (see docs/certificate-keyid-template.md) before signing, and must also
// call s.notifyWaiter(requestID, requestOutcome{status:
// model.CertificateRequestStatusApproved, certificate: cert}) so a
// connection blocked in Wait unblocks.
func (s *CertRequestService) Approve(ctx context.Context, requestID string, identity *Identity) (certificate string, err error) {
	return "", errors.New("not implemented")
}

// Deny marks requestID denied and notifies anything waiting in Wait.
func (s *CertRequestService) Deny(ctx context.Context, requestID string) error {
	now := time.Now()
	result := s.db.WithContext(ctx).Model(&model.CertificateRequest{}).
		Where("id = ? AND status = ?", requestID, model.CertificateRequestStatusPending).
		Updates(map[string]any{
			"status":      model.CertificateRequestStatusDenied,
			"resolved_at": now,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to deny certificate request: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("certificate request %q is not pending", requestID)
	}

	s.notifyWaiter(requestID, requestOutcome{status: model.CertificateRequestStatusDenied})

	return nil
}

// Wait blocks until requestID resolves (approved/denied) or ctx is
// canceled (e.g. the client disconnects), for the SSE handler in
// server/controller/certrequests.go to relay to the client.
func (s *CertRequestService) Wait(ctx context.Context, requestID string) (status model.CertificateRequestStatus, certificate string, err error) {
	s.mu.Lock()
	ch, ok := s.waiters[requestID]
	s.mu.Unlock()
	if !ok {
		return "", "", fmt.Errorf("no pending wait registered for certificate request %q", requestID)
	}

	select {
	case outcome := <-ch:
		return outcome.status, outcome.certificate, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.waiters, requestID)
		s.mu.Unlock()
		return "", "", ctx.Err()
	}
}

// registerWaiter creates the channel Approve/Deny will send requestID's
// outcome to.
func (s *CertRequestService) registerWaiter(requestID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waiters[requestID] = make(chan requestOutcome, 1)
}

// notifyWaiter delivers outcome to whatever's blocked in Wait for
// requestID, if anything still is (a non-existent or already-consumed
// entry is a silent no-op — Wait may never have been called, or the
// waiting connection may have already disconnected).
func (s *CertRequestService) notifyWaiter(requestID string, outcome requestOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch, ok := s.waiters[requestID]
	if !ok {
		return
	}
	ch <- outcome
	delete(s.waiters, requestID)
}
