package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	sshcrypto "github.com/mnestor/ssoossh/internal/crypto/ssh"
	"github.com/mnestor/ssoossh/internal/fipsmode"
	"github.com/mnestor/ssoossh/internal/serial"
	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// NewCertRequestParams are the client-supplied inputs to CreateRequest.
// RequestedOptions is narrowed by server config before anything is shown in
// the web UI (server config is the outer bound — see docs/internals/invariants.md Hard
// Constraints). That narrowing isn't implemented yet (see Approve) — for
// now RequestedOptions is stored as submitted.
type NewCertRequestParams struct {
	Type             model.CertificateType
	PublicKey        string
	Username         string // set for CertificateTypePAM only — see model.CertificateRequest.Username
	SourceIP         string
	LocalUsername    string // set for CertificateTypeUser only — see model.CertificateRequest.LocalUsername
	LocalHostname    string // set for CertificateTypeUser only — see model.CertificateRequest.LocalHostname
	RequestedOptions RequestedOptions
}

// DecisionContext is the connection the approving/denying request arrived
// on — captured in the controller, where the *gin.Context is reachable, and
// passed down to Approve/Deny alongside the deciding identity. ForwardedFor
// is the raw X-Forwarded-For header, kept separately from SourceIP because
// g.ClientIP() already resolves that header down to one trusted address;
// the raw chain preserves forensic detail resolution throws away. This is a
// deliberate allowlist of headers, not "every header minus a denylist" —
// see model.CertificateRequestDecision's doc comment for why Cookie and
// Authorization are never captured.
type DecisionContext struct {
	SourceIP       string
	UserAgent      string
	AcceptLanguage string
	ForwardedFor   string
}

// ApprovalSelection carries the human's choices when approving a request,
// varying by certificate type. ServiceAccount is required for
// CertificateTypeService; Principals is optional for CertificateTypeUser
// and ignored for others. Empty/absent Principals on a user request
// defaults to []string{approver.Username} server-side, preserving existing
// behavior for direct API callers.
type ApprovalSelection struct {
	ServiceAccount string
	Principals     []string
}

// CertRequestProvider manages the pending-approval lifecycle for
// certificate requests. CertRequestService is the production
// implementation.
type CertRequestProvider interface {
	CreateRequest(ctx context.Context, p NewCertRequestParams) (requestID string, err error)
	Detail(ctx context.Context, requestID string, identity *Identity) (*RequestDetail, error)
	Approve(ctx context.Context, requestID string, identity *Identity, dc DecisionContext, selection ApprovalSelection) error
	Deny(ctx context.Context, requestID string, identity *Identity, dc DecisionContext) error
	Wait(ctx context.Context, requestID string) (WaitOutcome, error)
}

// WaitOutcome is what Approve/Deny/expiry hands to callers blocked in or
// reconnecting to Wait, and what Wait itself returns. Code is set instead
// of Certificate for CertificateRequestStatusEnrolled (CertificateTypeService
// — see Approve).
//
// Exported fields with one unexported one: the same value is both the
// cached entry and the return value, so a separate public shape would only
// be a second thing to keep in step. resolvedAt stays internal because it
// is bookkeeping for EvictResolved, not part of the answer.
type WaitOutcome struct {
	Status      model.CertificateRequestStatus
	Certificate string
	Code        string

	// ServiceAccount and ExpiresAt describe an enrollment and are set only
	// alongside Code. They exist because the operator running
	// `service enroll` never sees the approval screen: without them the CLI
	// can print a code but not whose certificate it will produce, or when
	// it stops working. Both are display detail — a lookup failure leaves
	// them zero rather than failing the wait.
	ServiceAccount string
	ExpiresAt      time.Time

	// resolvedAt is when this outcome was cached, and exists only so
	// EvictResolved can age entries out. Stamped centrally in notifyWaiter
	// and tryHandleWakeMessage rather than by callers, so no construction
	// site can forget it and leave an entry that looks infinitely old.
	resolvedAt time.Time
}

