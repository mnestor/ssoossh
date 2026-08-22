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
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/fipsmode"
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
	Username         string // set for CertificateTypePAM only — see model.CertificateRequest.Username
	SourceIP         string
	RequestedOptions RequestedOptions
}

// CertRequestProvider manages the pending-approval lifecycle for
// certificate requests. CertRequestService is the production
// implementation.
type CertRequestProvider interface {
	CreateRequest(ctx context.Context, p NewCertRequestParams) (requestID string, err error)
	Detail(ctx context.Context, requestID string, identity *Identity) (*RequestDetail, error)
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
// resolve (see server/controller/certrequests.go's eventsHandler); a human
// opens the approval URL that client printed and approves or denies it
// out-of-band, which is what unblocks that wait via publisher/subscriber
// below (see docs/signing-pipeline.md). Requests are never listed — see
// NewCertRequestController for why.
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
	policies   map[model.CertificateType]*certTypePolicy
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
		policies:   newCertTypePolicies(c.CertOptions, keyIDTmpls),
		publisher:  publisher,
		subscriber: subscriber,
		resolved:   make(map[string]requestOutcome),
	}, nil
}

// policyFor returns certType's certTypePolicy, or an error naming it when
// none exists — the same defense-in-depth guard resolveCertOptions and
// resolvePrincipals used to each reimplement in their own switch's default
// case. In practice every route into CreateRequest hardcodes a known
// model.CertificateType (see server/controller/certrequests.go), so this
// only fires for a corrupted or hand-edited database row.
func (s *CertRequestService) policyFor(certType model.CertificateType) (*certTypePolicy, error) {
	policy, ok := s.policies[certType]
	if !ok {
		return nil, fmt.Errorf("unsupported certificate type %q", certType)
	}
	return policy, nil
}

