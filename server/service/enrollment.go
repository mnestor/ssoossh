package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/serial"
	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
	"github.com/mnestor/ssoossh/server/utils/paging"
)

// EnrollmentProvider redeems an enrollment code into a signed certificate
// and serves the per-enrollment retrieval log. EnrollmentService is the
// production implementation.
type EnrollmentProvider interface {
	Retrieve(ctx context.Context, code string, sourceIP string) (certificate string, err error)
	ListRetrievals(ctx context.Context, requestID string, identity *Identity) (RetrievalLog, error)
	ListForIdentity(ctx context.Context, identity *Identity) ([]ServiceEnrollment, error)
	ListForAdmin(ctx context.Context, identity *Identity, params AdminListParams) (AdminEnrollmentList, error)
	GetEnrollmentDetail(ctx context.Context, enrollmentID string, identity *Identity) (AdminEnrollmentDetail, error)
	Reassign(ctx context.Context, enrollmentID string, toUserID string, reason string, identity *Identity) error
}

// EnrollmentService redeems an approved model.Enrollment (created by
// CertRequestService.Approve for a CertificateTypeService request) into a
// signed certificate. `service retrieve` posts only the enrollment code —
// never a public key — so a stolen code can't be paired with an attacker's
// keypair (see docs/internals/design-brief.md, "Service enrollment").
//
// Codes are reusable until the enrollment expires: unattended jobs retry
// safely, and every redemption issues a fresh certificate carrying the
// lifetime fixed at approval, measured from that redemption. The two spans
// are configured separately (cert_options.service.enrollment_duration and
// .valid_duration), so a code long enough to live in a crontab does not
// imply a certificate that lives that long. Each redemption is logged as a
// model.EnrollmentRetrieval for the approving user and auditors to read
// back.
type EnrollmentService struct {
	config     *config.Config
	db         *gorm.DB
	publisher  message.Publisher
	subscriber message.Subscriber

	// notifier reports each redemption to the approving user. Never nil —
	// see CertRequestService.SetNotifier.
	notifier Notifier

	// auditor records the enrollment.* events. Nil until SetAuditor runs.
	auditor *AuditService
}

// NewEnrollmentService constructs an EnrollmentService signing through the
// pipeline behind publisher/subscriber.
func NewEnrollmentService(c *config.Config, db *gorm.DB, publisher message.Publisher, subscriber message.Subscriber) (*EnrollmentService, error) {
	return &EnrollmentService{
		config:     c,
		db:         db,
		publisher:  publisher,
		subscriber: subscriber,
		notifier:   discardNotifications{},
	}, nil
}

// SetNotifier attaches the notification publisher. See
// CertRequestService.SetNotifier.
func (s *EnrollmentService) SetNotifier(n Notifier) {
	if n != nil {
		s.notifier = n
	}
}

// SetAuditor attaches the audit recorder. See
// CertRequestService.SetAuditor.
func (s *EnrollmentService) SetAuditor(a *AuditService) { s.auditor = a }

// auditTx appends event inside tx, so a reassignment and its audit row
// commit together or not at all. A nil auditor is a no-op.
func (s *EnrollmentService) auditTx(tx *gorm.DB, event AuditEvent) error {
	if s.auditor == nil {
		return nil
	}
	return s.auditor.RecordTx(tx, event)
}

// auditLog emits the archive line for an event already written by auditTx.
func (s *EnrollmentService) auditLog(event AuditEvent) {
	if s.auditor == nil {
		return
	}
	s.auditor.LogOnly(event)
}

// auditRecord writes an event with no transaction to ride along with.
func (s *EnrollmentService) auditRecord(ctx context.Context, event AuditEvent) {
	if s.auditor == nil {
		return
	}
	s.auditor.Record(ctx, event)
}

