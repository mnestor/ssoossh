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
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
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

// requestOutcome is what Approve/Deny/expiry hands to callers blocked in or
// reconnecting to Wait.
type requestOutcome struct {
	status      model.CertificateRequestStatus
	certificate string
}

// CertRequestService manages the pending-approval lifecycle shared by all
// three certificate types: a client creates a request (`ssh login`,
// `host sign`, `service enroll`) and its events endpoint waits for it to
// resolve (see server/controller/certrequests.go's eventsHandler); the web
// UI lists/approves/denies it out-of-band, which is what unblocks that wait
// via the in-process broker below.
//
// Approving a request behaves differently per Type:
//   - user, host: sign and persist a model.Certificate immediately
//   - service: create a model.Enrollment instead (see service/enrollment.go) —
//     the certificate itself isn't issued until `service retrieve`
//
// The broker (waiters/resolved, below) is in-process only, matching the v1
// single-process deployment target noted in docs/ssoossh-context.md; a
// multi-instance deployment would need this replaced with something
// shared (e.g. LISTEN/NOTIFY on Postgres, or polling the DB). It's also
// lost across a server restart — a request left pending across a restart
// falls back to Wait's DB-status path (still correct, just loses the
// in-memory fast path) until acted on again.
type CertRequestService struct {
	config     *config.Config
	db         *gorm.DB
	keyIDTmpls *keyIDTemplates

	mu sync.Mutex
	// waiters holds a signal channel per requestID, closed (never sent on)
	// by notifyWaiter to broadcast resolution to every current and future
	// caller blocked in Wait — a closed channel can be read from any
	// number of times, unlike a single-value channel, so reconnecting
	// clients (a fresh Wait call for the same requestID after an SSE
	// disconnect/reconnect) don't race a one-shot consumer.
	waiters map[string]chan struct{}
	// resolved caches the outcome for any requestID notifyWaiter has fired
	// for, so a Wait call arriving after resolution (a late reconnect, or
	// one that was never blocked in the first place) reads the cached
	// outcome instead of hitting "no such waiter."
	resolved map[string]requestOutcome
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
		waiters:    make(map[string]chan struct{}),
		resolved:   make(map[string]requestOutcome),
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

	return req.ID, nil
}

// ttlCutoff returns the CreatedAt threshold before which a still-pending
// request is treated as expired: created before this instant, no longer
// approvable. A zero RequestTTL disables expiry (returns the zero Time, so
// no request's CreatedAt is ever before it).
func (s *CertRequestService) ttlCutoff() time.Time {
	if s.config.CertOptions.RequestTTL <= 0 {
		return time.Time{}
	}
	return time.Now().Add(-s.config.CertOptions.RequestTTL)
}

