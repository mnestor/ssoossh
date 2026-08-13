package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/certmsg"
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
	Approve(ctx context.Context, requestID string, identity *Identity) error
	Deny(ctx context.Context, requestID string) error
	Wait(ctx context.Context, requestID string) (status model.CertificateRequestStatus, certificate string, code string, err error)
}

// requestOutcome is what Approve/Deny/expiry hands to callers blocked in or
// reconnecting to Wait. code is set instead of certificate for
// CertificateRequestStatusEnrolled (CertificateTypeService — see Approve).
type requestOutcome struct {
	status      model.CertificateRequestStatus
	certificate string
	code        string
}

// requestOutcomeMessage is requestOutcome's wire shape, published to a
// request's wake topic (see certmsg.WaitTopic). Wait itself never needs to decode
// this — the resolved-cache/DB checks at the top of its loop are the
// actual source of truth, and the message is only a low-latency signal
// that something changed — but it's still JSON so any future consumer
// (e.g. debugging, or a listener/resolver in a later phase — see
// docs/signing-pipeline.md) can read it directly.
type requestOutcomeMessage struct {
	Status      model.CertificateRequestStatus `json:"status"`
	Certificate string                         `json:"certificate,omitempty"`
	Code        string                         `json:"code,omitempty"`
}

// CertRequestService manages the pending-approval lifecycle shared by all
// three certificate types: a client creates a request (`ssh login`,
// `host sign`, `service enroll`) and its events endpoint waits for it to
// resolve (see server/controller/certrequests.go's eventsHandler); the web
// UI lists/approves/denies it out-of-band, which is what unblocks that wait
// via publisher/subscriber below (see docs/signing-pipeline.md).
//
// Approving a request behaves differently per Type:
//   - user, host: sign and persist a model.Certificate immediately
//   - service: create a model.Enrollment instead (see service/enrollment.go) —
//     the certificate itself isn't issued until `service retrieve`
//
// publisher/subscriber are gochannel-backed (in-process, in-memory) — see
// docs/signer-split-deferred.md for when that becomes configurable.
// Either way, the wake signal alone is never the
// only source of truth: resolved (below) and the DB are both checked
// before ever relying on it, so a lost/missed wake message is a latency
// problem (caught on reconnect), not a correctness one.
type CertRequestService struct {
	config     *config.Config
	db         *gorm.DB
	keyIDTmpls *keyIDTemplates
	publisher  message.Publisher
	subscriber message.Subscriber

	mu sync.Mutex
	// resolved caches the outcome for any requestID notifyWaiter has fired
	// for, so a Wait call arriving after resolution (a late reconnect, or
	// one that was never blocked in the first place) reads the cached
	// outcome instead of waiting on a wake message that already happened.
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
// publisher/subscriber back the wake-topic broker (see certmsg.WaitTopic) —
// the gochannel-based pair from server/pubsub.
func NewCertRequestService(c *config.Config, db *gorm.DB, publisher message.Publisher, subscriber message.Subscriber) (*CertRequestService, error) {
	keyIDTmpls, err := newKeyIDTemplates(c.CertOptions)
	if err != nil {
		return nil, err
	}

	return &CertRequestService{
		config:     c,
		db:         db,
		keyIDTmpls: keyIDTmpls,
		publisher:  publisher,
		subscriber: subscriber,
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

// Approve resolves policy for requestID against server config and identity,
// then branches by type:
//
//   - user, host: marks the request CertificateRequestStatusSigning and
//     publishes a self-contained signingJob to certmsg.SignQueueTopic — the queue is
//     the only entrypoint that turns an approved request into a signed
//     certificate (see docs/signing-pipeline.md; actual signing
//     is docs/signing-pipeline.md). The certificate is
//     delivered later, over the client's own Wait/SSE connection, once the
//     signer and listener/resolver process the job.
//   - service: does NOT use the queue. Marks the request
//     CertificateRequestStatusEnrolled with a freshly generated
//     EnrollmentToken directly, and notifies the wake topic itself, right
//     here in the approving request — there's no signer round trip to wait
//     on, since the certificate itself isn't produced until a later
//     `service retrieve` redeems the token (see
//     docs/ssoossh-context.md, "Service enrollment"). The client waiting on
//     Wait/SSE for this request gets the token, not a certificate.
//
// Policy resolution (applies to both branches):
//   - Extensions are narrowed to the intersection of requested and
//     configured-permitted (server config is always the outer bound — see
//     root CLAUDE.md Hard Constraints).
//   - ForceCommand and SourceAddresses are dropped entirely: there's no
//     server config concept yet to bound either against (source-address
//     policy is explicitly "design in progress," see
//     docs/ssoossh-context.md), and granting an unbounded client-requested
//     critical option would violate the same hard constraint. Revisit once
//     that policy exists.
//   - NoTouchRequired is only ever granted for CertificateTypeService, per
//     root CLAUDE.md.
//   - RequireGroup (service/host config) is enforced: identity must be a
//     member, or Approve fails without publishing/enrolling anything.
//   - Principals are a conservative provisional default — just the
//     identity's username for user/service, just the hostname for host —
//     pending the still-undecided "which LDAP attributes become
//     principals" question (docs/ssoossh-context.md). Safe to extend
//     later without narrowing what's already granted.
func (s *CertRequestService) Approve(ctx context.Context, requestID string, identity *Identity) error {
	var req model.CertificateRequest
	if err := s.db.WithContext(ctx).First(&req, "id = ?", requestID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &errorresponses.NotFoundError{Resource: fmt.Sprintf("certificate request %q", requestID)}
		}
		return fmt.Errorf("failed to look up certificate request: %w", err)
	}
	if req.Status != model.CertificateRequestStatusPending {
		return fmt.Errorf("certificate request %q is not pending", requestID)
	}
	if cutoff := s.ttlCutoff(); !cutoff.IsZero() && req.CreatedAt.Before(cutoff) {
		s.expire(ctx, requestID)
		return fmt.Errorf("certificate request %q is not pending", requestID)
	}

	var requested RequestedOptions
	if err := json.Unmarshal([]byte(req.RequestedOptions), &requested); err != nil {
		return fmt.Errorf("failed to decode requested options: %w", err)
	}

	narrowed, validDuration, requireGroup, err := resolveCertOptions(s.config.CertOptions, req.Type, requested)
	if err != nil {
		return err
	}
	if requireGroup != "" && !slices.Contains(identity.Groups, requireGroup) {
		return fmt.Errorf("identity is not authorized to approve %s certificates", req.Type)
	}

	switch req.Type {
	case model.CertificateTypeService:
		return s.approveServiceEnrollment(ctx, requestID, narrowed)
	case model.CertificateTypeUser:
		return s.approveForSigning(ctx, req, identity, narrowed, validDuration)
	default:
		// Host and PAM certificates aren't issuable yet — the signer only
		// handles user certificates for now (see
		// docs/signing-pipeline.md). Reject here rather than
		// queueing a job the signer will refuse: the human approving it gets
		// an immediate, comprehensible error instead of the request quietly
		// resolving to "failed" a moment later. The signer keeps its own
		// guard as defense in depth.
		return fmt.Errorf("issuing %s certificates is not supported yet", req.Type)
	}
}

// approveServiceEnrollment implements Approve's service branch — see its
// doc comment. narrowed is req's already-resolved, server-config-bounded
// RequestedOptions.
func (s *CertRequestService) approveServiceEnrollment(ctx context.Context, requestID string, narrowed RequestedOptions) error {
	narrowedJSON, err := json.Marshal(narrowed)
	if err != nil {
		return fmt.Errorf("failed to encode narrowed options: %w", err)
	}
	token := uuid.NewString()
	now := time.Now()

	result := s.db.WithContext(ctx).Model(&model.CertificateRequest{}).
		Where("id = ? AND status = ?", requestID, model.CertificateRequestStatusPending).
		Updates(map[string]any{
			"status":            model.CertificateRequestStatusEnrolled,
			"requested_options": string(narrowedJSON),
			"enrollment_token":  token,
			"resolved_at":       now,
		})
	if result.Error != nil {
		return fmt.Errorf("failed to mark certificate request as enrolled: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("certificate request %q is not pending", requestID)
	}

	// No signer round trip for enrollment — notify the wake topic directly
	// from here, unlike the user/host queue-and-wait path.
	s.notifyWaiter(requestID, requestOutcome{status: model.CertificateRequestStatusEnrolled, code: token})

	return nil
}

// approveForSigning implements Approve's user/host branch — see its doc
// comment. narrowed/validDuration are req.Type's already-resolved,
// server-config-bounded policy.
func (s *CertRequestService) approveForSigning(ctx context.Context, req model.CertificateRequest, identity *Identity, narrowed RequestedOptions, validDuration time.Duration) error {
	keyID, err := s.keyIDTmpls.execute(req.Type, keyIDTemplateData{
		Username: identity.Username,
		Subject:  identity.Subject,
		Email:    identity.Email,
		ClientIP: req.SourceIP,
		Hostname: req.Hostname,
		UniqueID: req.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to compute key ID: %w", err)
	}

	now := time.Now()
	result := s.db.WithContext(ctx).Model(&model.CertificateRequest{}).
		Where("id = ? AND status = ?", req.ID, model.CertificateRequestStatusPending).
		Updates(map[string]any{"status": model.CertificateRequestStatusSigning})
	if result.Error != nil {
		return fmt.Errorf("failed to mark certificate request as signing: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("certificate request %q is not pending", req.ID)
	}

	job := certmsg.SigningJob{
		RequestID:        req.ID,
		Type:             req.Type,
		PublicKey:        req.PublicKey,
		Hostname:         req.Hostname,
		Principals:       resolvePrincipals(req.Type, req.Hostname, identity),
		KeyID:            keyID,
		RequestedOptions: narrowed,
		ValidAfter:       now,
		ValidBefore:      now.Add(validDuration),
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to encode signing job: %w", err)
	}

	// If this publish fails, the row is left in Signing with no queued
	// job — a stuck row the invalidation sweep is responsible for
	// catching, not something recovered here (see
	// docs/signing-pipeline.md).
	if err := s.publisher.Publish(certmsg.SignQueueTopic, message.NewMessage(watermill.NewUUID(), payload)); err != nil {
		return fmt.Errorf("failed to publish signing job: %w", err)
	}

	return nil
}

// resolveCertOptions narrows requested against certType's server config.
// See Approve's doc comment for the policy this implements.
func resolveCertOptions(opts config.CertificateOptions, certType model.CertificateType, requested RequestedOptions) (narrowed RequestedOptions, validDuration time.Duration, requireGroup string, err error) {
	var permittedExtensions []string
	switch certType {
	case model.CertificateTypeUser:
		permittedExtensions = opts.User.Extensions
		validDuration = opts.User.ValidDuration
	case model.CertificateTypeService:
		permittedExtensions = opts.Service.Extensions
		validDuration = opts.Service.ValidDuration
		requireGroup = opts.Service.RequireGroup
	case model.CertificateTypeHost:
		validDuration = opts.Host.ValidDuration
		requireGroup = opts.Host.RequireGroup
	default:
		return RequestedOptions{}, 0, "", fmt.Errorf("unsupported certificate type %q", certType)
	}

	narrowed.Extensions = intersectStrings(requested.Extensions, permittedExtensions)
	narrowed.NoTouchRequired = requested.NoTouchRequired && certType == model.CertificateTypeService

	return narrowed, validDuration, requireGroup, nil
}

// intersectStrings returns the elements of requested that also appear in
// permitted, preserving requested's order. nil/empty permitted yields nil.
func intersectStrings(requested, permitted []string) []string {
	if len(permitted) == 0 {
		return nil
	}
	allowed := make(map[string]bool, len(permitted))
	for _, p := range permitted {
		allowed[p] = true
	}

	var out []string
	for _, r := range requested {
		if allowed[r] {
			out = append(out, r)
		}
	}
	return out
}

// resolvePrincipals is a conservative, provisional default — see this
// package's Approve doc comment and docs/ssoossh-context.md's "Which LDAP
// attributes become principals" open question.
func resolvePrincipals(certType model.CertificateType, hostname string, identity *Identity) []string {
	if certType == model.CertificateTypeHost {
		return []string{hostname}
	}
	return []string{identity.Username}
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
func (s *CertRequestService) Wait(ctx context.Context, requestID string) (status model.CertificateRequestStatus, certificate string, code string, err error) {
	for {
		s.mu.Lock()
		if outcome, ok := s.resolved[requestID]; ok {
			s.mu.Unlock()
			return outcome.status, outcome.certificate, outcome.code, nil
		}
		s.mu.Unlock()

		// Subscribe before the DB read below, not after: the gochannel
		// pub/sub (server/pubsub) is Persistent, so even if notifyWaiter
		// publishes between this Subscribe call and the DB read, the
		// message is still replayed to us — subscribing late here can
		// only mean seeing the wake message slightly sooner than a
		// same-process notifyWaiter call could otherwise race, never
		// missing it. Fresh subscription per loop iteration (not held
		// across iterations) — see docs/signing-pipeline.md.
		messages, err := s.subscriber.Subscribe(ctx, certmsg.WaitTopic(requestID))
		if err != nil {
			return "", "", "", fmt.Errorf("failed to subscribe to certificate request updates: %w", err)
		}

		req, err := s.lookupRequest(ctx, requestID)
		if err != nil {
			return "", "", "", err
		}

		block, err := s.reconcileStatus(ctx, requestID, req)
		if err != nil {
			return "", "", "", err
		}
		if !block {
			continue
		}

		select {
		case msg, ok := <-messages:
			if ok {
				msg.Ack()
			}
			continue
		case <-ctx.Done():
			return "", "", "", ctx.Err()
		}
	}
}

// lookupRequest fetches requestID, translating a not-found row into
// errorresponses.NotFoundError.
func (s *CertRequestService) lookupRequest(ctx context.Context, requestID string) (model.CertificateRequest, error) {
	var req model.CertificateRequest
	if err := s.db.WithContext(ctx).First(&req, "id = ?", requestID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.CertificateRequest{}, &errorresponses.NotFoundError{Resource: fmt.Sprintf("certificate request %q", requestID)}
		}
		return model.CertificateRequest{}, fmt.Errorf("failed to look up certificate request: %w", err)
	}
	return req, nil
}

// reconcileStatus handles req's current DB status for Wait's loop:
// expires a pending request past TTL, or caches+notifies a terminal
// status found in the DB but not yet reflected in-memory (e.g. after a
// server restart, or a race with Approve/Deny landing between Wait's
// resolved-map check and this read).
//
// Returns block=true if Wait should proceed to block on the wake-topic
// select (still pending within TTL, or signing — neither is terminal), and
// block=false if the loop should continue around and re-check from the top
// (something just changed). A non-nil error terminates Wait outright.
func (s *CertRequestService) reconcileStatus(ctx context.Context, requestID string, req model.CertificateRequest) (block bool, err error) {
	switch req.Status {
	case model.CertificateRequestStatusPending:
		if cutoff := s.ttlCutoff(); !cutoff.IsZero() && req.CreatedAt.Before(cutoff) {
			s.expire(ctx, requestID)
			return false, nil
		}
		return true, nil

	case model.CertificateRequestStatusSigning:
		// Approved and queued for the signer (see
		// docs/signing-pipeline.md) — not yet resolved. No TTL
		// applies (TTL is only for "unapproved too long," see ttlCutoff),
		// so wait the same way as pending.
		return true, nil

	case model.CertificateRequestStatusApproved:
		// Only reachable with a cold resolved cache: Wait checks that
		// first, and the listener/resolver populates it *before* writing
		// this status (see SignedReplyHandler.resolveSuccess). So getting
		// here means the process restarted after the certificate was
		// delivered — and since certificates are never persisted, it's
		// genuinely gone. Say so, rather than handing back a successful
		// outcome with an empty certificate the caller may not check.
		return false, &errorresponses.CertificateUnavailableError{RequestID: requestID}

	case model.CertificateRequestStatusEnrolled:
		// Unlike a certificate, an enrollment token *is* durable — it's on
		// the row — so a reconnect against a cold cache can be answered in
		// full from the database.
		s.notifyWaiter(requestID, requestOutcome{status: req.Status, code: req.EnrollmentToken})
		return false, nil

	default: // denied, expired, failed
		s.notifyWaiter(requestID, requestOutcome{status: req.Status})
		return false, nil
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
// publishes it to the request's wake topic (see certmsg.WaitTopic) so anything
// currently blocked in Wait — in this process or, once a real shared
// broker is configured (docs/signer-split-deferred.md), another
// one — wakes up. A publish failure here is logged but not fatal to the
// caller (Deny/expire's own DB update already succeeded, which is the
// durable fact) — a blocked Wait call will still catch up on its own via
// the DB-status check the next time anything nudges it (reconnect, or a
// future poll), same as if the process restarted between the DB write and
// this publish.
func (s *CertRequestService) notifyWaiter(requestID string, outcome requestOutcome) {
	s.mu.Lock()
	s.resolved[requestID] = outcome
	s.mu.Unlock()

	payload, err := json.Marshal(requestOutcomeMessage{
		Status:      outcome.status,
		Certificate: outcome.certificate,
		Code:        outcome.code,
	})
	if err != nil {
		slog.Error("failed to encode certificate request outcome", "request_id", requestID, "error", err)
		return
	}

	if err := s.publisher.Publish(certmsg.WaitTopic(requestID), message.NewMessage(watermill.NewUUID(), payload)); err != nil {
		slog.Error("failed to publish certificate request outcome", "request_id", requestID, "error", err)
	}
}