// Retrieve signs and returns a service certificate for the enrollment
// identified by code, using the public key, key ID, principals, and option
// set stored at approval time — never re-deriving policy
// (evaluate-at-enrollment-time; see docs/operations/certificate-lifetime-policy.md).
//
// The certificate is valid from now for the duration fixed at approval, so
// every redemption yields a full-length certificate — including the last one
// before the code expires. The code outliving the certificate is the point:
// that is what lets a cron job keep renewing without a human re-enrolling it.
//
// Signing goes through the same queue as every other certificate: a
// service-type certmsg.SigningJob is published with a fresh per-retrieval
// ID, and SignedReplyHandler wakes this method on the retrieval's own wake
// topic once the audit row is durable. The signer stays the only component
// that touches CA key material.
func (s *EnrollmentService) Retrieve(ctx context.Context, code string, sourceIP string) (certificate string, err error) {
	var enrollment model.Enrollment
	if err := s.db.WithContext(ctx).First(&enrollment, "code = ?", code).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", &errorresponses.NotFoundError{Resource: "enrollment"}
		}
		return "", fmt.Errorf("failed to look up enrollment: %w", err)
	}

	now := time.Now()
	if !enrollment.ExpiresAt.After(now) {
		// An expired code answers exactly like an unknown one: the caller
		// holds a dead capability either way, and the distinction is
		// visible to the approver in the web UI, not to the wire.
		return "", &errorresponses.NotFoundError{Resource: "enrollment"}
	}

	var principals []string
	if err := json.Unmarshal([]byte(enrollment.Principals), &principals); err != nil || len(principals) == 0 {
		// Enrollments created before principals/key ID were fixed at
		// approval time can't be signed faithfully; the operator re-enrolls.
		return "", fmt.Errorf("enrollment %q has no stored principals; re-enroll the service", enrollment.ID)
	}

	var opts RequestedOptions
	if err := json.Unmarshal([]byte(enrollment.OptionSet), &opts); err != nil {
		return "", fmt.Errorf("failed to decode enrollment option set: %w", err)
	}

	// Rows written before enrollment and certificate lifetimes were split
	// carry no duration, and their ExpiresAt is the certificate bound the
	// approver actually agreed to. Honor it rather than guessing a length
	// for a grant made under the old rules.
	//
	// A stored zero is not that case and must not be read as one: a
	// pin-only lifetime policy computes zero (see
	// docs/operations/certificate-lifetime-policy.md), and inheriting the code's
	// window there would turn the one span that must never become a
	// certificate's into exactly that. Passed through as-is, the signer
	// rejects the zero-length span and the redemption fails closed.
	validBefore := enrollment.ExpiresAt
	if enrollment.CertificateDurationSeconds != nil {
		validBefore = now.Add(time.Duration(*enrollment.CertificateDurationSeconds) * time.Second)
	}

	serialNum, err := serial.New()
	if err != nil {
		return "", fmt.Errorf("failed to allocate certificate serial: %w", err)
	}

	// The retrieval row is written before the job is queued so
	// SignedReplyHandler can already resolve the enrollment linkage for the
	// audit row when the reply lands. Succeeded stays false until the
	// certificate is actually delivered below.
	retrieval := model.EnrollmentRetrieval{
		ID:                uuid.NewString(),
		EnrollmentID:      enrollment.ID,
		SourceIP:          sourceIP,
		CertificateSerial: serialNum,
		RetrievedAt:       now,
	}
	if err := s.db.WithContext(ctx).Create(&retrieval).Error; err != nil {
		return "", fmt.Errorf("failed to record enrollment retrieval: %w", err)
	}

	// Subscribe before publishing, same as CertRequestService.Wait: the
	// pub/sub is Persistent, so a reply landing between Publish and the
	// select below is replayed, never missed. The topic is per-retrieval,
	// so no earlier redemption's outcome can be replayed into this one.
	messages, err := s.subscriber.Subscribe(ctx, certmsg.WaitTopic(retrieval.ID))
	if err != nil {
		return "", fmt.Errorf("failed to subscribe to retrieval outcome: %w", err)
	}

	job := certmsg.SigningJob{
		RequestID:        retrieval.ID,
		Type:             model.CertificateTypeService,
		PublicKey:        enrollment.PublicKey,
		Principals:       principals,
		KeyID:            enrollment.KeyID,
		RequestedOptions: opts,
		ValidAfter:       now,
		ValidBefore:      validBefore,
		Serial:           serialNum,
	}
	payload, err := json.Marshal(job)
	if err != nil {
		// not covered: certmsg.SigningJob is a plain struct, so
		// json.Marshal cannot fail on it.
		return "", fmt.Errorf("failed to encode signing job: %w", err)
	}
	if err := s.publisher.Publish(certmsg.SignQueueTopic, message.NewMessage(watermill.NewUUID(), payload)); err != nil {
		return "", fmt.Errorf("failed to publish signing job: %w", err)
	}

	// Whether the signer answers or not, this redemption is worth
	// reporting: a code that validated and then failed to produce a
	// certificate is exactly the case an operator wants to hear about, and
	// staying quiet about it would make this a success log rather than an
	// alarm.
	//
	// firstRedemption is read from the row loaded before this attempt, so
	// it describes the state the code was in when it was presented rather
	// than after markRetrievalSucceeded has stamped it.
	firstRedemption := enrollment.RedeemedAt == nil

	cert, err := s.awaitSignedCertificate(ctx, messages, serialNum)
	if err != nil {
		s.notifyRedemption(ctx, enrollment, retrieval, principals, validBefore, firstRedemption, false)
		return "", err
	}

	s.markRetrievalSucceeded(ctx, enrollment.ID, retrieval.ID, now)

	s.auditRedemption(ctx, enrollment, serialNum, sourceIP, now)

	s.notifyRedemption(ctx, enrollment, retrieval, principals, validBefore, firstRedemption, true)

	return cert, nil
}

