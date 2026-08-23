package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/serial"
	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// EnrollmentProvider redeems an enrollment code into a signed certificate
// and serves the per-enrollment retrieval log. EnrollmentService is the
// production implementation.
type EnrollmentProvider interface {
	Retrieve(ctx context.Context, code string, sourceIP string) (certificate string, err error)
	ListRetrievals(ctx context.Context, requestID string, identity *Identity) ([]model.EnrollmentRetrieval, error)
}

// EnrollmentService redeems an approved model.Enrollment (created by
// CertRequestService.Approve for a CertificateTypeService request) into a
// signed certificate. `service retrieve` posts only the enrollment code —
// never a public key — so a stolen code can't be paired with an attacker's
// keypair (see docs/dev/ssoossh-context.md, "Service enrollment").
//
// Codes are reusable until the enrollment expires: unattended jobs retry
// safely, and every redemption issues a fresh certificate bounded by the
// enrollment's approval-time expiry. Each redemption is logged as a
// model.EnrollmentRetrieval for the approving user and auditors to read
// back.
type EnrollmentService struct {
	config     *config.Config
	db         *gorm.DB
	publisher  message.Publisher
	subscriber message.Subscriber
}

// NewEnrollmentService constructs an EnrollmentService signing through the
// pipeline behind publisher/subscriber.
func NewEnrollmentService(c *config.Config, db *gorm.DB, publisher message.Publisher, subscriber message.Subscriber) (*EnrollmentService, error) {
	return &EnrollmentService{config: c, db: db, publisher: publisher, subscriber: subscriber}, nil
}

// Retrieve signs and returns a service certificate for the enrollment
// identified by code, using the public key, key ID, principals, and option
// set stored at approval time — never re-deriving policy
// (evaluate-at-enrollment-time; see docs/certificate-lifetime-policy.md).
//
// The certificate is valid from now until the enrollment's own expiry, so a
// redemption near the end of the enrollment window yields a short
// certificate rather than one outliving what the approver granted.
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
		ValidBefore:      enrollment.ExpiresAt,
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

	cert, err := s.awaitSignedCertificate(ctx, messages, serialNum)
	if err != nil {
		return "", err
	}

	s.markRetrievalSucceeded(ctx, enrollment.ID, retrieval.ID, now)

	return cert, nil
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
	if timeout := s.config.CertOptions.SigningTimeout; timeout > 0 {
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

// ListRetrievals returns the retrieval log for the enrollment created from
// certificate request requestID, newest first.
//
// Authorization: the enrollment's approving user, or an identity with
// auditor-level access (config.AdminConfig.GrantsAuditor). Checked here
// rather than in a route middleware because the approver rule depends on
// the row being read. Fails closed with Forbidden for anyone else.
func (s *EnrollmentService) ListRetrievals(ctx context.Context, requestID string, identity *Identity) ([]model.EnrollmentRetrieval, error) {
	var enrollment model.Enrollment
	if err := s.db.WithContext(ctx).First(&enrollment, "certificate_request_id = ?", requestID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &errorresponses.NotFoundError{Resource: fmt.Sprintf("enrollment for request %q", requestID)}
		}
		return nil, fmt.Errorf("failed to look up enrollment: %w", err)
	}

	if !s.config.Admin.GrantsAuditor(identity.Groups) {
		var user model.User
		err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error
		if err != nil || user.ID != enrollment.UserID {
			return nil, &errorresponses.ForbiddenError{Reason: "retrieval log belongs to another user"}
		}
	}

	var retrievals []model.EnrollmentRetrieval
	if err := s.db.WithContext(ctx).
		Where("enrollment_id = ?", enrollment.ID).
		Order("retrieved_at DESC").
		Find(&retrievals).Error; err != nil {
		return nil, fmt.Errorf("failed to list enrollment retrievals: %w", err)
	}
	return retrievals, nil
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
