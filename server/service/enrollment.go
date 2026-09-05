package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"slices"
	"strings"
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
	SetNotificationEmail(ctx context.Context, enrollmentID string, identity *Identity, address string) error
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
		//
		// The owners are told, though, before that answer goes out. By this
		// point the row is loaded, so the attempt is fully attributable —
		// and it is either a forgotten job now failing on schedule or a
		// credential being replayed, both of which they want to hear about.
		s.notifyExpiredAttempt(ctx, enrollment, sourceIP, now)
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

// notifyRedemption queues the redemption notification for everyone holding
// the enrollment's service account.
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

	s.notifier.NotifyEnrollment(ctx, notify.KindServiceEnrollmentRedeemed, enrollment.ID, enrollment.ServiceAccount, &notify.ServiceEnrollmentRedeemed{
		ServiceAccount:       enrollment.ServiceAccount,
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

// notifyExpiredAttempt reports one presentation of an expired code, at most
// once per enrollment per mail.expired_attempt_window.
//
// The rate limit is a claim in the database rather than a counter in
// memory, for the same reason the expiry reminder's is: every instance
// fields redemptions, and the delivery queue group deduplicates
// consumption, not publication. Two instances answering the same retry loop
// would otherwise queue two events and the group would faithfully send
// both.
//
// Claim-then-publish, so a crash between the two loses a message rather
// than sending an extra one. For a report of an already-failing job that is
// the right side to fail on: the next attempt in the loop re-reports it.
func (s *EnrollmentService) notifyExpiredAttempt(ctx context.Context, enrollment model.Enrollment, sourceIP string, attemptedAt time.Time) {
	window := s.config.Mail.ExpiredAttemptWindow
	if window <= 0 {
		return
	}

	// Matches an enrollment never notified, or one whose last notification
	// is older than the window. The same statement is both the check and
	// the claim, which is what makes it safe across instances.
	result := s.db.WithContext(ctx).Model(&model.Enrollment{}).
		Where("id = ?", enrollment.ID).
		Where("last_expired_attempt_notified_at IS NULL OR last_expired_attempt_notified_at < ?", attemptedAt.Add(-window)).
		Update("last_expired_attempt_notified_at", attemptedAt)
	if result.Error != nil {
		slog.ErrorContext(ctx, "failed to claim the expired-attempt notification",
			"enrollment_id", enrollment.ID, "error", result.Error)
		return
	}
	if result.RowsAffected == 0 {
		// Already reported inside this window, by this instance or another.
		return
	}

	requestID := ""
	if enrollment.CertificateRequestID != nil {
		requestID = *enrollment.CertificateRequestID
	}
	fingerprint, keyType := describeAuthorizedKey(enrollment.PublicKey)

	s.notifier.NotifyEnrollment(ctx, notify.KindServiceEnrollmentExpiredAttempt, enrollment.ID, enrollment.ServiceAccount,
		&notify.ServiceEnrollmentExpiredAttempt{
			ServiceAccount:       enrollment.ServiceAccount,
			RequestID:            requestID,
			EnrollmentID:         enrollment.ID,
			KeyID:                enrollment.KeyID,
			Principals:           decodeEnrollmentPrincipals(enrollment),
			PublicKeyFingerprint: fingerprint,
			PublicKeyType:        keyType,
			SourceIP:             sourceIP,
			AttemptedAt:          attemptedAt,
			CodeExpiredAt:        enrollment.ExpiresAt,
			ServerURL:            s.config.HTTP.PublicOrigin(),
		})
}

// The reminder cadence. An enrollment inside mail.expiry_reminder_lead is
// reminded once a week until it is within ExpiryReminderDailyWindow of
// expiring, then once a day. A 30-day lead therefore produces weekly
// reminders at 30, 23, 16 and 9 days out, then daily ones from 7 days out
// to the last; the default 7-day lead produces the daily ones alone.
//
// Exported because the sweep interval in bootstrap is derived from the
// shortest cadence here, not from the lead: a daily reminder swept every
// seven hours would drift a full day late over the week it runs.
const (
	ExpiryReminderDailyWindow    = 7 * 24 * time.Hour
	ExpiryReminderDailyInterval  = 24 * time.Hour
	ExpiryReminderWeeklyInterval = 7 * 24 * time.Hour
)

// expiryReminderBatch bounds how many reminders one sweep pass claims.
//
// The sweep is a background job with no deadline, and the queue it
// publishes into is in-process, so the bound is not about throughput: it is
// about the first pass after an upgrade, when no enrollment has been
// reminded yet and every one inside the window is eligible at once. The
// remainder is picked up by the next pass, which is minutes away.
const expiryReminderBatch = 500

// SweepExpiryReminders sends the reminders for codes coming up on expiry,
// weekly and then daily (see ExpiryReminderDailyWindow), and is the only
// notification not emitted from an event path — the event here is the
// absence of one.
//
// Registered as a scheduled job (see bootstrap.registerExpiryReminderJob)
// and disabled entirely when mail.expiry_reminder_lead is zero.
//
// Every instance runs this. The once-per-interval guarantee comes from
// claiming each row with a guarded UPDATE on expiry_reminder_sent_at and
// publishing only when that reports a row, so two instances sweeping the
// same enrollment produce one reminder between them rather than one each.
// The guard is "last sent at or before the cadence threshold" rather than
// an equality on the value read, so it needs no timestamp round-trip
// precision from either dialect.
func (s *EnrollmentService) SweepExpiryReminders(ctx context.Context) error {
	// Registration already skips a zero lead, so in production nothing
	// reaches here with one. The guard stays because it belongs with the
	// value it reads rather than only at the registration that happens to be
	// its one caller today.
	lead := s.config.Mail.ExpiryReminderLead
	if lead <= 0 {
		return nil
	}

	now := time.Now()
	dailyFrom := now.Add(ExpiryReminderDailyWindow)
	var due []model.Enrollment
	err := s.db.WithContext(ctx).
		// Already-expired codes are deliberately excluded. A reminder that
		// something expires "in already elapsed" helps nobody, and the
		// aftermath has its own notification: an expired code that anything
		// still presents raises service_enrollment_expired_attempt.
		Where("expires_at > ?", now).
		Where("expires_at <= ?", now.Add(lead)).
		// Due under whichever cadence the code's remaining life puts it in:
		// never reminded, or last reminded at least one interval ago.
		Where(s.db.
			Where("expires_at <= ? AND (expiry_reminder_sent_at IS NULL OR expiry_reminder_sent_at <= ?)",
				dailyFrom, now.Add(-ExpiryReminderDailyInterval)).
			Or("expires_at > ? AND (expiry_reminder_sent_at IS NULL OR expiry_reminder_sent_at <= ?)",
				dailyFrom, now.Add(-ExpiryReminderWeeklyInterval))).
		Order("expires_at").
		Limit(expiryReminderBatch).
		Find(&due).Error
	if err != nil {
		return fmt.Errorf("failed to find enrollments due an expiry reminder: %w", err)
	}

	for _, enrollment := range due {
		daily := !enrollment.ExpiresAt.After(dailyFrom)
		interval := ExpiryReminderWeeklyInterval
		if daily {
			interval = ExpiryReminderDailyInterval
		}

		// Guarded on the column still being at or before the cadence
		// threshold, which is the claim: a second instance that selected
		// the same row finds it already stamped with now, loses this
		// update, and publishes nothing.
		result := s.db.WithContext(ctx).Model(&model.Enrollment{}).
			Where("id = ? AND (expiry_reminder_sent_at IS NULL OR expiry_reminder_sent_at <= ?)",
				enrollment.ID, now.Add(-interval)).
			Update("expiry_reminder_sent_at", now)
		if result.Error != nil {
			return fmt.Errorf("failed to claim the expiry reminder for enrollment %q: %w", enrollment.ID, result.Error)
		}
		if result.RowsAffected == 0 {
			continue
		}

		requestID := ""
		if enrollment.CertificateRequestID != nil {
			requestID = *enrollment.CertificateRequestID
		}
		fingerprint, keyType := describeAuthorizedKey(enrollment.PublicKey)

		firstRedeemedAt := time.Time{}
		if enrollment.RedeemedAt != nil {
			firstRedeemedAt = *enrollment.RedeemedAt
		}

		s.notifier.NotifyEnrollment(ctx, notify.KindServiceEnrollmentExpiring, enrollment.ID, enrollment.ServiceAccount,
			&notify.ServiceEnrollmentExpiring{
				ServiceAccount:       enrollment.ServiceAccount,
				RequestID:            requestID,
				EnrollmentID:         enrollment.ID,
				KeyID:                enrollment.KeyID,
				Principals:           decodeEnrollmentPrincipals(enrollment),
				PublicKeyFingerprint: fingerprint,
				PublicKeyType:        keyType,
				FirstRedeemedAt:      firstRedeemedAt,
				CodeExpiresAt:        enrollment.ExpiresAt,
				Daily:                daily,
				ServerURL:            s.config.HTTP.PublicOrigin(),
			})
	}

	if len(due) > 0 {
		slog.InfoContext(ctx, "sent enrollment expiry reminders", "count", len(due), "lead", lead)
	}
	return nil
}

// maxNotificationEmailLength caps a stored notification address. Well past
// the 254-octet limit RFC 5321 puts on a path, so it rejects only input
// that was never an address.
const maxNotificationEmailLength = 320

// validateNotificationEmail trims and format-checks a notification address,
// returning the value to store. Empty is valid and means "fan out to the
// account's holders".
//
// Shared by the two places an address can be set — approval time and the
// later edit — so the two cannot come to disagree about what is acceptable.
// A rejected address is the caller's input, not a server fault, so it
// renders as a 400.
//
// The address is stored as given otherwise. There is no domain allowlist:
// whoever sets one is already trusted to approve certificates for the
// account, and an operator who wants a restriction can be given a config
// knob later without a migration (see
// docs/proposals/notification-kinds-expansion.md, "Open questions").
func validateNotificationEmail(address string) (string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", nil
	}
	if len(address) > maxNotificationEmailLength {
		return "", &errorresponses.InvalidRequestError{
			Reason: fmt.Sprintf("notification address is %d characters, over the %d limit", len(address), maxNotificationEmailLength),
		}
	}
	if _, err := netmail.ParseAddress(address); err != nil {
		return "", &errorresponses.InvalidRequestError{
			Reason: fmt.Sprintf("%q is not a valid email address", address),
		}
	}
	return address, nil
}

// SetNotificationEmail points every notification about one enrollment at a
// single address, or clears it so they fan out to the account's holders
// again.
//
// Authorized to a holder of the enrollment's service account or to an SOC
// operator. Auditor is deliberately not enough: auditor is a read role, and
// this write silently redirects every future message about a credential.
func (s *EnrollmentService) SetNotificationEmail(ctx context.Context, enrollmentID string, identity *Identity, address string) error {
	address, err := validateNotificationEmail(address)
	if err != nil {
		return err
	}

	var enrollment model.Enrollment
	if err := s.db.WithContext(ctx).First(&enrollment, "id = ?", enrollmentID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &errorresponses.NotFoundError{Resource: fmt.Sprintf("enrollment %q", enrollmentID)}
		}
		return fmt.Errorf("failed to look up enrollment: %w", err)
	}

	if !ownsEnrollment(identity, enrollment) && !s.config.Admin.GrantsSOC(identity.Groups) {
		return &errorresponses.ForbiddenError{Reason: "enrollment belongs to a service account you do not hold"}
	}

	if err := s.db.WithContext(ctx).Model(&model.Enrollment{}).
		Where("id = ?", enrollmentID).
		Update("notification_email", address).Error; err != nil {
		return fmt.Errorf("failed to set the notification address of enrollment %q: %w", enrollmentID, err)
	}

	// Both the old and the new address are recorded: "who stopped this
	// account's holders hearing about their code, and where did it go
	// instead" is the question this event exists to answer, and the new
	// value alone does not answer it.
	// The setter's users-row id is the grouping key that puts this on
	// their own timeline. Best effort: a miss costs the key, not the event.
	var actorUserID string
	if err := s.db.WithContext(ctx).Model(&model.User{}).
		Select("id").Where("subject = ?", identity.Subject).
		Scan(&actorUserID).Error; err != nil {
		slog.Warn("could not resolve the acting user's id for the audit event",
			"enrollment_id", enrollmentID, "error", err)
	}

	s.auditRecord(ctx, AuditEvent{
		Action: AuditEnrollmentNotificationEmailSet,
		Actor:  AuditSubjectFromIdentity(identity, actorUserID),
		Target: &AuditSubject{UserID: enrollment.UserID},
		Detail: map[string]any{
			"enrollment_id":      enrollmentID,
			"service_account":    enrollment.ServiceAccount,
			"previous_email":     enrollment.NotificationEmail,
			"notification_email": address,
		},
	})
	return nil
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

	// ApproverUsername is who approved this enrollment. Shown because a
	// holder now sees codes their colleagues created and needs to know
	// which; empty when that user's row has since gone.
	ApproverUsername string
}