// notifyRedemption queues the redemption notification for the user who
// approved the enrollment.
//
// Queued rather than sent: the caller here is an unattended job waiting on
// its certificate, and a slow or unreachable mail relay must not delay the
// answer it came for. Called after the certificate has been delivered (or
// the failure established) for the same reason.
func (s *EnrollmentService) notifyRedemption(
	ctx context.Context,
	enrollment model.Enrollment,
	retrieval model.EnrollmentRetrieval,
	principals []string,
	certificateExpiresAt time.Time,
	firstRedemption bool,
	succeeded bool,
) {
	requestID := ""
	if enrollment.CertificateRequestID != nil {
		requestID = *enrollment.CertificateRequestID
	}

	// The service account is the enrollment's sole principal, fixed at
	// approval time — see CertRequestService.approveServiceEnrollment.
	serviceAccount := ""
	if len(principals) > 0 {
		serviceAccount = principals[0]
	}

	s.notifier.Notify(ctx, notify.KindServiceEnrollmentRedeemed, enrollment.UserID, &notify.ServiceEnrollmentRedeemed{
		ServiceAccount:       serviceAccount,
		RequestID:            requestID,
		EnrollmentID:         enrollment.ID,
		RetrievalID:          retrieval.ID,
		SourceIP:             retrieval.SourceIP,
		RetrievedAt:          retrieval.RetrievedAt,
		CertificateSerial:    retrieval.CertificateSerial,
		CertificateExpiresAt: certificateExpiresAt,
		KeyID:                enrollment.KeyID,
		Principals:           principals,
		Succeeded:            succeeded,
		FirstRedemption:      firstRedemption,
		CodeExpiresAt:        enrollment.ExpiresAt,
		ServerURL:            s.config.HTTP.PublicOrigin(),
	})
}

// awaitSignedCertificate blocks until the retrieval's wake topic carries a
// terminal outcome, the signing timeout elapses, or ctx is canceled.
//
// The wake message's certificate is trusted only after the database
// confirms the audit row for our pre-allocated serial exists — the same
// DB-is-the-authority rule CertRequestService.tryHandleWakeMessage applies,
// and SignedReplyHandler writes that row before it publishes the wake.
func (s *EnrollmentService) awaitSignedCertificate(ctx context.Context, messages <-chan *message.Message, serialNum uint64) (string, error) {
	var timeoutC <-chan time.Time
	if timeout := s.config.CertOptions.SigningGrace(); timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		timeoutC = timer.C
	}

	for {
		select {
		case msg, ok := <-messages:
			if !ok {
				return "", fmt.Errorf("retrieval outcome subscription closed before signing finished")
			}
			msg.Ack()

			var outcome requestOutcomeMessage
			if err := json.Unmarshal(msg.Payload, &outcome); err != nil {
				continue
			}

			switch outcome.Status {
			case model.CertificateRequestStatusApproved:
				var audit model.Certificate
				if err := s.db.WithContext(ctx).First(&audit, "serial_number = ?", serialNum).Error; err != nil {
					// The wake message claims success the database doesn't
					// back yet — don't hand out an unverified certificate;
					// keep waiting for a confirmable outcome.
					slog.Warn("retrieval wake message not yet backed by an audit row",
						"serial", serialNum, "error", err)
					continue
				}
				return outcome.Certificate, nil
			case model.CertificateRequestStatusFailed:
				return "", fmt.Errorf("certificate signing failed; see server logs")
			default:
				continue
			}

		case <-ctx.Done():
			return "", ctx.Err()

		case <-timeoutC:
			return "", fmt.Errorf("timed out waiting for certificate signing")
		}
	}
}

// ServiceEnrollment is one enrollment as the web UI reads it: the row, the
// two JSON columns already decoded, the bound key's fingerprint, and a
// summary of its retrieval log.
//
// Enrollment.Code is carried because this is the database row, but nothing
// converting a ServiceEnrollment to a response may put it on the wire — see
// webtypes.ServiceEnrollmentResponse.
type ServiceEnrollment struct {
	Enrollment model.Enrollment

	// Principals and Options are Enrollment's JSON columns, decoded. Both
	// are left empty when the stored JSON does not parse: a listing exists
	// to show what was approved, and one unreadable column is not a reason
	// to withhold the created/expires dates beside it.
	Principals []string
	Options    RequestedOptions

	// Fingerprint is the SHA256 fingerprint of Enrollment.PublicKey,
	// computed on read rather than stored — the row keeps the
	// authorized_keys text, and only the display wants the short form.
	// Empty when the key does not parse.
	Fingerprint string

	// RetrievalCount is every logged redemption attempt, successes and
	// signing failures alike. LastRetrievedAt is the most recent of them,
	// nil for a code that has never been redeemed.
	RetrievalCount  int
	LastRetrievedAt *time.Time
}