// requestOutcomeMessage is WaitOutcome's wire shape, published to a
// request's wake topic (see certmsg.WaitTopic). Wait itself never needs to decode
// this — the resolved-cache/DB checks at the top of its loop are the
// actual source of truth, and the message is only a low-latency signal
// that something changed — but it's still JSON so any future consumer
// (e.g. debugging, or a listener/resolver in a later phase — see
// docs/internals/signing-pipeline.md) can read it directly.
type requestOutcomeMessage struct {
	Status      model.CertificateRequestStatus `json:"status"`
	Certificate string                         `json:"certificate,omitempty"`
	Code        string                         `json:"code,omitempty"`

	// The enrollment detail that accompanies Code. Omitted when unset so a
	// message from an older instance decodes to the same zero values this
	// side already tolerates.
	ServiceAccount string     `json:"service_account,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
}

// CertRequestService manages the pending-approval lifecycle shared by all
// certificate types: a client creates a request (`ssh login`,
// `service enroll`) and its events endpoint waits for it to
// resolve (see server/controller/certrequests.go's eventsHandler); a human
// opens the approval URL that client printed and approves or denies it
// out-of-band, which is what unblocks that wait via publisher/subscriber
// below (see docs/internals/signing-pipeline.md). Requests are never listed — see
// NewCertRequestController for why.
//
// Approving a request behaves differently per Type:
//   - user, PAM: queue a signing job and resolve to a certificate
//   - service: create a model.Enrollment instead (see service/enrollment.go) —
//     the certificate itself isn't issued until `service retrieve`
//
// publisher/subscriber are gochannel-backed (in-process, in-memory) — see
// docs/dev/signer-split-deferred.md for when that becomes configurable.
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
	engine     *lifetimePolicyEngine

	// notifier reports enrollment creation to the approving user. Never
	// nil — SetNotifier is optional, and the zero value discards, so a
	// deployment with mail off needs no branch at the call site.
	notifier Notifier

	// auditor records the cert.* events. Nil until SetAuditor runs, and
	// left nil in tests that do not exercise auditing.
	auditor *AuditService

	mu sync.Mutex
	// resolved caches the outcome for any requestID notifyWaiter has fired
	// for, so a Wait call arriving after resolution (a late reconnect, or
	// one that was never blocked in the first place) reads the cached
	// outcome instead of waiting on a wake message that already happened.
	resolved map[string]WaitOutcome
	// TODO: ca signing dependency (reuse/extend CAService — it currently
	// only exposes GetCAPublicKey, not signing) and the lifetime-policy
	// computation (see docs/internals/design-brief.md "Certificate lifetime
	// policy") are still needed before Approve can actually issue a
	// certificate.
}

// NewCertRequestService constructs a CertRequestService, parsing
// c.CertOptions' per-type key ID templates (see
// docs/internals/certificate-keyid-template.md) so a bad template fails startup.
// publisher/subscriber back the wake-topic broker (see certmsg.WaitTopic) —
// the gochannel-based pair from server/pubsub.
func NewCertRequestService(c *config.Config, db *gorm.DB, publisher message.Publisher, subscriber message.Subscriber) (*CertRequestService, error) {
	keyIDTmpls, err := newKeyIDTemplates(c.CertOptions)
	if err != nil {
		return nil, err
	}

	engine, err := newLifetimePolicyEngine(c.CertOptions, c.AuthConfig.Fields.Extra)
	if err != nil {
		return nil, err
	}

	policies, err := newCertTypePolicies(c.CertOptions, keyIDTmpls, c.AuthConfig.Fields.Extra)
	if err != nil {
		return nil, err
	}

	return &CertRequestService{
		config:     c,
		db:         db,
		policies:   policies,
		publisher:  publisher,
		subscriber: subscriber,
		engine:     engine,
		resolved:   make(map[string]WaitOutcome),
		notifier:   discardNotifications{},
	}, nil
}

// SetNotifier attaches the notification publisher. Called from bootstrap
// after both services exist; a service constructed without it discards
// notifications rather than panicking, which is what keeps every existing
// test and the mail-disabled deployment working unchanged.
func (s *CertRequestService) SetNotifier(n Notifier) {
	if n != nil {
		s.notifier = n
	}
}

// SetAuditor attaches the audit recorder, on the same terms as
// SetNotifier: optional, wired at startup, and absent in tests that do not
// exercise it.
func (s *CertRequestService) SetAuditor(a *AuditService) { s.auditor = a }

// auditTx appends event inside tx, so an approval and its audit row commit
// together or not at all. A nil auditor is a no-op.
func (s *CertRequestService) auditTx(tx *gorm.DB, event AuditEvent) error {
	if s.auditor == nil {
		return nil
	}
	return s.auditor.RecordTx(tx, event)
}

// auditLog emits the shipped-log line for an event already written by
// auditTx, after that transaction has committed.
func (s *CertRequestService) auditLog(event AuditEvent) {
	if s.auditor == nil {
		return
	}
	s.auditor.LogOnly(event)
}

// auditRecord writes an event that has no transaction to ride along with.
func (s *CertRequestService) auditRecord(ctx context.Context, event AuditEvent) {
	if s.auditor == nil {
		return
	}
	s.auditor.Record(ctx, event)
}

// ValidateStartupConfig checks the lifetime policy configuration against the
// server's reverse-proxy configuration, logging warnings if a footgun is
// detected. Called once at startup after services are initialized, before
// Approve uses the policies to evaluate requests.
func (s *CertRequestService) ValidateStartupConfig() {
	s.engine.validateStartupConfig(s.config.HTTP.TrustedProxies)
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
// normalizeSourceAddresses returns addrs with link-local addresses and
// repeats removed, preserving first-seen order.
//
// Link-local (fe80::/10, 169.254.0.0/16) is dropped because it is meaningful
// only within a single link: it cannot support a source-address restriction,
// and it identifies nothing to a host the certificate is presented to. It is
// also the main source of repeats, since net.IP.String() drops the IPv6 zone
// and one address derived from a single MAC then arrives identically from
// every interface carrying it.
//
// A value that does not parse as an IP is kept rather than discarded: it
// says something about the client that sent it, and this is an audit record
// before it is a policy input. Only what is positively identified as
// link-local is removed.
//
// Linear scan rather than a map: this is one machine's interface addresses,
// so a handful of entries at most, and keeping the caller's order keeps the
// persisted value readable.
func normalizeSourceAddresses(addrs []string) []string {
	if len(addrs) == 0 {
		return addrs
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		if ip := net.ParseIP(a); ip != nil && ip.IsLinkLocalUnicast() {
			continue
		}
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *CertRequestService) CreateRequest(ctx context.Context, p NewCertRequestParams) (requestID string, err error) {
	// Union the server-observed source address into the caller's own
	// reported SourceAddresses before persisting, so the stored value is
	// the complete set docs/internals/design-brief.md's lifetime-policy section
	// describes: the client's own interfaces plus the address the server
	// observed the request coming from. This matters for a client behind
	// NAT — the address ssoosshd sees when it mints the certificate is not
	// the address downstream hosts see when the client connects to them,
	// so a source-address restriction built from the observed address
	// alone would wrongly reject the client's real connections. Plain
	// string-equality dedup is enough here; netip-normalized matching
	// belongs to the deferred source-address policy engine in
	// docs/operations/certificate-lifetime-policy.md, not this capture step.
	//
	// The caller's own list is normalized here rather than trusted to arrive
	// clean: any client at all — an older release, pam_ssoossh, something
	// third-party — decides what it sends. This value is persisted, rendered
	// on the approval screen, and matched against later, so it is cleaned on
	// the way in rather than at each of those.
	//
	// The server-observed SourceIP below is not put through the same filter.
	// It is a fact the server established about this connection rather than a
	// claim the client made, and it is worth recording even when the client
	// reached ssoosshd over a link-local address.
	p.RequestedOptions.SourceAddresses = normalizeSourceAddresses(p.RequestedOptions.SourceAddresses)
	if p.SourceIP != "" && !slices.Contains(p.RequestedOptions.SourceAddresses, p.SourceIP) {
		p.RequestedOptions.SourceAddresses = append(p.RequestedOptions.SourceAddresses, p.SourceIP)
	}

	optionsJSON, err := json.Marshal(p.RequestedOptions)
	if err != nil {
		// not covered: RequestedOptions is a plain struct of strings,
		// bools and slices, so json.Marshal cannot fail on it.
		return "", fmt.Errorf("failed to encode requested options: %w", err)
	}

	req := model.CertificateRequest{
		ID:               uuid.NewString(),
		Type:             p.Type,
		PublicKey:        p.PublicKey,
		Username:         p.Username,
		RequestedOptions: string(optionsJSON),
		SourceIP:         p.SourceIP,
		LocalUsername:    p.LocalUsername,
		LocalHostname:    p.LocalHostname,
		Status:           model.CertificateRequestStatusPending,
		CreatedAt:        time.Now(),
	}

	if err := s.db.WithContext(ctx).Create(&req).Error; err != nil {
		return "", fmt.Errorf("failed to persist certificate request: %w", err)
	}

	// A request is created by an unauthenticated client, so nothing knows
	// whose it is yet: the event carries no actor and no target, only the
	// connection detail an incident reviewer would want.
	s.auditRecord(ctx, AuditEvent{
		Action:     AuditCertRequested,
		OccurredAt: req.CreatedAt,
		Detail: map[string]any{
			"request_id":     req.ID,
			"cert_type":      string(req.Type),
			"source_ip":      req.SourceIP,
			"local_username": req.LocalUsername,
			"local_hostname": req.LocalHostname,
		},
	})

	return req.ID, nil
}

// ttlCutoff returns the CreatedAt threshold before which a still-pending
// request is treated as expired: created before this instant, no longer
// approvable. A zero ApprovalTTL disables expiry (returns the zero Time, so
// no request's CreatedAt is ever before it).
//
// UTC, and it has to be: this value is compared against created_at, which
// SQLite compares as a string. A local-offset cutoff against UTC-stored
// rows compares by literal digits rather than by instant. See package
// dbtime.
func (s *CertRequestService) ttlCutoff() time.Time {
	if s.config.CertOptions.ApprovalTTL() <= 0 {
		return time.Time{}
	}
	return time.Now().Add(-s.config.CertOptions.ApprovalTTL()).UTC()
}

// EvictResolved drops cached outcomes older than the request TTL. Without
// it the resolved map grows one entry per request for the life of the
// process — and each entry holds a signed certificate, which this design
// otherwise takes care never to persist anywhere.
//
// ApprovalTTL is the right bound and needs no grace period: resolvedAt is
// never earlier than the request's created_at, so an entry older than the
// TTL belongs to a request that has itself expired. Wait's expiry timer is
// measured from created_at, so no client can still be waiting on it, and a
// late reconnect gets the same 410 it would have got anyway.
//
// Config guarantees ClientTimeout > 0 (CertificateOptions.Validate), and
// ApprovalTTL is a fixed fraction of it, so there is no disabled-TTL case
// to fall back on here.
//
// This must run on EVERY instance. The map is process-local memory, so
// unlike the database sweep it can never be gated behind leader election —
// electing one leader would leave every other instance leaking. See
// docs/dev/multi-instance-safety-plan.md item 3.
func (s *CertRequestService) EvictResolved(_ context.Context) error {
	cutoff := time.Now().Add(-s.config.CertOptions.ApprovalTTL())

	s.mu.Lock()
	defer s.mu.Unlock()
	for id, outcome := range s.resolved {
		if outcome.resolvedAt.Before(cutoff) {
			delete(s.resolved, id)
		}
	}
	return nil
}

// RequestDetail is everything the approval page needs to show a human what
// they are about to authorize.
//
// Requested and Narrowed are both present on purpose. Server config is the
// outer bound on every option, and options a deployment doesn't permit are
// trimmed rather than rejected — so the UI has to be able to show what was
// asked for alongside what would actually be granted, before anyone
// approves anything (see docs/internals/invariants.md).
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

	// Decision is the request's audit record — nil for a still-pending
	// request, since most requests being viewed haven't been decided yet.
	// See model.CertificateRequestDecision.
	Decision *model.CertificateRequestDecision
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

	user, err := s.resolveUser(ctx, identity)
	if err != nil {
		return nil, err
	}
	if err := s.bindRequester(ctx, &req, user); err != nil {
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

	decision, err := s.lookupDecision(ctx, req.ID)
	if err != nil {
		// not covered: failing this query while leaving Detail's earlier
		// lookup and bindRequester's query intact needs per-query DB fault
		// injection, which this codebase has no helper for.
		// TestLookupDecision_ShouldSurfaceAGenericDBError covers
		// lookupDecision's own error branch directly instead.
		return nil, err
	}

	// Pass empty principals for the preview: user-type requests default to the
	// approver's username in the preview (since no selection has been made yet),
	// and PAM/service don't use this field.
	principals := policy.principals(req.Username, identity, nil)
	for _, p := range principals {
		if err := sshcrypto.ValidatePrincipal(p); err != nil {
			return nil, fmt.Errorf("invalid principal: %w", err)
		}
	}

	return &RequestDetail{
		Request:       req,
		Requested:     requested,
		Narrowed:      narrowRequestedOptions(policy, requested),
		Principals:    principals,
		ValidDuration: policy.validDuration,
		Decision:      decision,
	}, nil
}

// lookupDecision returns requestID's decision record, or nil if it hasn't
// been decided yet — "not found" is the expected, common case here (most
// requests being viewed are still pending), not an error.
func (s *CertRequestService) lookupDecision(ctx context.Context, requestID string) (*model.CertificateRequestDecision, error) {
	var decision model.CertificateRequestDecision
	err := s.db.WithContext(ctx).First(&decision, "certificate_request_id = ?", requestID).Error
	switch {
	case err == nil:
		return &decision, nil
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil, nil
	default:
		return nil, fmt.Errorf("failed to look up certificate request decision: %w", err)
	}
}

// Approve resolves policy for requestID against server config and identity,
// then branches by type:
//
//   - user, PAM: marks the request CertificateRequestStatusSigning and
//     publishes a self-contained signingJob to certmsg.SignQueueTopic — the queue is
//     the only entrypoint that turns an approved request into a signed
//     certificate (see docs/internals/signing-pipeline.md). The certificate is
//     delivered later, over the client's own Wait/SSE connection, once the
//     signer and listener/resolver process the job.
//   - service: does NOT use the queue. Marks the request
//     CertificateRequestStatusEnrolled with a freshly generated
//     EnrollmentToken directly, and notifies the wake topic itself, right
//     here in the approving request — there's no signer round trip to wait
//     on, since the certificate itself isn't produced until a later
//     `service retrieve` redeems the token (see
//     docs/internals/design-brief.md, "Service enrollment"). The client waiting on
//     Wait/SSE for this request gets the token, not a certificate.
//
// Policy resolution (applies to both branches):
//   - Extensions are narrowed to the intersection of requested and
//     configured-permitted (server config is always the outer bound — see
//     docs/internals/invariants.md).
//   - ForceCommand and SourceAddresses are dropped entirely: there's no
//     server config concept yet to bound either against (source-address
//     policy is explicitly "design in progress," see
//     docs/internals/design-brief.md), and granting an unbounded client-requested
//     critical option would violate the same hard constraint. Revisit once
//     that policy exists.
//   - NoTouchRequired is only ever granted for CertificateTypeService, per
//     docs/internals/invariants.md.
//   - RequireGroup (service/PAM config) is enforced: identity must be
//     a member, or Approve fails without publishing/enrolling anything. PAM
//     is the one type where an unset RequireGroup denies rather than opens
//     — see CertOptionsPAM.RequireGroup.
//   - Principals: User-type requests carry the approver's selection (or
//     default to the approver's username if none was selected), validated
//     against the set of accounts the approver holds (username plus
//     OtherAccounts). PAM's principal is the local account named on the
//     request (req.Username). Service certificates use the selected
//     ServiceAccount — required, and it must be one of the approver's own
//     identity.ServiceAccounts (account linkage: approving for an account
//     you aren't associated with is refused). Empty/absent Principals on a
//     user request preserves existing behavior for direct API callers.
func (s *CertRequestService) Approve(ctx context.Context, requestID string, identity *Identity, dc DecisionContext, selection ApprovalSelection) error {
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

	// The session-built identity carries no Extra (see
	// middleware.SessionAuthMiddleware), so claim conditions and the key ID
	// template's extra fields are hydrated from the approver's users row,
	// persisted at login. Resolved BEFORE the authorization gate so a
	// require condition can read claims, and before bindRequester so a
	// caller who cannot approve never claims the request. identity is
	// per-request, so mutating it stays local.
	user, err := s.resolveUser(ctx, identity)
	if err != nil {
		return err
	}
	identity.Extra = decodeExtraFields(user.ExtraFields)

	if err := checkApproverAuthorization(req.Type, policy, identity, selection); err != nil {
		return err
	}

	if err := s.bindRequester(ctx, &req, user); err != nil {
		return err
	}

	if s.config.FIPSEnabled() {
		if err := s.checkFIPSApproved(req.PublicKey); err != nil {
			return err
		}
	}

	switch policy.flow {
	case flowEnrollment:
		return s.approveServiceEnrollment(ctx, req, narrowed, identity, policy, dc, selection.ServiceAccount)
	case flowSigning:
		return s.approveForSigning(ctx, req, identity, policy, narrowed, dc, selection.Principals)
	default:
		// This should never happen — every certificate type in
		// newCertTypePolicies maps to either flowEnrollment or flowSigning.
		// This guard exists only to catch bugs in policy initialization.
		return fmt.Errorf("unsupported certificate approval flow for %s", req.Type)
	}
}

// checkApproverAuthorization decides whether identity may approve a request
// of certType at all: the type's require condition (group membership, claim
// thresholds), plus the per-type account linkage that ties the
// certificate's principals to accounts the approver actually holds.
//
// Split out of Approve both to keep that function readable and because
// every check here shares one requirement — it must run before
// bindRequester, so a caller who cannot approve never claims the request.
// The require condition additionally needs identity.Extra hydrated, which
// is why Approve resolves the users row first.
func checkApproverAuthorization(certType model.CertificateType, policy *certTypePolicy, identity *Identity, selection ApprovalSelection) error {
	// Both rules below come from the policy table rather than a switch here,
	// so adding a certificate type means filling in newCertTypePolicies
	// rather than remembering to extend this function.
	if policy.require != nil && !policy.require.evaluate(identity) {
		return fmt.Errorf("identity is not authorized to approve %s certificates", certType)
	}

	if policy.linkage != nil {
		return policy.linkage(identity, selection)
	}
	return nil
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
//
// Takes the already-resolved users row (see resolveUser): Approve resolves
// it before the authorization gate, so binding — which claims the request —
// stays the last step a caller reaches.
func (s *CertRequestService) bindRequester(ctx context.Context, req *model.CertificateRequest, user model.User) error {
	userID := user.ID

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
		// not covered: failing this query and not resolveUser's (which
		// is tested) needs per-query DB fault injection, which this
		// codebase has no helper for.
		return fmt.Errorf("failed to bind certificate request to user: %w", result.Error)
	}

	if result.RowsAffected == 0 {
		var claimed model.CertificateRequest
		if err := s.db.WithContext(ctx).First(&claimed, "id = ?", req.ID).Error; err != nil {
			// not covered: failing the re-read and not the guarded UPDATE
			// above it needs per-query DB fault injection, which this
			// codebase has no helper for.
			return fmt.Errorf("failed to re-read certificate request after a racing claim: %w", err)
		}
		if claimed.UserID == nil || *claimed.UserID != userID {
			return &errorresponses.ForbiddenError{Reason: "certificate request belongs to another user"}
		}
	}

	req.UserID = &userID
	return nil
}

// resolveUser maps identity to its users row, keyed on the OIDC subject.
// The row is written at login (AuthService.upsertUser), so a miss means the
// caller's session outlived its user record.
func (s *CertRequestService) resolveUser(ctx context.Context, identity *Identity) (model.User, error) {
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.User{}, &errorresponses.ForbiddenError{Reason: "no user record for the authenticated identity"}
		}
		return model.User{}, fmt.Errorf("failed to look up the approving user: %w", err)
	}
	return user, nil
}

// approveServiceEnrollment implements Approve's service branch — see its
// doc comment. narrowed is req's already-resolved, server-config-bounded
// RequestedOptions. policy is req.Type's certTypePolicy.
func (s *CertRequestService) approveServiceEnrollment(ctx context.Context, req model.CertificateRequest, narrowed RequestedOptions, identity *Identity, policy *certTypePolicy, dc DecisionContext, serviceAccount string) error {
	// Compute certificate and enrollment-code lifetimes using the policy
	// engine, then apply its extension grants and source-rule narrowing.
	outcome := s.engine.evaluate(req.Type, identity, req.SourceIP, policy.validDuration, policy.enrollmentDuration)
	effectiveDuration := outcome.duration
	narrowed = outcome.narrowOptions(narrowed, req.SourceIP)

	narrowedJSON, err := json.Marshal(narrowed)
	if err != nil {
		// not covered: RequestedOptions is a plain struct, so json.Marshal
		// cannot fail on it.
		return fmt.Errorf("failed to encode narrowed options: %w", err)
	}

	// Key ID and principals are fixed here, not at retrieve time: the
	// enrollment contract is evaluate-at-enrollment-time (see
	// docs/operations/certificate-lifetime-policy.md), and the approving identity —
	// which both derive from — no longer exists when `service retrieve`
	// redeems the code unattended.
	keyID, err := executeKeyIDTemplate(policy.keyIDTemplate, newKeyIDTemplateData(identity, req.SourceIP, req.ID))
	if err != nil {
		// not covered: parseKeyIDTemplate already executed
		// policy.keyIDTemplate once against a zero-value keyIDTemplateData
		// at construction, extra lookups render MISSING rather than
		// erroring (missingkey=zero), and the data is plain strings and
		// extraValues, so executing against real data cannot newly fail.
		return fmt.Errorf("failed to compute key ID: %w", err)
	}

	// The chosen service account (validated against the approver's own
	// identity.ServiceAccounts in Approve) is the certificate principal —
	// the whole point of the linkage check is that the certificate names
	// the account, not the human who approved it.
	principals := []string{serviceAccount}
	for _, p := range principals {
		if err := sshcrypto.ValidatePrincipal(p); err != nil {
			return fmt.Errorf("invalid principal: %w", err)
		}
	}
	principalsJSON, err := json.Marshal(principals)
	if err != nil {
		// not covered: a []string, so json.Marshal cannot fail on it.
		return fmt.Errorf("failed to encode principals: %w", err)
	}

	token := uuid.NewString()
	enrollmentID := uuid.NewString()
	fingerprint, keyType := describeAuthorizedKey(req.PublicKey)
	now := time.Now()
	certDurationSeconds := int64(effectiveDuration / time.Second)
	// The code's own lifetime, not the certificate's. These were one value
	// until it became clear that they pull in opposite directions — see
	// config.CertOptionsService.EnrollmentDuration. The policy engine tiers
	// it (max_enrollment_duration) under the enrollment_duration ceiling.
	// effectiveDuration still bounds each certificate, applied at
	// redemption rather than here.
	expiresAt := now.Add(outcome.enrollmentDuration)

	decision, err := newDecision(req.ID, model.CertificateRequestDecisionApproved, identity, dc, now, &outcome.explanation)
	if err != nil {
		// not covered: newDecision can only fail through its own
		// json.Marshal calls, unreachable at their own definition.
		return err
	}

	// The code itself is deliberately absent: it is a bearer credential,
	// and the never-log-sensitive-data rule covers audit payloads too.
	auditEvent := AuditEvent{
		Action: AuditEnrollmentCodeCreated,
		// derefOrEmpty, not a dereference: the guarded UPDATE inside the
		// transaction below is what establishes req.UserID is set, and
		// this is built before it runs.
		Actor:      AuditSubjectFromIdentity(identity, derefOrEmpty(req.UserID)),
		OccurredAt: now,
		Detail: map[string]any{
			"request_id":      req.ID,
			"enrollment_id":   enrollmentID,
			"service_account": serviceAccount,
			"key_id":          keyID,
			"principals":      principals,
			"source_ip":       req.SourceIP,
			"expires_at":      expiresAt,
			"cert_lifetime":   effectiveDuration.String(),
		},
	}

	// The status update, enrollment creation, and decision-audit insert are
	// introduced together by this change, so they're wrapped in one transaction
	// rather than adding a new inconsistency window while already touching
	// this path. This is narrower than the pre-existing, separately
	// tracked lack of a transaction across Approve's bind/resolve/queue
	// writes (2026-08-21 security audit) — that finding stays open.
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.CertificateRequest{}).
			Where("id = ? AND status = ?", req.ID, model.CertificateRequestStatusPending).
			Updates(map[string]any{
				"status":            model.CertificateRequestStatusEnrolled,
				"requested_options": string(narrowedJSON),
				"enrollment_token":  token,
				"service_account":   serviceAccount,
				"resolved_at":       now,
			})
		if result.Error != nil {
			// not covered: failing this query while leaving the enclosing
			// Transaction() able to begin needs per-query DB fault
			// injection, which this codebase has no helper for.
			return fmt.Errorf("failed to mark certificate request as enrolled: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("certificate request %q is not pending", req.ID)
		}

		// Create the enrollment record with both computed lifetimes.
		enrollment := &model.Enrollment{
			ID:                         enrollmentID,
			Code:                       token,
			PublicKey:                  req.PublicKey,
			OptionSet:                  string(narrowedJSON),
			KeyID:                      keyID,
			Principals:                 string(principalsJSON),
			CertificateRequestID:       &req.ID,
			UserID:                     *req.UserID, // req.UserID was bound in Approve
			CreatedAt:                  now,
			ExpiresAt:                  expiresAt,
			CertificateDurationSeconds: &certDurationSeconds,
		}
		if err := tx.Create(enrollment).Error; err != nil {
			return fmt.Errorf("failed to create enrollment: %w", err)
		}

		if err := tx.Create(decision).Error; err != nil {
			return fmt.Errorf("failed to record approval decision: %w", err)
		}
		// Same transaction as the state change it describes, so an
		// enrollment without its audit row is unrepresentable.
		if err := s.auditTx(tx, auditEvent); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// Best-effort archive copy, after the row is durable.
	s.auditLog(auditEvent)

	// Read only now that the transaction has committed: req.UserID is bound
	// by Approve, and the guarded UPDATE inside the transaction is what
	// establishes that this call came through it rather than racing past a
	// request that is no longer pending.
	enrollmentUserID := *req.UserID

	// No signer round trip for enrollment — notify the wake topic directly
	// from here, unlike the user/PAM queue-and-wait path.
	s.notifyWaiter(req.ID, WaitOutcome{
		Status:         model.CertificateRequestStatusEnrolled,
		Code:           token,
		ServiceAccount: serviceAccount,
		ExpiresAt:      expiresAt,
	})

	// Queued, not sent: the caller is a browser waiting on the approval,
	// and it must not wait on a mail relay. Emitted after the waiter is
	// woken for the same reason — the operator's terminal gets its code
	// first, whatever the notification path does next.
	//
	// Deliberately without the code. It is a bearer credential shown once
	// in the terminal that ran `service enroll`; everything else the
	// operator was told is here. See notify.ServiceEnrollmentCreated.
	s.notifier.Notify(ctx, notify.KindServiceEnrollmentCreated, enrollmentUserID, &notify.ServiceEnrollmentCreated{
		ServiceAccount:       serviceAccount,
		RequestID:            req.ID,
		EnrollmentID:         enrollmentID,
		KeyID:                keyID,
		Principals:           principals,
		PublicKeyFingerprint: fingerprint,
		PublicKeyType:        keyType,
		Extensions:           narrowed.Extensions,
		ForceCommand:         narrowed.ForceCommand,
		SourceAddresses:      narrowed.SourceAddresses,
		NoTouchRequired:      narrowed.NoTouchRequired,
		RequestSourceIP:      req.SourceIP,
		ApprovedAt:           now,
		ApprovedByUsername:   identity.Username,
		CodeExpiresAt:        expiresAt,
		CertificateLifetime:  effectiveDuration,
		ServerURL:            s.config.HTTP.PublicOrigin(),
	})

	return nil
}

// describeAuthorizedKey summarizes a public key for a notification: its
// algorithm and SHA256 fingerprint, never the key itself.
//
// Errors are swallowed into empty strings on purpose. The key already
// passed parsing on its way into the request, and a notification that
// omits a fingerprint is worth more than an approval that fails because
// its notification could not be decorated.
func describeAuthorizedKey(authorizedKey string) (fingerprint, keyType string) {
	parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorizedKey)) //nolint:dogsled // the comment/options/rest returns say nothing about the key.
	if err != nil {
		return "", ""
	}
	return ssh.FingerprintSHA256(parsed), parsed.Type()
}

// checkServiceAccountLinkage enforces service account linkage on a
// service-type approval: the approver must name the service account, and
// only one their identity is actually associated with — group membership
// alone doesn't let someone mint a certificate for an arbitrary account.
func checkServiceAccountLinkage(identity *Identity, serviceAccount string) error {
	if serviceAccount == "" {
		return fmt.Errorf("approving a service certificate requires choosing a service account")
	}
	if !slices.Contains(identity.ServiceAccounts, serviceAccount) {
		return fmt.Errorf("identity is not associated with service account %q", serviceAccount)
	}
	return nil
}

// checkUserPrincipalLinkage enforces principal linkage on a user-type
// approval: every selected principal must be either the approver's own
// username or one of their OtherAccounts. Rejects any principal the
// approver doesn't hold, preventing a caller from handing themselves
// access they haven't been granted.
func checkUserPrincipalLinkage(identity *Identity, selected []string) error {
	// Build the set of principals the approver holds: their username plus
	// any other accounts.
	allowed := make(map[string]bool)
	allowed[identity.Username] = true
	for _, account := range identity.OtherAccounts {
		allowed[account] = true
	}

	// Every selected principal must be in the allowed set.
	for _, principal := range selected {
		if !allowed[principal] {
			return &errorresponses.ForbiddenError{Reason: fmt.Sprintf("approver does not hold principal %q", principal)}
		}
	}
	return nil
}

// newDecision builds the immutable audit record for a single Approve/Deny
// resolution of requestID, snapshotting identity's full six fields, dc's
// connection context, and the policy explanation (nil for a denial, which
// issues nothing to explain). Plain copied values, not a reference to the
// users table — see model.CertificateRequestDecision's doc comment for why.
func newDecision(requestID string, outcome model.CertificateRequestDecisionOutcome, identity *Identity, dc DecisionContext, decidedAt time.Time, explanation *PolicyExplanation) (*model.CertificateRequestDecision, error) {
	groupsJSON, err := json.Marshal(identity.Groups)
	if err != nil {
		// not covered (this branch and the two below): all three are
		// []string, so json.Marshal cannot fail on them.
		return nil, fmt.Errorf("failed to encode decision groups: %w", err)
	}
	otherAccountsJSON, err := json.Marshal(identity.OtherAccounts)
	if err != nil {
		return nil, fmt.Errorf("failed to encode decision other accounts: %w", err)
	}
	serviceAccountsJSON, err := json.Marshal(identity.ServiceAccounts)
	if err != nil {
		return nil, fmt.Errorf("failed to encode decision service accounts: %w", err)
	}

	var explanationJSON string
	if explanation != nil {
		encoded, err := json.Marshal(explanation)
		if err != nil {
			// not covered: PolicyExplanation is a plain struct of strings
			// and slices, so json.Marshal cannot fail on it.
			return nil, fmt.Errorf("failed to encode policy explanation: %w", err)
		}
		explanationJSON = string(encoded)
	}

	return &model.CertificateRequestDecision{
		ID:                   uuid.NewString(),
		CertificateRequestID: requestID,
		Outcome:              outcome,
		Subject:              identity.Subject,
		Username:             identity.Username,
		Email:                identity.Email,
		Groups:               string(groupsJSON),
		OtherAccounts:        string(otherAccountsJSON),
		ServiceAccounts:      string(serviceAccountsJSON),
		SourceIP:             dc.SourceIP,
		UserAgent:            dc.UserAgent,
		AcceptLanguage:       dc.AcceptLanguage,
		ForwardedFor:         dc.ForwardedFor,
		PolicyExplanation:    explanationJSON,
		DecidedAt:            decidedAt,
	}, nil
}

// approveForSigning implements Approve's user/PAM branch — see its doc
// comment. policy/narrowed are req.Type's already-resolved,
// server-config-bounded policy. selectedPrincipals is the approver's
// selection for user-type requests and is ignored for PAM.
func (s *CertRequestService) approveForSigning(ctx context.Context, req model.CertificateRequest, identity *Identity, policy *certTypePolicy, narrowed RequestedOptions, dc DecisionContext, selectedPrincipals []string) error {
	keyID, err := executeKeyIDTemplate(policy.keyIDTemplate, newKeyIDTemplateData(identity, req.SourceIP, req.ID))
	if err != nil {
		// not covered: parseKeyIDTemplate already executed
		// policy.keyIDTemplate once against a zero-value keyIDTemplateData
		// at construction to catch unresolvable fields, extra lookups
		// render MISSING rather than erroring (missingkey=zero), and the
		// data is plain strings and extraValues, so executing it again
		// against real request data cannot newly fail.
		return fmt.Errorf("failed to compute key ID: %w", err)
	}

	// Allocate certificate serial now, before queuing the signing job,
	// so it's available to persist at resolution without waiting for the
	// signer. This avoids burning serials on signing failures.
	serialNum, err := serial.New()
	if err != nil {
		return fmt.Errorf("failed to allocate certificate serial: %w", err)
	}

	now := time.Now()

	// Compute certificate lifetime using the policy engine, which evaluates
	// tiers and source network rules, then apply its extension grants and
	// source-rule narrowing.
	outcome := s.engine.evaluate(req.Type, identity, req.SourceIP, policy.validDuration, 0)
	effectiveDuration := outcome.duration
	narrowed = outcome.narrowOptions(narrowed, req.SourceIP)

	decision, err := newDecision(req.ID, model.CertificateRequestDecisionApproved, identity, dc, now, &outcome.explanation)
	if err != nil {
		// not covered: newDecision can only fail through its own
		// json.Marshal calls, unreachable at their own definition.
		return err
	}

	auditEvent := AuditEvent{
		Action:     AuditCertApproved,
		Actor:      AuditSubjectFromIdentity(identity, derefOrEmpty(req.UserID)),
		OccurredAt: now,
		Detail: map[string]any{
			"request_id":    req.ID,
			"cert_type":     string(req.Type),
			"key_id":        keyID,
			"serial":        serialNum,
			"source_ip":     req.SourceIP,
			"cert_lifetime": effectiveDuration.String(),
		},
	}

	// See approveServiceEnrollment's comment on why this pair is
	// transactional but the wider bind/resolve/queue sequence stays out of
	// scope here.
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.CertificateRequest{}).
			Where("id = ? AND status = ?", req.ID, model.CertificateRequestStatusPending).
			Updates(map[string]any{"status": model.CertificateRequestStatusSigning, "serial_number": serialNum})
		if result.Error != nil {
			// not covered: failing this query while leaving the enclosing
			// Transaction() able to begin needs per-query DB fault
			// injection, which this codebase has no helper for.
			return fmt.Errorf("failed to mark certificate request as signing: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("certificate request %q is not pending", req.ID)
		}
		if err := tx.Create(decision).Error; err != nil {
			return fmt.Errorf("failed to record approval decision: %w", err)
		}
		if err := s.auditTx(tx, auditEvent); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.auditLog(auditEvent)

	// Principals are derived per-type: user uses the approver's selection
	// (or defaults to their username), PAM uses the local account being
	// authenticated. Validate every one before it can be persisted or
	// signed into a certificate; the signer re-checks as a backstop.
	principals := policy.principals(req.Username, identity, selectedPrincipals)
	for _, p := range principals {
		if err := sshcrypto.ValidatePrincipal(p); err != nil {
			return fmt.Errorf("invalid principal: %w", err)
		}
	}

	job := certmsg.SigningJob{
		RequestID:        req.ID,
		Type:             req.Type,
		PublicKey:        req.PublicKey,
		Principals:       principals,
		KeyID:            keyID,
		RequestedOptions: narrowed,
		ValidAfter:       now,
		ValidBefore:      now.Add(effectiveDuration),
		Serial:           serialNum,
	}
	payload, err := json.Marshal(job)
	if err != nil {
		// not covered: certmsg.SigningJob is a plain struct, so
		// json.Marshal cannot fail on it.
		return fmt.Errorf("failed to encode signing job: %w", err)
	}

	// If this publish fails, the row is left in Signing with no queued
	// job — a stuck row the invalidation sweep is responsible for
	// catching, not something recovered here (see
	// docs/internals/signing-pipeline.md).
	if err := s.publisher.Publish(certmsg.SignQueueTopic, message.NewMessage(watermill.NewUUID(), payload)); err != nil {
		return fmt.Errorf("failed to publish signing job: %w", err)
	}

	return nil
}

// derefOrEmpty reads an optional id column into a plain string, for the
// audit grouping keys where "not yet bound" and "no user" are both simply
// no key.
func derefOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
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

	out := make([]string, 0, len(requested))
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
func (s *CertRequestService) Deny(ctx context.Context, requestID string, identity *Identity, dc DecisionContext) error {
	now := time.Now()

	decision, err := newDecision(requestID, model.CertificateRequestDecisionDenied, identity, dc, now, nil)
	if err != nil {
		// not covered: newDecision can only fail through its own
		// json.Marshal calls on []string, unreachable at their own
		// definition.
		return err
	}

	auditEvent := AuditEvent{
		Action:     AuditCertDenied,
		Actor:      AuditSubjectFromIdentity(identity, ""),
		OccurredAt: now,
		Detail:     map[string]any{"request_id": requestID},
	}

	// See approveServiceEnrollment's comment on why this pair is
	// transactional but the wider bind/resolve/queue sequence stays out of
	// scope here.
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		q := tx.Model(&model.CertificateRequest{}).
			Where("id = ? AND status = ?", requestID, model.CertificateRequestStatusPending)
		if cutoff := s.ttlCutoff(); !cutoff.IsZero() {
			q = q.Where("created_at > ?", cutoff)
		}
		result := q.Updates(map[string]any{
			"status":      model.CertificateRequestStatusDenied,
			"resolved_at": now,
		})
		if result.Error != nil {
			// not covered: failing this query while leaving the enclosing
			// Transaction() able to begin needs per-query DB fault
			// injection, which this codebase has no helper for.
			return fmt.Errorf("failed to deny certificate request: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("certificate request %q is not pending", requestID)
		}
		if err := tx.Create(decision).Error; err != nil {
			return fmt.Errorf("failed to record denial decision: %w", err)
		}
		if err := s.auditTx(tx, auditEvent); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	s.auditLog(auditEvent)

	s.notifyWaiter(requestID, WaitOutcome{Status: model.CertificateRequestStatusDenied})

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
func (s *CertRequestService) Wait(ctx context.Context, requestID string) (WaitOutcome, error) {
	// Fast path: an outcome already cached needs no broker at all, which
	// matters for the SSE-reconnect case this method is explicitly safe
	// for.
	s.mu.Lock()
	if outcome, ok := s.resolved[requestID]; ok {
		s.mu.Unlock()
		return outcome, nil
	}
	s.mu.Unlock()

	// One subscription, taken before the first database read and held for
	// the whole call.
	//
	// Subscribing before the read closes the window where a wake fires
	// between the two: it lands on this channel instead of being missed.
	// Holding it across loop iterations closes the same window *between*
	// iterations, which is the one that actually bit — neither broker
	// replays. gochannel is deliberately not Persistent (see
	// server/pubsub/pubsub.go) and core NATS has no replay at all, so a
	// wake published while nothing is subscribed is gone. Under NATS that
	// is unrecoverable rather than merely late: the certificate is never
	// persisted (docs/internals/signing-pipeline.md), so the reconcileStatus read
	// below can see "approved" on another instance and still have nothing
	// to hand back, leaving the client blocked until its request expires.
	messages, err := s.subscriber.Subscribe(ctx, certmsg.WaitTopic(requestID))
	if err != nil {
		return WaitOutcome{}, fmt.Errorf("failed to subscribe to certificate request updates: %w", err)
	}

	for {
		s.mu.Lock()
		if outcome, ok := s.resolved[requestID]; ok {
			s.mu.Unlock()
			return outcome, nil
		}
		s.mu.Unlock()

		req, err := s.lookupRequest(ctx, requestID)
		if err != nil {
			return WaitOutcome{}, err
		}

		block, err := s.reconcileStatus(ctx, requestID, req)
		if err != nil {
			return WaitOutcome{}, err
		}
		if !block {
			continue
		}

		// Block until an incoming message, context cancellation, or request
		// expiration. Stop the timer as soon as the select returns rather than
		// deferring it: this loop can iterate many times within one Wait call,
		// and a deferred Stop would hold every timer until Wait finally returns.
		expireC, stopExpiry := s.expiryTimer(req)
		msg, err := s.waitForUpdate(ctx, messages, expireC)
		stopExpiry()
		if err != nil {
			return WaitOutcome{}, err
		}

		// A wake message may carry the outcome directly. Trust it only after
		// tryHandleWakeMessage confirms the status against the database, which
		// stays the authority for the approval decision; the message is a
		// wakeup plus an optimization that saves a second round-trip. This is
		// what lets an SSE client on instance B receive a certificate issued
		// on instance A. Anything unverified falls through and re-reads the DB.
		if msg != nil {
			if outcome, handled := s.tryHandleWakeMessage(ctx, requestID, msg); handled {
				return outcome, nil
			}
		}
	}
}

// expiryTimer returns a channel that fires when the request's TTL elapses,
// measured from its creation time, together with a stop function the caller
// must invoke once the select has returned.
//
// Measuring from req.CreatedAt rather than from now is what stops a
// reconnecting client extending its own deadline indefinitely, by calling
// Wait every four minutes against a five minute TTL.
//
// With no TTL configured, or with the deadline already behind us, the
// returned channel is nil. Receiving from a nil channel blocks forever, so
// that case simply never fires and expiry is left to reconcileStatus.
func (s *CertRequestService) expiryTimer(req model.CertificateRequest) (<-chan time.Time, func()) {
	ttl := s.config.CertOptions.ApprovalTTL()
	if ttl <= 0 {
		return nil, func() {}
	}

	remaining := time.Until(req.CreatedAt.Add(ttl))
	if remaining <= 0 {
		return nil, func() {}
	}

	timer := time.NewTimer(remaining)
	return timer.C, func() { timer.Stop() }
}

// waitForUpdate blocks until an incoming message, context cancellation, or
// request expiration. expireC may be nil (no TTL configured); receiving from
// a nil channel blocks forever, so that case simply never fires and no
// separate code path is needed for it.
//
// It returns the received message so the caller can inspect its payload with
// requestID in scope, nil when the expiry timer fired or the subscription
// closed, or ctx.Err() if the context was cancelled.
func (s *CertRequestService) waitForUpdate(ctx context.Context, messages <-chan *message.Message, expireC <-chan time.Time) (*message.Message, error) {
	select {
	case msg, ok := <-messages:
		if !ok {
			return nil, nil
		}
		msg.Ack()
		return msg, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-expireC:
		// Request may have expired; the caller loops back to reconcileStatus,
		// which applies the TTL cutoff and marks it expired.
		return nil, nil
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
		// docs/internals/signing-pipeline.md) — not yet resolved. No TTL
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
		// full from the database. The chosen account is on the row too; the
		// code's expiry lives on the enrollment it created, so that takes a
		// second read.
		s.notifyWaiter(requestID, WaitOutcome{
			Status:         req.Status,
			Code:           req.EnrollmentToken,
			ServiceAccount: req.ServiceAccount,
			ExpiresAt:      s.enrollmentExpiry(ctx, req.EnrollmentToken),
		})
		return false, nil

	default: // denied, expired, failed
		s.notifyWaiter(requestID, WaitOutcome{Status: req.Status})
		return false, nil
	}
}

// enrollmentExpiry reads when the code minted for an approved enrollment
// stops being redeemable, for the reconnect path where the in-memory
// outcome is gone but the row is not. It is display detail on an outcome
// that is already decided, so a miss or a read failure returns the zero
// time and the caller simply omits the field — the client still gets its
// code.
func (s *CertRequestService) enrollmentExpiry(ctx context.Context, code string) time.Time {
	if code == "" {
		return time.Time{}
	}
	var enrollment model.Enrollment
	if err := s.db.WithContext(ctx).First(&enrollment, "code = ?", code).Error; err != nil {
		slog.Warn("failed to read enrollment expiry for a resolved request", "error", err)
		return time.Time{}
	}
	return enrollment.ExpiresAt
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

	s.notifyWaiter(requestID, WaitOutcome{Status: model.CertificateRequestStatusExpired})
}

// tryHandleWakeMessage attempts to decode and use a wake message payload,
// subject to database verification of the approval decision. It decodes the
// message, verifies the status against the database, and only if both match
// and represent a terminal decision, returns the certificate from the message
// (optimization) without another DB round-trip. This approach keeps the
// database as the authority for the critical approval decision while using
// the wake message as an optimization for the certificate bytes.
//
// Authorization: gochannel (in-process) is in a trusted boundary. NATS
// requires mTLS peer authentication (documented as an operational
// prerequisite in docs/dev/multi-instance-safety-plan.md); this method
// documents but does not enforce that requirement, as NATS configuration
// is outside ssoosshd's scope.
//
// Returns the resolved outcome and true if the message carried a terminal
// status and was verified by the database; otherwise returns false to
// trigger a fresh DB read in the caller's next loop iteration.
func (s *CertRequestService) tryHandleWakeMessage(ctx context.Context, requestID string, msg *message.Message) (outcome WaitOutcome, handled bool) {
	var msgOutcome requestOutcomeMessage
	if err := json.Unmarshal(msg.Payload, &msgOutcome); err != nil {
		// Malformed payload — fall back to DB.
		return WaitOutcome{}, false
	}

	// Only trust terminal statuses from the message. Signing is not
	// terminal — the signer hasn't finished yet, and the message is just
	// a race signal.
	switch msgOutcome.Status {
	case model.CertificateRequestStatusApproved,
		model.CertificateRequestStatusEnrolled,
		model.CertificateRequestStatusDenied,
		model.CertificateRequestStatusExpired,
		model.CertificateRequestStatusFailed:
		// Verify the status in the database before trusting the message.
		// This ensures that only legitimately approved requests result in
		// certificate delivery. gochannel is trusted (in-process); NATS
		// relies on mTLS peer auth (documented prerequisite).
		req, err := s.lookupRequest(ctx, requestID)
		if err != nil {
			// DB lookup failed or request not found — fall back to
			// reconcileStatus to handle it through the normal path.
			return WaitOutcome{}, false
		}

		// The resolving instance publishes this wake *before* it writes
		// the status (SignedReplyHandler.resolveSuccess documents why), so
		// the row can still read Signing when the message lands. In one
		// process that is invisible: notifyWaiter caches the outcome and
		// Wait returns from the cache before ever reaching here. Across
		// instances there is no shared cache, so it is the ordinary case
		// rather than an anomaly, and discarding the message would throw
		// away the only copy of the certificate — leaving the client
		// blocked until its request expires. Give the row a bounded moment
		// to catch up instead. The database still has to confirm the
		// decision; it is only allowed to be slightly behind.
		if req.Status != msgOutcome.Status && req.Status == model.CertificateRequestStatusSigning {
			req = s.awaitStatus(ctx, requestID, msgOutcome.Status, req)
		}

		// Verify the DB status matches the message status. Accept the
		// certificate from the message only if the DB confirms the decision.
		// This is a cheap check — the common case succeeds immediately.
		if req.Status == msgOutcome.Status {
			// Status verified. Cache the outcome and return it directly,
			// completing the wait without another DB round-trip. This
			// optimizes both same-instance (typical) and cross-instance
			// (multi-instance) cases.
			resolved := WaitOutcome{
				Status:         msgOutcome.Status,
				Certificate:    msgOutcome.Certificate,
				Code:           msgOutcome.Code,
				ServiceAccount: msgOutcome.ServiceAccount,
				resolvedAt:     time.Now(),
			}
			if msgOutcome.ExpiresAt != nil {
				resolved.ExpiresAt = *msgOutcome.ExpiresAt
			}
			s.mu.Lock()
			s.resolved[requestID] = resolved
			s.mu.Unlock()
			return resolved, true
		}

		// Status mismatch — the message status doesn't match the DB status.
		// This shouldn't happen in normal operation, but could indicate a
		// stale message or concurrent state change. Fall back to the DB.
		return WaitOutcome{}, false

	default:
		// Non-terminal status (only Signing) — fall through and re-read
		// the DB. The message is just a signal that something may have
		// changed; the DB is the authority.
		return WaitOutcome{}, false
	}
}

// statusConfirmWindow bounds how long a wake message waits for the database
// row to catch up with it, and statusConfirmPoll how often it re-reads.
// Both are sized for a single UPDATE landing on the resolving instance, not
// for a stalled signer: a row that has not caught up by then is treated as
// a genuine mismatch.
const (
	statusConfirmWindow = 2 * time.Second
	statusConfirmPoll   = 25 * time.Millisecond
)

// awaitStatus re-reads requestID until its status reaches want or the
// confirmation window elapses, returning the freshest row it managed to
// read. Used only to let a wake message wait out the gap between delivery
// and the status write; a row that never reaches want is returned as it
// stands and rejected by the caller, so this can only ever delay a
// rejection, never turn one into an acceptance the database disagrees with.
func (s *CertRequestService) awaitStatus(ctx context.Context, requestID string, want model.CertificateRequestStatus, current model.CertificateRequest) model.CertificateRequest {
	deadline := time.Now().Add(statusConfirmWindow)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return current
		case <-time.After(statusConfirmPoll):
		}

		req, err := s.lookupRequest(ctx, requestID)
		if err != nil {
			// A read failure here is not fatal: the caller rejects the
			// message and Wait's next iteration re-reads the database
			// anyway.
			return current
		}
		current = req
		if current.Status == want {
			return current
		}
	}
	return current
}

// notifyWaiter caches outcome for requestID (so any Wait call arriving
// after this point, including a late reconnect, reads it directly) and
// publishes it to the request's wake topic (see certmsg.WaitTopic) so anything
// currently blocked in Wait — in this process or, once a real shared
// broker is configured (docs/dev/signer-split-deferred.md), another
// one — wakes up. A publish failure here is logged but not fatal to the
// caller (Deny/expire's own DB update already succeeded, which is the
// durable fact) — a blocked Wait call will still catch up on its own via
// the DB-status check the next time anything nudges it (reconnect, or a
// future poll), same as if the process restarted between the DB write and
// this publish.
func (s *CertRequestService) notifyWaiter(requestID string, outcome WaitOutcome) {
	outcome.resolvedAt = time.Now()

	s.mu.Lock()
	s.resolved[requestID] = outcome
	s.mu.Unlock()

	msgOutcome := requestOutcomeMessage{
		Status:         outcome.Status,
		Certificate:    outcome.Certificate,
		Code:           outcome.Code,
		ServiceAccount: outcome.ServiceAccount,
	}
	// A pointer so an unset expiry is absent from the message rather than
	// arriving as the zero time, which a consumer would have to know to
	// disbelieve.
	if !outcome.ExpiresAt.IsZero() {
		expiresAt := outcome.ExpiresAt
		msgOutcome.ExpiresAt = &expiresAt
	}

	payload, err := json.Marshal(msgOutcome)
	if err != nil {
		// not covered: requestOutcomeMessage is a plain struct, so
		// json.Marshal cannot fail on it.
		slog.Error("failed to encode certificate request outcome", "request_id", requestID, "error", err)
		return
	}

	if err := s.publisher.Publish(certmsg.WaitTopic(requestID), message.NewMessage(watermill.NewUUID(), payload)); err != nil {
		slog.Error("failed to publish certificate request outcome", "request_id", requestID, "error", err)
	}
}