// heldServiceAccounts returns the service accounts identity holds, with
// empties dropped.
//
// The empties matter: a claim that releases a blank entry would otherwise
// match every enrollment whose principals never parsed, which is exactly
// the set that must be owned by nobody.
func heldServiceAccounts(identity *Identity) []string {
	held := make([]string, 0, len(identity.ServiceAccounts))
	for _, account := range identity.ServiceAccounts {
		if account != "" {
			held = append(held, account)
		}
	}
	return held
}

// ownsEnrollment reports whether identity holds enrollment's service
// account, which is the whole of enrollment ownership — there is no stored
// owner and no transfer (see
// docs/proposals/enrollment-group-ownership.md).
//
// Answered from the session identity rather than the users row, the same
// source every other authorization decision in this server reads, so
// access reflects the accounts the provider released at login.
func ownsEnrollment(identity *Identity, enrollment model.Enrollment) bool {
	if enrollment.ServiceAccount == "" {
		return false
	}
	return slices.Contains(identity.ServiceAccounts, enrollment.ServiceAccount)
}

// ListForIdentity returns the enrollments identity owns, newest first.
//
// Scoped by the service accounts on the session: an enrollment is owned by
// every holder of its service account, so this answers "every code for an
// account I hold", not "every code I approved". The list therefore includes
// codes approved by colleagues, and excludes codes the caller approved for
// an account they have since lost.
//
// There is still no parameter here with which to ask for someone else's:
// the account set comes from the session, never from the caller. An
// identity holding no service accounts owns no enrollments, which is an
// empty list rather than an error.
//
// Expired enrollments are included. A code that has stopped working is
// exactly what a holder needs to see to decide whether the job behind it
// still needs one.
func (s *EnrollmentService) ListForIdentity(ctx context.Context, identity *Identity) ([]ServiceEnrollment, error) {
	held := heldServiceAccounts(identity)
	if len(held) == 0 {
		return []ServiceEnrollment{}, nil
	}

	// The approver's username comes along on a LEFT join rather than a
	// second query: the page shows it on every row now that a holder sees
	// codes other people approved, and it is nullable because the join can
	// miss a user row that has since gone.
	type enrollmentRow struct {
		model.Enrollment
		ApproverUsername *string
	}

	var rows []enrollmentRow
	if err := s.db.WithContext(ctx).
		Model(&model.Enrollment{}).
		Joins("LEFT JOIN users ON enrollments.user_id = users.id").
		Select("enrollments.*, users.username AS approver_username").
		Where("enrollments.service_account IN ?", held).
		Order("enrollments.created_at DESC").
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
			Enrollment:     row.Enrollment,
			Principals:     decodeEnrollmentPrincipals(row.Enrollment),
			Options:        decodeEnrollmentOptions(row.Enrollment),
			Fingerprint:    enrollmentFingerprint(row.Enrollment),
			RetrievalCount: counts[row.ID],
		}
		if row.ApproverUsername != nil {
			enrollment.ApproverUsername = *row.ApproverUsername
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
	Enrollment  model.Enrollment
	Approver    model.User
	Principals  []string
	Options     RequestedOptions
	Fingerprint string
	Retrievals  RetrievalLog

	// Reassignments is history and nothing more: ownership can no longer be
	// transferred, so this list only ever shrinks (as audit rows are
	// pruned) and is empty for every enrollment created since. See
	// model.EnrollmentReassignment.
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
// Authorization: a holder of the enrollment's service account, or an
// identity with auditor-level access (config.AdminConfig.GrantsAuditor).
// Checked here rather than in a route middleware because the ownership
// rule depends on the row being read. Fails closed with Forbidden for
// anyone else.
func (s *EnrollmentService) ListRetrievals(ctx context.Context, requestID string, identity *Identity) (RetrievalLog, error) {
	var enrollment model.Enrollment
	if err := s.db.WithContext(ctx).First(&enrollment, "certificate_request_id = ?", requestID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return RetrievalLog{}, &errorresponses.NotFoundError{Resource: fmt.Sprintf("enrollment for request %q", requestID)}
		}
		return RetrievalLog{}, fmt.Errorf("failed to look up enrollment: %w", err)
	}

	if !s.config.Admin.GrantsAuditor(identity.Groups) && !ownsEnrollment(identity, enrollment) {
		return RetrievalLog{}, &errorresponses.ForbiddenError{Reason: "retrieval log belongs to a service account you do not hold"}
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
// session), so the event carries no actor.
//
// It still targets the approving user, which is now provenance rather than
// ownership — the enrollment belongs to a service account held by several
// people. The account itself is in Detail, which is what makes the
// account-centric question ("everything redeemed for svc-backup") one the
// log can answer.
func (s *EnrollmentService) auditRedemption(ctx context.Context, enrollment model.Enrollment, serialNum uint64, sourceIP string, now time.Time) {
	s.auditRecord(ctx, AuditEvent{
		Action:     AuditEnrollmentRedeemed,
		Target:     &AuditSubject{UserID: enrollment.UserID},
		OccurredAt: now,
		Detail: map[string]any{
			"enrollment_id":   enrollment.ID,
			"service_account": enrollment.ServiceAccount,
			"key_id":          enrollment.KeyID,
			"serial":          serialNum,
			"source_ip":       sourceIP,
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

	// Build the query: select enrollments with their approver's
	// username/email. A LEFT join, not an ownership link -- the approver is
	// provenance, and every holder of the service account owns the row.
	query := s.db.WithContext(ctx).
		Model(&model.Enrollment{}).
		Joins("LEFT JOIN users ON enrollments.user_id = users.id")

	// Apply search filter across approver username and email, service
	// account, key ID, and certificate_request_id.
	if params.Query != "" {
		whereClause, args := paging.Filter(params.Query,
			"users.username",
			"users.email",
			"enrollments.service_account",
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
		ServiceAccount             string
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
			enrollments.key_id, enrollments.principals, enrollments.service_account,
			enrollments.certificate_request_id,
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
			ServiceAccount:             r.ServiceAccount,
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
// retrieval log, visible to auditors and to holders of its service account.
//
// Scoped by the config's auditor group (config.Admin.GrantsAuditor) or
// ownership (holding the enrollment's service account). Fails closed with
// Forbidden for anyone else, and with NotFound for an unknown enrollment ID.
func (s *EnrollmentService) GetEnrollmentDetail(ctx context.Context, enrollmentID string, identity *Identity) (AdminEnrollmentDetail, error) {
	var enrollment model.Enrollment
	if err := s.db.WithContext(ctx).First(&enrollment, "id = ?", enrollmentID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return AdminEnrollmentDetail{}, &errorresponses.NotFoundError{Resource: fmt.Sprintf("enrollment %q", enrollmentID)}
		}
		return AdminEnrollmentDetail{}, fmt.Errorf("failed to look up enrollment: %w", err)
	}

	// Check authorization: auditor, or a holder of the service account
	if !s.config.Admin.GrantsAuditor(identity.Groups) && !ownsEnrollment(identity, enrollment) {
		return AdminEnrollmentDetail{}, &errorresponses.ForbiddenError{Reason: "enrollment belongs to a service account you do not hold"}
	}

	// Load approver info. Provenance, not ownership: it says who created
	// this code, and the reader may well not be them.
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