// ListForIdentity returns the enrollments identity approved, newest first.
//
// Scoped by the users row behind the OIDC subject, the same rule as
// CertificateService.ListForIdentity and for the same reason: an enrollment
// is found by its owner, never by naming a service account or a code, so
// there is no parameter here with which to ask for someone else's. An
// identity with no users row owns no enrollments, which is an empty list
// rather than an error.
//
// Expired enrollments are included. A code that has stopped working is
// exactly what the approver needs to see to decide whether the job behind
// it still needs one.
func (s *EnrollmentService) ListForIdentity(ctx context.Context, identity *Identity) ([]ServiceEnrollment, error) {
	var user model.User
	if err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []ServiceEnrollment{}, nil
		}
		return nil, fmt.Errorf("failed to look up the requesting user: %w", err)
	}

	var rows []model.Enrollment
	if err := s.db.WithContext(ctx).
		Where("user_id = ?", user.ID).
		Order("created_at DESC").
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to list enrollments: %w", err)
	}
	if len(rows) == 0 {
		return []ServiceEnrollment{}, nil
	}

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}

	counts, err := s.retrievalCounts(ctx, ids)
	if err != nil {
		return nil, err
	}
	latest, err := s.latestRetrievals(ctx, ids)
	if err != nil {
		return nil, err
	}

	out := make([]ServiceEnrollment, 0, len(rows))
	for _, row := range rows {
		enrollment := ServiceEnrollment{
			Enrollment:     row,
			Principals:     decodeEnrollmentPrincipals(row),
			Options:        decodeEnrollmentOptions(row),
			Fingerprint:    enrollmentFingerprint(row),
			RetrievalCount: counts[row.ID],
		}
		if at, ok := latest[row.ID]; ok {
			retrievedAt := at
			enrollment.LastRetrievedAt = &retrievedAt
		}
		out = append(out, enrollment)
	}
	return out, nil
}

// retrievalCounts counts the logged redemptions of each enrollment in
// enrollmentIDs, in one grouped query rather than one query per row.
// Enrollments with no retrievals are simply absent from the map, which
// reads back as the zero count.
func (s *EnrollmentService) retrievalCounts(ctx context.Context, enrollmentIDs []string) (map[string]int, error) {
	var rows []struct {
		EnrollmentID string
		Total        int
	}
	if err := s.db.WithContext(ctx).
		Model(&model.EnrollmentRetrieval{}).
		Select("enrollment_id, COUNT(*) AS total").
		Where("enrollment_id IN ?", enrollmentIDs).
		Group("enrollment_id").
		Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to count enrollment retrievals: %w", err)
	}

	counts := make(map[string]int, len(rows))
	for _, row := range rows {
		counts[row.EnrollmentID] = row.Total
	}
	return counts, nil
}

// latestRetrievals returns the newest retrieval timestamp for each
// enrollment in enrollmentIDs.
//
// Whole rows are selected rather than a MAX(retrieved_at) aggregate so that
// retrieved_at arrives as its declared column type on both supported
// drivers: an aggregate expression has no declared type, and sqlite hands
// one back as text that will not scan into a time.Time. The correlated
// subquery does the same work at a size bounded by the caller's own
// enrollments.
func (s *EnrollmentService) latestRetrievals(ctx context.Context, enrollmentIDs []string) (map[string]time.Time, error) {
	var rows []model.EnrollmentRetrieval
	if err := s.db.WithContext(ctx).
		Where("enrollment_id IN ?", enrollmentIDs).
		Where(`retrieved_at = (
			SELECT MAX(inner_retrievals.retrieved_at) FROM enrollment_retrievals AS inner_retrievals
			WHERE inner_retrievals.enrollment_id = enrollment_retrievals.enrollment_id
		)`).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to find the latest enrollment retrievals: %w", err)
	}

	// Two redemptions sharing the newest timestamp both come back; they
	// carry the same instant, so which one wins the map slot is immaterial.
	latest := make(map[string]time.Time, len(rows))
	for _, row := range rows {
		latest[row.EnrollmentID] = row.RetrievedAt
	}
	return latest, nil
}

// decodeEnrollmentPrincipals decodes the enrollment's principals column.
// A row written before principals were fixed at approval time has none, and
// one that will not parse is logged rather than returned as an error: this
// is a read for display, and the rest of the row is still worth showing.
func decodeEnrollmentPrincipals(row model.Enrollment) []string {
	if row.Principals == "" {
		return nil
	}
	var principals []string
	if err := json.Unmarshal([]byte(row.Principals), &principals); err != nil {
		slog.Error("failed to decode enrollment principals", "enrollment_id", row.ID, "error", err)
		return nil
	}
	return principals
}

// decodeEnrollmentOptions decodes the enrollment's option set, on the same
// terms as decodeEnrollmentPrincipals.
func decodeEnrollmentOptions(row model.Enrollment) RequestedOptions {
	if row.OptionSet == "" {
		return RequestedOptions{}
	}
	var opts RequestedOptions
	if err := json.Unmarshal([]byte(row.OptionSet), &opts); err != nil {
		slog.Error("failed to decode enrollment option set", "enrollment_id", row.ID, "error", err)
		return RequestedOptions{}
	}
	return opts
}

// enrollmentFingerprint renders the bound public key's SHA256 fingerprint,
// or empty if it does not parse. Only the display wants it, so an
// unparseable key costs the fingerprint line and nothing else.
func enrollmentFingerprint(row model.Enrollment) string {
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(row.PublicKey)) //nolint:dogsled // ParseAuthorizedKey's comment/options/rest returns say nothing about a fingerprint.
	if err != nil {
		slog.Error("failed to parse enrollment public key", "enrollment_id", row.ID, "error", err)
		return ""
	}
	return ssh.FingerprintSHA256(publicKey)
}