// CreateRequest persists a new pending model.CertificateRequest for p and
// returns its ID, which the client then waits on via Wait and the web UI
// resolves via Approve/Deny.
func (s *CertRequestService) CreateRequest(ctx context.Context, p NewCertRequestParams) (requestID string, err error) {
	optionsJSON, err := json.Marshal(p.RequestedOptions)
	if err != nil {
		return "", fmt.Errorf("failed to encode requested options: %w", err) // excluded from coverage: RequestedOptions is a plain struct of strings/bools/slices, json.Marshal can't fail on it, see exclude-from-coverage.txt
	}

	req := model.CertificateRequest{
		ID:               uuid.NewString(),
		Type:             p.Type,
		PublicKey:        p.PublicKey,
		Hostname:         p.Hostname,
		Username:         p.Username,
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

// RequestDetail is everything the approval page needs to show a human what
// they are about to authorize.
//
// Requested and Narrowed are both present on purpose. Server config is the
// outer bound on every option, and options a deployment doesn't permit are
// trimmed rather than rejected — so the UI has to be able to show what was
// asked for alongside what would actually be granted, before anyone
// approves anything (see root CLAUDE.md, Hard Constraints).
type RequestDetail struct {
	Request model.CertificateRequest

	// Requested is what the client asked for, as submitted.
	Requested RequestedOptions

	// Narrowed is what would actually be granted, after server config.
	Narrowed RequestedOptions

	// Principals and ValidDuration are the other two things the certificate
	// would carry that the requester does not choose.
	Principals    []string
	ValidDuration time.Duration
}

// Detail returns what identity would be approving for requestID, and binds
// the request to them.
//
// The binding lives here rather than only in Approve because this is the
// first authenticated touch: the approval page loads before anyone decides,
// so claiming here means a request is owned from the moment its owner looks
// at it, and a second user gets a clear refusal on load instead of after
// clicking approve. Approve re-checks regardless — this endpoint is a
// convenience, not the enforcement point.
//
// Policy is resolved against identity because the certificate's principals
// come from the approver (see resolvePrincipals), so a different viewer
// would legitimately see different principals. The binding is what stops
// that being useful to anyone but the owner.
func (s *CertRequestService) Detail(ctx context.Context, requestID string, identity *Identity) (*RequestDetail, error) {
	var req model.CertificateRequest
	if err := s.db.WithContext(ctx).First(&req, "id = ?", requestID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &errorresponses.NotFoundError{Resource: fmt.Sprintf("certificate request %q", requestID)}
		}
		return nil, fmt.Errorf("failed to look up certificate request: %w", err)
	}

	if err := s.bindRequester(ctx, &req, identity); err != nil {
		return nil, err
	}

	var requested RequestedOptions
	if err := json.Unmarshal([]byte(req.RequestedOptions), &requested); err != nil {
		return nil, fmt.Errorf("failed to decode requested options: %w", err)
	}

	policy, err := s.policyFor(req.Type)
	if err != nil {
		return nil, err
	}

	return &RequestDetail{
		Request:       req,
		Requested:     requested,
		Narrowed:      narrowRequestedOptions(policy, requested),
		Principals:    policy.principals(req.Hostname, req.Username, identity),
		ValidDuration: policy.validDuration,
	}, nil
}

// Approve resolves policy for requestID against server config and identity,
// then branches by type:
//
//   - user, PAM: marks the request CertificateRequestStatusSigning and
//     publishes a self-contained signingJob to certmsg.SignQueueTopic — the queue is
//     the only entrypoint that turns an approved request into a signed
//     certificate (see docs/signing-pipeline.md; actual signing
//     is docs/signing-pipeline.md). The certificate is
//     delivered later, over the client's own Wait/SSE connection, once the
//     signer and listener/resolver process the job. Host is not — it falls
//     through to Approve's default case below.
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
//   - RequireGroup (service/host/PAM config) is enforced: identity must be
//     a member, or Approve fails without publishing/enrolling anything. PAM
//     is the one type where an unset RequireGroup denies rather than opens
//     — see CertOptionsPAM.RequireGroup.
//   - Principals are a conservative provisional default — just the
//     identity's username for user/service, just the hostname for host —
//     pending the still-undecided "which LDAP attributes become
//     principals" question (docs/ssoossh-context.md). PAM is the deliberate
//     exception: its principal is the local account named on the request
//     (req.Username), not the approver's identity — see resolvePrincipals.
//     Safe to extend later without narrowing what's already granted.
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

	policy, err := s.policyFor(req.Type)
	if err != nil {
		return err
	}
	narrowed := narrowRequestedOptions(policy, requested)
	// PAM is the one type where an unset requireGroup denies rather than
	// opens: "who may sudo" is deliberately narrower than "who may log in",
	// so a deployment that hasn't configured cert_options.pam.require_group
	// issues no PAM certificates at all (see CertOptionsPAM.RequireGroup).
	if req.Type == model.CertificateTypePAM && policy.requireGroup == "" {
		return fmt.Errorf("pam certificate issuance requires cert_options.pam.require_group to be configured")
	}
	if policy.requireGroup != "" && !slices.Contains(identity.Groups, policy.requireGroup) {
		return fmt.Errorf("identity is not authorized to approve %s certificates", req.Type)
	}

	if err := s.bindRequester(ctx, &req, identity); err != nil {
		return err
	}

	if s.config.FIPSEnabled() {
		if err := s.checkFIPSApproved(req.PublicKey); err != nil {
			return err
		}
	}

	switch policy.flow {
	case flowEnrollment:
		return s.approveServiceEnrollment(ctx, requestID, narrowed)
	case flowSigning:
		return s.approveForSigning(ctx, req, identity, policy, narrowed)
	default:
		// Host certificates aren't issuable yet — the signer only handles
		// user and PAM certificates for now (see
		// docs/signing-pipeline.md). Reject here rather than
		// queueing a job the signer will refuse: the human approving it gets
		// an immediate, comprehensible error instead of the request quietly
		// resolving to "failed" a moment later. The signer keeps its own
		// guard as defense in depth.
		return fmt.Errorf("issuing %s certificates is not supported yet", req.Type)
	}
}

// checkFIPSApproved rejects authorizedKey if its algorithm isn't
// FIPS-approved. Called from Approve, not CreateRequest: a client can
// submit whatever key it likes, but a FIPS-enabled server refuses to act on
// (sign or enroll) one that isn't approved. server/signer independently
// repeats this check on the signing path as defense in depth against a
// compromised main process that could publish directly to the sign queue.
func (s *CertRequestService) checkFIPSApproved(authorizedKey string) error {
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorizedKey)) //nolint:dogsled // ParseAuthorizedKey's comment/options/rest returns are irrelevant here.
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err)
	}
	keyType, ok := fipsmode.FromSSHAlgorithm(publicKey.Type())
	if !ok || !fipsmode.IsApprovedInFIPS(keyType) {
		return fmt.Errorf("public key algorithm %q is not FIPS-approved", publicKey.Type())
	}
	return nil
}