// ListPending returns the pending, not-yet-expired requests visible to the
// approving user in the web UI. Rows past the TTL are filtered out here but
// not actively flipped to "expired" — Wait does that lazily for whichever
// request a client is actually still watching; a listed-but-abandoned
// request simply ages out of this list on its own.
//
// TODO: decide the visibility rule — all pending requests, or only ones the
// current user is entitled to approve (see docs/ssoossh-context.md open
// question on host-admin scope).
func (s *CertRequestService) ListPending(ctx context.Context) ([]model.CertificateRequest, error) {
	q := s.db.WithContext(ctx).Where("status = ?", model.CertificateRequestStatusPending)
	if cutoff := s.ttlCutoff(); !cutoff.IsZero() {
		q = q.Where("created_at > ?", cutoff)
	}

	var requests []model.CertificateRequest
	if err := q.Order("created_at").Find(&requests).Error; err != nil {
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
// computation (see the TODO on CertRequestService). Once it is, this must:
//   - reject with errorresponses.NotFoundError/a TTL check (see Deny) rather
//     than approving a request past s.ttlCutoff()
//   - compute the key ID via s.keyIDTmpls.execute(req.Type, keyIDTemplateData{...})
//     (see docs/certificate-keyid-template.md) before signing
//   - call s.notifyWaiter(requestID, requestOutcome{status:
//     model.CertificateRequestStatusApproved, certificate: cert}) so Wait
//     unblocks
func (s *CertRequestService) Approve(ctx context.Context, requestID string, identity *Identity) (certificate string, err error) {
	return "", errors.New("not implemented")
}

// Deny marks requestID denied and notifies anything waiting in Wait. Denying
// a request that's already past the TTL (but not yet lazily flipped to
// "expired" by Wait) fails the same as denying any other non-pending
// request — the row's actual state is Wait's responsibility to reconcile.
func (s *CertRequestService) Deny(ctx context.Context, requestID string) error {
	now := time.Now()
	q := s.db.WithContext(ctx).Model(&model.CertificateRequest{}).
		Where("id = ? AND status = ?", requestID, model.CertificateRequestStatusPending)
	if cutoff := s.ttlCutoff(); !cutoff.IsZero() {
		q = q.Where("created_at > ?", cutoff)
	}
	result := q.Updates(map[string]any{
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

// Wait blocks until requestID resolves (approved/denied/expired) or ctx is
// canceled (e.g. the client disconnects), for the events handler in
// server/controller/certrequests.go to relay to the client.
//
// Safe to call more than once for the same requestID, including after a
// previous call returned via ctx cancellation — each call re-checks the
// cached outcome and the DB rather than assuming a single caller, so an SSE
// client reconnecting after a dropped connection just re-attaches instead
// of hitting a stale "no such waiter" error.
func (s *CertRequestService) Wait(ctx context.Context, requestID string) (status model.CertificateRequestStatus, certificate string, err error) {
	for {
		//TODO: I think this is where we need to hook up https://github.com/ThreeDotsLabs/watermill
		// see docs/watermill-signer-plan.md for the full design (wake topic
		// here, plus a separate durable sign queue + signer + listener —
		// this Wait loop only ever needs to change for the wake-topic half).
		s.mu.Lock()
		if outcome, ok := s.resolved[requestID]; ok {
			s.mu.Unlock()
			return outcome.status, outcome.certificate, nil
		}
		ch, ok := s.waiters[requestID]
		if !ok {
			ch = make(chan struct{})
			s.waiters[requestID] = ch
		}
		s.mu.Unlock()

		var req model.CertificateRequest
		if err := s.db.WithContext(ctx).First(&req, "id = ?", requestID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", "", &errorresponses.NotFoundError{Resource: fmt.Sprintf("certificate request %q", requestID)}
			}
			return "", "", fmt.Errorf("failed to look up certificate request: %w", err)
		}

		if req.Status != model.CertificateRequestStatusPending {
			// Resolved in the DB but not reflected in-memory — e.g. after a
			// server restart, or a race with Approve/Deny landing between
			// the resolved-map check above and this read. Cache it so
			// later callers (including this one, next loop) hit the fast
			// path.
			s.notifyWaiter(requestID, requestOutcome{status: req.Status})
			continue
		}

		if cutoff := s.ttlCutoff(); !cutoff.IsZero() && req.CreatedAt.Before(cutoff) {
			s.expire(ctx, requestID)
			continue
		}

		select {
		case <-ch:
			continue
		case <-ctx.Done():
			return "", "", ctx.Err()
		}
	}
}

// expire marks requestID expired in the DB, guarded the same way Deny
// guards its update so a concurrent Approve/Deny can't race it — only one
// of them actually changes the row — then notifies any waiter. If the
// update affects no rows (lost the race, or already resolved by the time
// this runs), it's a no-op: the next loop iteration in Wait re-reads the
// DB and picks up whatever the actual resolved state turned out to be.
func (s *CertRequestService) expire(ctx context.Context, requestID string) {
	now := time.Now()
	result := s.db.WithContext(ctx).Model(&model.CertificateRequest{}).
		Where("id = ? AND status = ?", requestID, model.CertificateRequestStatusPending).
		Updates(map[string]any{
			"status":      model.CertificateRequestStatusExpired,
			"resolved_at": now,
		})
	if result.Error != nil || result.RowsAffected == 0 {
		return
	}

	s.notifyWaiter(requestID, requestOutcome{status: model.CertificateRequestStatusExpired})
}

// notifyWaiter caches outcome for requestID (so any Wait call arriving
// after this point, including a late reconnect, reads it directly) and
// wakes anything currently blocked in Wait by closing its signal channel —
// closing rather than sending lets any number of current and future
// waiters observe it, instead of only the first to receive.
func (s *CertRequestService) notifyWaiter(requestID string, outcome requestOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.resolved[requestID] = outcome

	ch, ok := s.waiters[requestID]
	if !ok {
		return
	}
	close(ch)
	delete(s.waiters, requestID)
}