// RetrievalLog is one enrollment's redemption history: the newest page of
// it, and how many rows exist in total. The two differ whenever the code
// has been redeemed more than RetrievalPageSize times.
type RetrievalLog struct {
	Retrievals []model.EnrollmentRetrieval
	Total      int
}

// AdminListParams are the query parameters for listing enrollments as an admin.
type AdminListParams struct {
	Limit  int
	Offset int
	Query  string
}

// AdminEnrollmentList is a paged view of all enrollments across users,
// visible to auditors.
type AdminEnrollmentList struct {
	Enrollments []AdminEnrollmentRow
	Total       int64
}

// AdminEnrollmentRow is one enrollment in the admin list, with approver info
// and retrieval summary.
type AdminEnrollmentRow struct {
	Enrollment model.Enrollment
	Approver   model.User
	Principals []string
	Options    RequestedOptions

	Fingerprint     string
	RetrievalCount  int
	LastRetrievedAt *time.Time
}

// AdminEnrollmentDetail is one enrollment in full, with the full retrieval log.
type AdminEnrollmentDetail struct {
	Enrollment    model.Enrollment
	Approver      model.User
	Principals    []string
	Options       RequestedOptions
	Fingerprint   string
	Retrievals    RetrievalLog
	Reassignments []model.EnrollmentReassignment
}

// RetrievalPageSize bounds how many redemptions ListRetrievals returns.
//
// The log is unbounded in the database and can be very large: codes are
// reusable for cert_options.service.enrollment_duration (a year by
// default), so an hourly renewal leaves ~8,760 rows and a five-minute one
// leaves ~105,000. Reading a whole year of them to render a panel is not
// something to do by accident, and the recent end is the part anyone opens
// it for — "is this thing still running, and from where".
const RetrievalPageSize = 100

// ListRetrievals returns the retrieval log for the enrollment created from
// certificate request requestID, newest first, capped at
// RetrievalPageSize rows. The total is reported alongside so a caller can
// say what it is showing a slice of.
//
// Authorization: the enrollment's approving user, or an identity with
// auditor-level access (config.AdminConfig.GrantsAuditor). Checked here
// rather than in a route middleware because the approver rule depends on
// the row being read. Fails closed with Forbidden for anyone else.
func (s *EnrollmentService) ListRetrievals(ctx context.Context, requestID string, identity *Identity) (RetrievalLog, error) {
	var enrollment model.Enrollment
	if err := s.db.WithContext(ctx).First(&enrollment, "certificate_request_id = ?", requestID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return RetrievalLog{}, &errorresponses.NotFoundError{Resource: fmt.Sprintf("enrollment for request %q", requestID)}
		}
		return RetrievalLog{}, fmt.Errorf("failed to look up enrollment: %w", err)
	}

	if !s.config.Admin.GrantsAuditor(identity.Groups) {
		var user model.User
		err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error
		if err != nil || user.ID != enrollment.UserID {
			return RetrievalLog{}, &errorresponses.ForbiddenError{Reason: "retrieval log belongs to another user"}
		}
	}

	// Counted separately rather than inferred from the page: a full page
	// says only "at least this many", and the difference between the two is
	// exactly what the caller renders.
	var total int64
	if err := s.db.WithContext(ctx).
		Model(&model.EnrollmentRetrieval{}).
		Where("enrollment_id = ?", enrollment.ID).
		Count(&total).Error; err != nil {
		return RetrievalLog{}, fmt.Errorf("failed to count enrollment retrievals: %w", err)
	}

	var retrievals []model.EnrollmentRetrieval
	if err := s.db.WithContext(ctx).
		Where("enrollment_id = ?", enrollment.ID).
		Order("retrieved_at DESC").
		Limit(RetrievalPageSize).
		Find(&retrievals).Error; err != nil {
		return RetrievalLog{}, fmt.Errorf("failed to list enrollment retrievals: %w", err)
	}
	return RetrievalLog{Retrievals: retrievals, Total: int(total)}, nil
}

// markRetrievalSucceeded records delivery on the retrieval row and stamps
// the enrollment's first redemption. Both are audit detail: failing to
// write them must not fail a retrieval whose certificate is already signed,
// audited, and in the caller's hands, so errors are logged rather than
// returned.
func (s *EnrollmentService) markRetrievalSucceeded(ctx context.Context, enrollmentID, retrievalID string, now time.Time) {
	if err := s.db.WithContext(ctx).Model(&model.EnrollmentRetrieval{}).
		Where("id = ?", retrievalID).
		Update("succeeded", true).Error; err != nil {
		slog.Error("failed to mark enrollment retrieval succeeded",
			"retrieval_id", retrievalID, "error", err)
	}

	// First redemption only: RedeemedAt is audit detail, not a single-use
	// gate — codes stay redeemable until the enrollment expires.
	if err := s.db.WithContext(ctx).Model(&model.Enrollment{}).
		Where("id = ? AND redeemed_at IS NULL", enrollmentID).
		Update("redeemed_at", now).Error; err != nil {
		slog.Error("failed to stamp enrollment redemption",
			"enrollment_id", enrollmentID, "error", err)
	}
}