// bindRequester ties req to the users row behind identity, and refuses when
// it is already tied to a different one.
//
// A request is created by an unauthenticated client, so nothing knows whose
// it is until a human authenticates and acts on it. The first such action
// claims it; every later one must match. That is what stops one user
// approving another's pending request — which matters because the
// certificate carries the *approver's* principals over the *requester's*
// public key (see resolvePrincipals), so an admin approving a stranger's
// request would hand that stranger an admin certificate.
//
// It does not defend against a user being tricked into approving a request
// an attacker created for them: that consent-phishing case needs a
// verification code the client displays and the browser has to match, which
// is deliberately out of scope here (see docs/security-review-2026-08-11.md
// finding 2).
func (s *CertRequestService) bindRequester(ctx context.Context, req *model.CertificateRequest, identity *Identity) error {
	userID, err := s.resolveUserID(ctx, identity)
	if err != nil {
		return err
	}

	if req.UserID != nil {
		if *req.UserID != userID {
			return &errorresponses.ForbiddenError{Reason: "certificate request belongs to another user"}
		}
		return nil
	}

	// Guarded so two approvals racing on an unclaimed request can't both
	// win: the second sees RowsAffected == 0 and falls through to the
	// ownership check below rather than overwriting the first claim.
	result := s.db.WithContext(ctx).Model(&model.CertificateRequest{}).
		Where("id = ? AND user_id IS NULL", req.ID).
		Update("user_id", userID)
	if result.Error != nil {
		return fmt.Errorf("failed to bind certificate request to user: %w", result.Error) // excluded from coverage: forcing this specific query (not resolveUserID's, tested) to fail needs per-query DB fault injection, see exclude-from-coverage.txt
	}

	if result.RowsAffected == 0 {
		var claimed model.CertificateRequest
		if err := s.db.WithContext(ctx).First(&claimed, "id = ?", req.ID).Error; err != nil {
			return fmt.Errorf("failed to re-read certificate request after a racing claim: %w", err) // excluded from coverage: forcing the re-read specifically (not the guarded UPDATE above it) to fail needs per-query DB fault injection, see exclude-from-coverage.txt
		}
		if claimed.UserID == nil || *claimed.UserID != userID {
			return &errorresponses.ForbiddenError{Reason: "certificate request belongs to another user"}
		}
	}

	req.UserID = &userID
	return nil
}

// resolveUserID maps identity to its users row ID, keyed on the OIDC
// subject. The row is written at login (AuthService.upsertUser), so a miss
// means the caller's session outlived its user record.
func (s *CertRequestService) resolveUserID(ctx context.Context, identity *Identity) (string, error) {
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", &errorresponses.ForbiddenError{Reason: "no user record for the authenticated identity"}
		}
		return "", fmt.Errorf("failed to look up the approving user: %w", err)
	}
	return user.ID, nil
}

// approveServiceEnrollment implements Approve's service branch — see its
// doc comment. narrowed is req's already-resolved, server-config-bounded
// RequestedOptions.
func (s *CertRequestService) approveServiceEnrollment(ctx context.Context, requestID string, narrowed RequestedOptions) error {
	narrowedJSON, err := json.Marshal(narrowed)
	if err != nil {
		return fmt.Errorf("failed to encode narrowed options: %w", err) // excluded from coverage: RequestedOptions is a plain struct, json.Marshal can't fail on it, see exclude-from-coverage.txt
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

// approveForSigning implements Approve's user/PAM branch — see its doc
// comment. policy/narrowed are req.Type's already-resolved,
// server-config-bounded policy.
func (s *CertRequestService) approveForSigning(ctx context.Context, req model.CertificateRequest, identity *Identity, policy *certTypePolicy, narrowed RequestedOptions) error {
	keyID, err := executeKeyIDTemplate(policy.keyIDTemplate, keyIDTemplateData{
		Username: identity.Username,
		Subject:  identity.Subject,
		Email:    identity.Email,
		ClientIP: req.SourceIP,
		Hostname: req.Hostname,
		UniqueID: req.ID,
	})
	if err != nil {
		return fmt.Errorf("failed to compute key ID: %w", err) // excluded from coverage: parseKeyIDTemplate already executed policy.keyIDTemplate once against a zero-value keyIDTemplateData at construction to catch unresolvable fields; keyIDTemplateData is a flat struct of strings, so executing it again against real request data cannot newly fail, see exclude-from-coverage.txt
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
		Principals:       policy.principals(req.Hostname, req.Username, identity),
		KeyID:            keyID,
		RequestedOptions: narrowed,
		ValidAfter:       now,
		ValidBefore:      now.Add(policy.validDuration),
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("failed to encode signing job: %w", err) // excluded from coverage: certmsg.SigningJob is a plain struct, json.Marshal can't fail on it, see exclude-from-coverage.txt
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
		// excluded from coverage: requestOutcomeMessage is a plain struct, json.Marshal can't fail on it, see exclude-from-coverage.txt
		slog.Error("failed to encode certificate request outcome", "request_id", requestID, "error", err)
		return
	}

	if err := s.publisher.Publish(certmsg.WaitTopic(requestID), message.NewMessage(watermill.NewUUID(), payload)); err != nil {
		slog.Error("failed to publish certificate request outcome", "request_id", requestID, "error", err)
	}
}