// auditRedemption records one redemption. Recorded outside a transaction
// for the same reason markRetrievalSucceeded's writes are: the certificate
// is already signed and in the caller's hands, so nothing here may fail the
// retrieval. The redeeming party is unauthenticated (a code, not a
// session), so the event carries no actor and targets the enrollment's
// owner.
func (s *EnrollmentService) auditRedemption(ctx context.Context, enrollment model.Enrollment, serialNum uint64, sourceIP string, now time.Time) {
	s.auditRecord(ctx, AuditEvent{
		Action:     AuditEnrollmentRedeemed,
		Target:     &AuditSubject{UserID: enrollment.UserID},
		OccurredAt: now,
		Detail: map[string]any{
			"enrollment_id": enrollment.ID,
			"key_id":        enrollment.KeyID,
			"serial":        serialNum,
			"source_ip":     sourceIP,
		},
	})
}

// ListForAdmin returns a paged, searchable list of all enrollments across
// all users, visible to auditors.
//
// Scoped by the config's auditor group (config.Admin.GrantsAuditor).
// Fails closed with Forbidden for non-auditors.
//
// Search matches case-insensitively against:
// - Approving user's username and email.
// - Enrollment principals.
// - Enrollment key ID.
// - Certificate request ID.
func (s *EnrollmentService) ListForAdmin(ctx context.Context, identity *Identity, params AdminListParams) (AdminEnrollmentList, error) {
	if !s.config.Admin.GrantsAuditor(identity.Groups) {
		return AdminEnrollmentList{}, &errorresponses.ForbiddenError{Reason: "auditor access required"}
	}

	// Build the query: select enrollments with their owner's username/email.
	query := s.db.WithContext(ctx).
		Model(&model.Enrollment{}).
		Joins("LEFT JOIN users ON enrollments.user_id = users.id")

	// Apply search filter across username, email, principals, key ID, and certificate_request_id.
	if params.Query != "" {
		whereClause, args := paging.Filter(params.Query,
			"users.username",
			"users.email",
			"enrollments.principals",
			"enrollments.key_id",
			"CAST(COALESCE(enrollments.certificate_request_id, '') AS TEXT)")
		if whereClause != "" {
			query = query.Where(whereClause, args...)
		}
	}

	// Apply deterministic ordering to prevent pagination issues.
	query = query.Order("enrollments.created_at DESC, enrollments.id DESC")

	// Count total matching rows before paging.
	total, err := paging.Count(query)
	if err != nil {
		return AdminEnrollmentList{}, fmt.Errorf("failed to count enrollments: %w", err)
	}

	// Apply pagination window.
	query = paging.Apply(query, paging.Params{Limit: params.Limit, Offset: params.Offset, Query: params.Query})

	// Fetch the results.
	type enrollmentRow struct {
		ID                         string
		Code                       string
		PublicKey                  string
		OptionSet                  string
		KeyID                      string
		Principals                 string
		CertificateRequestID       *string
		UserID                     string
		CreatedAt                  time.Time
		ExpiresAt                  time.Time
		CertificateDurationSeconds *int64
		RedeemedAt                 *time.Time
		UserUsername               *string
		UserEmail                  *string
	}

	var rows []enrollmentRow
	if err := query.
		Select(`enrollments.id, enrollments.code, enrollments.public_key, enrollments.option_set,
			enrollments.key_id, enrollments.principals, enrollments.certificate_request_id,
			enrollments.user_id, enrollments.created_at, enrollments.expires_at,
			enrollments.certificate_duration_seconds, enrollments.redeemed_at,
			users.username AS user_username, users.email AS user_email`).
		Scan(&rows).Error; err != nil {
		return AdminEnrollmentList{}, fmt.Errorf("failed to list enrollments: %w", err)
	}

	if len(rows) == 0 {
		return AdminEnrollmentList{Total: total}, nil
	}

	// Extract enrollment IDs for retrieval counts
	enrollmentIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		enrollmentIDs = append(enrollmentIDs, r.ID)
	}

	// Load retrieval counts and timestamps
	counts, err := s.retrievalCounts(ctx, enrollmentIDs)
	if err != nil {
		return AdminEnrollmentList{}, err
	}
	latest, err := s.latestRetrievals(ctx, enrollmentIDs)
	if err != nil {
		return AdminEnrollmentList{}, err
	}

	// Build response
	enrollments := make([]AdminEnrollmentRow, 0, len(rows))
	for _, r := range rows {
		enrollment := model.Enrollment{
			ID:                         r.ID,
			Code:                       r.Code,
			PublicKey:                  r.PublicKey,
			OptionSet:                  r.OptionSet,
			KeyID:                      r.KeyID,
			Principals:                 r.Principals,
			CertificateRequestID:       r.CertificateRequestID,
			UserID:                     r.UserID,
			CreatedAt:                  r.CreatedAt,
			ExpiresAt:                  r.ExpiresAt,
			CertificateDurationSeconds: r.CertificateDurationSeconds,
			RedeemedAt:                 r.RedeemedAt,
		}

		approver := model.User{
			ID: r.UserID,
		}
		if r.UserUsername != nil {
			approver.Username = *r.UserUsername
		}
		if r.UserEmail != nil {
			approver.Email = *r.UserEmail
		}

		row := AdminEnrollmentRow{
			Enrollment:     enrollment,
			Approver:       approver,
			Principals:     decodeEnrollmentPrincipals(enrollment),
			Options:        decodeEnrollmentOptions(enrollment),
			Fingerprint:    enrollmentFingerprint(enrollment),
			RetrievalCount: counts[r.ID],
		}
		if at, ok := latest[r.ID]; ok {
			retrievedAt := at
			row.LastRetrievedAt = &retrievedAt
		}
		enrollments = append(enrollments, row)
	}

	return AdminEnrollmentList{Enrollments: enrollments, Total: total}, nil
}

// GetEnrollmentDetail returns a single enrollment with full details and
// retrieval log, visible to auditors and the enrollment's approving user.
//
// Scoped by the config's auditor group (config.Admin.GrantsAuditor) or
// ownership (matching the enrollment's user_id). Fails closed with Forbidden
// for anyone else, and with NotFound for an unknown enrollment ID.
func (s *EnrollmentService) GetEnrollmentDetail(ctx context.Context, enrollmentID string, identity *Identity) (AdminEnrollmentDetail, error) {
	var enrollment model.Enrollment
	if err := s.db.WithContext(ctx).First(&enrollment, "id = ?", enrollmentID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminEnrollmentDetail{}, &errorresponses.NotFoundError{Resource: fmt.Sprintf("enrollment %q", enrollmentID)}
		}
		return AdminEnrollmentDetail{}, fmt.Errorf("failed to look up enrollment: %w", err)
	}

	// Check authorization: auditor or owner
	if !s.config.Admin.GrantsAuditor(identity.Groups) {
		var user model.User
		err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error
		if err != nil || user.ID != enrollment.UserID {
			return AdminEnrollmentDetail{}, &errorresponses.ForbiddenError{Reason: "enrollment belongs to another user"}
		}
	}

	// Load approver info
	var approver model.User
	if err := s.db.WithContext(ctx).First(&approver, "id = ?", enrollment.UserID).Error; err != nil {
		return AdminEnrollmentDetail{}, fmt.Errorf("failed to load approver user: %w", err)
	}

	// Load retrieval log
	var retrievals []model.EnrollmentRetrieval
	if err := s.db.WithContext(ctx).
		Where("enrollment_id = ?", enrollmentID).
		Order("retrieved_at DESC").
		Limit(RetrievalPageSize).
		Find(&retrievals).Error; err != nil {
		return AdminEnrollmentDetail{}, fmt.Errorf("failed to load retrievals: %w", err)
	}

	var total int64
	if err := s.db.WithContext(ctx).
		Model(&model.EnrollmentRetrieval{}).
		Where("enrollment_id = ?", enrollmentID).
		Count(&total).Error; err != nil {
		return AdminEnrollmentDetail{}, fmt.Errorf("failed to count retrievals: %w", err)
	}

	// Load reassignment history
	var reassignments []model.EnrollmentReassignment
	if err := s.db.WithContext(ctx).
		Where("enrollment_id = ?", enrollmentID).
		Order("reassigned_at DESC").
		Find(&reassignments).Error; err != nil {
		return AdminEnrollmentDetail{}, fmt.Errorf("failed to load reassignments: %w", err)
	}

	return AdminEnrollmentDetail{
		Enrollment:    enrollment,
		Approver:      approver,
		Principals:    decodeEnrollmentPrincipals(enrollment),
		Options:       decodeEnrollmentOptions(enrollment),
		Fingerprint:   enrollmentFingerprint(enrollment),
		Retrievals:    RetrievalLog{Retrievals: retrievals, Total: int(total)},
		Reassignments: reassignments,
	}, nil
}

// isEligibleForReassignment checks whether a user can be reassigned an
// enrollment: their service_accounts JSON must contain the enrollment's
// principal. An error is returned for unparseable JSON; InvalidRequestError
// for an ineligible user.
func (s *EnrollmentService) isEligibleForReassignment(ctx context.Context, enrollment model.Enrollment, targetUserID string) error {
	// Load the target user
	var targetUser model.User
	if err := s.db.WithContext(ctx).First(&targetUser, "id = ?", targetUserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("target user %q does not exist", targetUserID)}
		}
		return fmt.Errorf("failed to look up target user: %w", err)
	}

	// Extract the enrollment's principal (service enrollments have exactly one).
	principals := decodeEnrollmentPrincipals(enrollment)
	if len(principals) == 0 {
		return fmt.Errorf("enrollment has no principals; cannot determine eligibility")
	}
	principal := principals[0]

	// Parse target user's service_accounts JSON.
	var serviceAccounts []string
	if err := json.Unmarshal([]byte(targetUser.ServiceAccounts), &serviceAccounts); err != nil {
		return fmt.Errorf("failed to parse target user service accounts: %w", err)
	}

	// Check if the principal is in the target's service accounts.
	for _, account := range serviceAccounts {
		if account == principal {
			return nil // Eligible
		}
	}

	return &errorresponses.InvalidRequestError{
		Reason: fmt.Sprintf("target user does not have service account %q", principal),
	}
}

// Reassign changes the ownership of an enrollment to a new user. The
// enrollment's key ID, principals, options, and expiry remain unchanged —
// only the user_id is updated. An audit record is created to track the
// transfer.
//
// Scoped by: owner (the current user_id) or admin group membership.
// Eligible target: a user whose service_accounts contains the enrollment's
// principal.
//
// Returns Forbidden for non-owners and non-admins, InvalidRequest for an
// ineligible target, and NotFound for an unknown enrollment.
// authorizeReassignment checks that identity may reassign enrollment —
// owner or admin — and resolves the reassigner's users row, which the
// records written afterwards are keyed on.
//
// Split out of Reassign because the two questions it answers ("may you"
// and "who are you") share the admin determination, so keeping them
// together avoids resolving membership twice.
func (s *EnrollmentService) authorizeReassignment(ctx context.Context, enrollment model.Enrollment, identity *Identity) (model.User, error) {
	// Admin is determined by RequireGroup membership.
	isAdmin := false
	if s.config.Admin.IsAdminEnabled() {
		isAdmin = slices.Contains(identity.Groups, s.config.Admin.RequireGroup)
	}

	if !isAdmin {
		var user model.User
		err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error
		if err != nil || user.ID != enrollment.UserID {
			return model.User{}, &errorresponses.ForbiddenError{Reason: "you must be the enrollment owner or an admin to reassign it"}
		}
		// Owner reassigning their own enrollment; the row is already in hand.
		return model.User{ID: enrollment.UserID}, nil
	}

	// Admin reassigning; load their user ID from the subject.
	var reassigner model.User
	if err := s.db.WithContext(ctx).First(&reassigner, "subject = ?", identity.Subject).Error; err != nil {
		return model.User{}, fmt.Errorf("failed to load reassigner user: %w", err)
	}
	return reassigner, nil
}

func (s *EnrollmentService) Reassign(ctx context.Context, enrollmentID string, toUserID string, reason string, identity *Identity) error {
	// Validated before anything is read or written: a reassignment with no
	// stated reason is exactly the record that is useless later.
	reason, err := ValidateAuditReason(AuditEnrollmentReassigned, reason)
	if err != nil {
		return &errorresponses.InvalidRequestError{Reason: err.Error()}
	}

	// Load the enrollment
	var enrollment model.Enrollment
	if err := s.db.WithContext(ctx).First(&enrollment, "id = ?", enrollmentID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &errorresponses.NotFoundError{Resource: fmt.Sprintf("enrollment %q", enrollmentID)}
		}
		return fmt.Errorf("failed to look up enrollment: %w", err)
	}

	reassigner, err := s.authorizeReassignment(ctx, enrollment, identity)
	if err != nil {
		return err
	}

	// Check eligibility of the target user.
	if err := s.isEligibleForReassignment(ctx, enrollment, toUserID); err != nil {
		return err
	}

	now := time.Now()

	// The pre-existing special-purpose reassignment table stays as it is —
	// it feeds its own UI surface — and the general audit event is emitted
	// alongside it.
	reassignment := model.EnrollmentReassignment{
		ID:                 uuid.NewString(),
		EnrollmentID:       enrollmentID,
		FromUserID:         enrollment.UserID,
		ToUserID:           toUserID,
		ReassignedByUserID: reassigner.ID,
		ReassignedAt:       now,
	}

	auditEvent := AuditEvent{
		Action:     AuditEnrollmentReassigned,
		Actor:      AuditSubjectFromIdentity(identity, reassigner.ID),
		Target:     &AuditSubject{UserID: toUserID},
		Reason:     reason,
		OccurredAt: now,
		Detail: map[string]any{
			"enrollment_id": enrollmentID,
			"from_user_id":  enrollment.UserID,
			"to_user_id":    toUserID,
			"key_id":        enrollment.KeyID,
		},
	}

	// One transaction for all three writes. The ownership change and its
	// two records were previously separate statements, so a failure between
	// them left an enrollment reassigned with nothing saying by whom.
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&enrollment).Update("user_id", toUserID).Error; err != nil {
			return fmt.Errorf("failed to update enrollment: %w", err)
		}
		if err := tx.Create(&reassignment).Error; err != nil {
			return fmt.Errorf("failed to create reassignment audit record: %w", err)
		}
		return s.auditTx(tx, auditEvent)
	}); err != nil {
		return err
	}

	s.auditLog(auditEvent)
	return nil
}
