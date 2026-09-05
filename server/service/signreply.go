package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
)

// SignedReplyHandler consumes certmsg.SignedTopic: it writes the audit row,
// delivers the certificate to the client waiting in Wait, and marks the
// request terminal.
//
// It lives in package service — rather than alongside the signer — because
// it's the mirror image of the signer's constraints: it needs full database
// access, and it needs to reach CertRequestService's delivery internals
// (notifyWaiter and its resolved cache). Keeping it here avoids exporting
// those; keeping it in its own file keeps the boundary legible.
type SignedReplyHandler struct {
	db    *gorm.DB
	certs *CertRequestService
}

// NewSignedReplyHandler constructs a SignedReplyHandler writing to db and
// delivering through certs.
func NewSignedReplyHandler(db *gorm.DB, certs *CertRequestService) *SignedReplyHandler {
	return &SignedReplyHandler{db: db, certs: certs}
}

// Register adds the signed-reply consumer to r.
func (h *SignedReplyHandler) Register(r *message.Router, subscriber message.Subscriber) {
	r.AddConsumerHandler("certrequest-signed-reply", certmsg.SignedTopic, subscriber, h.handle)
}

// handle resolves one signed reply.
//
// Errors returned here nack the message, which the router's retry middleware
// backs off and retries. Once retries are exhausted the message is dropped —
// see resolveSuccess for why that's survivable and what it costs.
func (h *SignedReplyHandler) handle(msg *message.Message) error {
	var reply certmsg.SignedReply
	if err := json.Unmarshal(msg.Payload, &reply); err != nil {
		// Nobody can be told about a reply we can't read, and redelivering
		// won't make it parse. Ack with a log line.
		slog.Error("discarding unparseable signed reply", "error", err)
		return nil
	}

	if reply.Failed() {
		return h.resolveFailure(msg.Context(), reply)
	}
	return h.resolveSuccess(msg.Context(), reply)
}

// resolveSuccess persists the audit row, delivers the certificate, and marks
// the request approved.
//
// The ordering here is load-bearing:
//
//  1. audit row first — it's the only durable record that a certificate was
//     ever issued (the certificate itself is deliberately never stored; see
//     docs/internals/signing-pipeline.md)
//  2. deliver (cache + wake) second
//  3. status update last
//
// Delivering before the status update is what makes Wait's
// "approved with a cold cache" case mean *only* "the process restarted" —
// which is exactly the case it reports as CertificateUnavailableError. Were
// the status written first, there'd be a window where a live server showed
// approved with nothing cached, and Wait would wrongly tell a client its
// certificate was gone.
//
// The cost of this ordering is that a crash between 2 and 3 leaves the row
// in "signing" — which is precisely what the invalidation sweep exists
// to clean up (see docs/internals/signing-pipeline.md), and by then
// the audit row is already durable.
func (h *SignedReplyHandler) resolveSuccess(ctx context.Context, reply certmsg.SignedReply) error {
	origin, err := h.recordCertificate(ctx, reply)
	if err != nil {
		return err
	}

	h.certs.notifyWaiter(reply.RequestID, WaitOutcome{
		Status:      model.CertificateRequestStatusApproved,
		Certificate: reply.Certificate,
	})

	// Emitted where the certificate becomes real rather than at approval, so
	// one site covers both types and no message ever describes a certificate
	// that failed to sign. Service certificates are excluded here: their
	// notification is the redemption one, addressed to the enrollment.
	h.notifyIssued(ctx, reply, origin)

	if !resolvesRequestRow(reply) {
		return nil
	}

	// A DB error here (as opposed to a benign zero-rows race, which
	// markResolved reports as nil) is returned so the handler nacks and the
	// reply is retried: otherwise the row stays in Signing and the
	// invalidation sweep would later mark this certificate's request Failed
	// even though it was successfully issued and audited. The retry is safe
	// because recordCertificate and notifyWaiter above are both idempotent.
	return h.markResolved(ctx, reply.RequestID, model.CertificateRequestStatusApproved, "")
}

// resolveFailure marks the request failed and tells the waiting client, so a
// signing failure surfaces as a terminal answer instead of hanging the
// client until its request expires.
func (h *SignedReplyHandler) resolveFailure(ctx context.Context, reply certmsg.SignedReply) error {
	slog.Error("certificate signing failed",
		"request_id", reply.RequestID,
		"error_code", reply.ErrorCode,
		"error", reply.Error,
	)

	h.certs.notifyWaiter(reply.RequestID, WaitOutcome{
		Status: model.CertificateRequestStatusFailed,
	})

	if !resolvesRequestRow(reply) {
		return nil
	}

	// Returned so a DB error nacks and retries (notifyWaiter is idempotent);
	// a benign zero-rows race comes back as nil. See resolveSuccess.
	return h.markResolved(ctx, reply.RequestID, model.CertificateRequestStatusFailed,
		fmt.Sprintf("%s: %s", reply.ErrorCode, reply.Error))
}

// resolvesRequestRow reports whether reply has a certificate_requests row
// waiting in Signing for it to move to a terminal state.
//
// A service reply does not: it resolves a retrieval instead, its RequestID
// being an enrollment_retrievals row (see EnrollmentService.Retrieve), and
// the original request row already went terminal (Enrolled) back at
// approval. Both the success and failure paths ask this, so the rule lives
// here rather than being restated at each.
func resolvesRequestRow(reply certmsg.SignedReply) bool {
	return reply.Type != model.CertificateTypeService
}

// certificateOrigin is what the request row behind an issued certificate
// says about where it came from: who it is for, which request produced it,
// and the client-reported context that makes a "was this you?" message
// recognizable to the person who was there.
//
// Every field is best effort. A reply whose request row cannot be read
// still produces an audit row and a delivered certificate; it just carries
// no owner, which is why the zero value is usable rather than an error.
type certificateOrigin struct {
	UserID    *string
	RequestID *string

	SourceIP      string
	LocalUsername string
	LocalHostname string
}

// reportedContext returns the client-reported account and machine behind a
// request, from whichever columns its type stores them in. A user request
// records the OS user and host the client ran on in local_username and
// local_hostname; a PAM or console request records the account being
// authenticated and the machine it is on in username and hostname (see
// model.CertificateRequest). The notification wants the same thing from
// each — the name the person who was there will recognize.
func reportedContext(req model.CertificateRequest) (username, hostname string) {
	switch req.Type {
	case model.CertificateTypePAM, model.CertificateTypeConsole:
		return req.Username, req.Hostname
	default:
		return req.LocalUsername, req.LocalHostname
	}
}

// recordCertificate writes the audit row for an issued certificate and
// returns what the request row said about its origin, which the issued
// notification needs and would otherwise re-read.
func (h *SignedReplyHandler) recordCertificate(ctx context.Context, reply certmsg.SignedReply) (certificateOrigin, error) {
	var origin certificateOrigin

	criticalOptions, err := json.Marshal(reply.CriticalOptions)
	if err != nil {
		// not covered: a map[string]string, so json.Marshal cannot fail.
		return origin, fmt.Errorf("failed to encode critical options: %w", err)
	}
	extensions, err := json.Marshal(reply.Extensions)
	if err != nil {
		// not covered: a []string, so json.Marshal cannot fail.
		return origin, fmt.Errorf("failed to encode extensions: %w", err)
	}

	// Read the owner off the request rather than carrying it through the
	// signing job. The signer has no database and no business knowing who a
	// certificate is for; the listener does, and Approve has already bound
	// the request to a users row (see CertRequestService.bindRequester).
	//
	// Best effort: a missing owner must not fail issuance for a certificate
	// that is already signed. It only costs per-user history for that row,
	// which is why it is logged rather than returned.
	// requestID is the audit-chain link back to the approval. It is set only
	// when the lookup below confirms the request row exists, because it is a
	// foreign key: pointing it at a missing row would fail this insert and
	// lose the audit record entirely, which is the very outcome the
	// best-effort handling here exists to avoid.
	var userID, requestID *string
	// req stays in scope past the lookup because the cert.issued event
	// below carries the account and host the request claimed. For a
	// service reply, or a lookup that fails, it is simply empty.
	var req model.CertificateRequest
	if reply.Type == model.CertificateTypeService {
		// A service reply's RequestID is an enrollment_retrievals row, not
		// a certificate request (see EnrollmentService.Retrieve): the audit
		// chain runs retrieval → enrollment → the request approved back
		// when the enrollment was created, and the enrollment carries both
		// the approving user and that request's ID.
		userID, requestID = h.resolveRetrievalOwner(ctx, reply.RequestID)
	} else {
		// The client-reported columns come back with the owner rather than
		// in a second read: the issued notification needs them, and this is
		// the one place the request row is already being loaded. Which
		// columns hold them depends on the type (see reportedContext), so
		// both pairs are read.
		switch err := h.db.WithContext(ctx).
			Select("type", "user_id", "source_ip", "local_username", "local_hostname", "username", "hostname").
			First(&req, "id = ?", reply.RequestID).Error; {
		case err != nil:
			slog.Warn("could not resolve the owner of an issued certificate",
				"request_id", reply.RequestID, "error", err)
		case req.UserID == nil:
			// The request exists but was never bound to a user. Recording the
			// request ID is what keeps this row reattachable later rather than
			// permanently orphaned.
			requestID = &reply.RequestID
			origin.SourceIP = req.SourceIP
			origin.LocalUsername, origin.LocalHostname = reportedContext(req)
			slog.Warn("issued certificate has no owner: its request was never bound to a user",
				"request_id", reply.RequestID)
		default:
			userID = req.UserID
			requestID = &reply.RequestID
			origin.SourceIP = req.SourceIP
			origin.LocalUsername, origin.LocalHostname = reportedContext(req)
		}
	}
	origin.UserID, origin.RequestID = userID, requestID

	cert := model.Certificate{
		ID:                   uuid.NewString(),
		Type:                 reply.Type,
		UserID:               userID,
		CertificateRequestID: requestID,
		PublicKeyFingerprint: reply.PublicKeyFingerprint,
		SerialNumber:         reply.Serial,
		KeyID:                reply.KeyID,
		Principals:           strings.Join(reply.Principals, ","),
		CriticalOptions:      string(criticalOptions),
		Extensions:           string(extensions),
		IssuedAt:             reply.ValidAfter,
		ExpiresAt:            reply.ValidBefore,
	}

	// Idempotent on the serial: serials are pre-allocated and uniquely
	// indexed (model.Certificate.SerialNumber), so a redelivered reply (a
	// nacked handler retried, or NATS at-least-once redelivery) finds the row
	// already present and skips it rather than failing on the unique
	// constraint. This is what makes it safe for resolveSuccess to nack on a
	// later markResolved failure: the retry cannot duplicate the audit row.
	if err := h.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "serial_number"}},
			DoNothing: true,
		}).
		Create(&cert).Error; err != nil {
		return origin, fmt.Errorf("failed to persist certificate audit record: %w", err)
	}

	// Shipped log only, never the table: the UI already has certificate
	// history from the certificates table, so a table copy would be pure
	// duplication. The archive line is the valuable part — it is the row an
	// incident reviewer joins against target-host sshd logs.
	if h.certs != nil {
		h.certs.auditRecord(ctx, AuditEvent{
			Action:     AuditCertIssued,
			Target:     &AuditSubject{UserID: derefOrEmpty(userID)},
			OccurredAt: reply.ValidAfter,
			Detail: map[string]any{
				"request_id":   derefOrEmpty(requestID),
				"cert_type":    string(reply.Type),
				"serial":       reply.Serial,
				"key_id":       reply.KeyID,
				"principals":   reply.Principals,
				"fingerprint":  reply.PublicKeyFingerprint,
				"valid_after":  reply.ValidAfter,
				"valid_before": reply.ValidBefore,
				// The account and host a PAM or console request claimed.
				// This is the line joined against the host's own sshd and
				// sudo logs, and the hostname is the join key.
				"username": req.Username,
				"hostname": req.Hostname,
			},
		})
	}

	return origin, nil
}

// notifyIssued queues the "was this you?" message for a user or PAM
// certificate.
//
// Service certificates are skipped: the enrollment path already reports
// every redemption to the enrollment's recipients, and a second message per
// redemption addressed to the approver would be noise about a job nobody
// was present for. Both kinds here default off, so an existing deployment
// stays as quiet on upgrade as it was before.
//
// A reply with no resolvable owner is skipped rather than logged again —
// recordCertificate has already said so, and there is nobody to tell.
func (h *SignedReplyHandler) notifyIssued(ctx context.Context, reply certmsg.SignedReply, origin certificateOrigin) {
	if h.certs == nil || origin.UserID == nil || *origin.UserID == "" {
		return
	}

	var kind notify.Kind
	switch reply.Type {
	case model.CertificateTypeUser:
		kind = notify.KindUserCertificateIssued
	case model.CertificateTypePAM:
		kind = notify.KindPAMCertificateIssued
	case model.CertificateTypeConsole:
		kind = notify.KindConsoleCertificateIssued
	default:
		// Service, and any type added later without a notification of its
		// own. Silent by design rather than by omission.
		return
	}

	// The critical options come back from the signer as the map that went
	// into the certificate, so they are read from there rather than from
	// the request: what the reader wants confirmed is what the certificate
	// carries, not what was asked for.
	forceCommand := reply.CriticalOptions["force-command"]
	var sourceAddresses []string
	if joined := reply.CriticalOptions["source-address"]; joined != "" {
		sourceAddresses = strings.Split(joined, ",")
	}

	h.certs.notifier.Notify(ctx, kind, *origin.UserID, &notify.CertificateIssued{
		CertificateType:      string(reply.Type),
		RequestID:            reply.RequestID,
		KeyID:                reply.KeyID,
		Principals:           reply.Principals,
		Serial:               reply.Serial,
		PublicKeyFingerprint: reply.PublicKeyFingerprint,
		LocalUsername:        origin.LocalUsername,
		LocalHostname:        origin.LocalHostname,
		SourceIP:             origin.SourceIP,
		IssuedAt:             reply.ValidAfter,
		ExpiresAt:            reply.ValidBefore,
		Extensions:           reply.Extensions,
		ForceCommand:         forceCommand,
		SourceAddresses:      sourceAddresses,
		ServerURL:            h.certs.config.HTTP.PublicOrigin(),
	})
}

// resolveRetrievalOwner resolves the audit linkage for a service
// certificate issued at retrieval time: retrievalID → enrollment → the
// approving user and the originally approved request. Best effort, same as
// the request-owner lookup above: a broken linkage costs history for this
// row, never the audit row itself.
func (h *SignedReplyHandler) resolveRetrievalOwner(ctx context.Context, retrievalID string) (userID, requestID *string) {
	var retrieval model.EnrollmentRetrieval
	if err := h.db.WithContext(ctx).First(&retrieval, "id = ?", retrievalID).Error; err != nil {
		slog.Warn("could not resolve the retrieval behind an issued service certificate",
			"retrieval_id", retrievalID, "error", err)
		return nil, nil
	}
	var enrollment model.Enrollment
	if err := h.db.WithContext(ctx).First(&enrollment, "id = ?", retrieval.EnrollmentID).Error; err != nil {
		slog.Warn("could not resolve the enrollment behind an issued service certificate",
			"retrieval_id", retrievalID, "enrollment_id", retrieval.EnrollmentID, "error", err)
		return nil, nil
	}
	return &enrollment.UserID, enrollment.CertificateRequestID
}

// markResolved moves a request out of Signing into its terminal status,
// recording failureReason (empty on success) for operators to read back
// later — the signer's error detail is otherwise only a log line.
//
// Guarded on the current status the same way Deny and expire are, so a
// request already resolved some other way (denied or expired in a race)
// isn't overwritten. Zero rows affected is therefore an expected outcome
// and returns nil, not an error.
//
// A genuine DB error is returned so the caller can nack and retry: leaving
// the row in Signing lets the invalidation sweep later mark an
// already-issued certificate's request Failed. Retrying is safe because the
// reply handler's other steps (recordCertificate, notifyWaiter) are
// idempotent; if retries are ultimately exhausted the sweep is still the
// backstop, exactly as before.
func (h *SignedReplyHandler) markResolved(ctx context.Context, requestID string, status model.CertificateRequestStatus, failureReason string) error {
	result := h.db.WithContext(ctx).Model(&model.CertificateRequest{}).
		Where("id = ? AND status = ?", requestID, model.CertificateRequestStatusSigning).
		Updates(map[string]any{
			"status":         status,
			"failure_reason": failureReason,
			"resolved_at":    time.Now(),
		})
	if result.Error != nil {
		slog.Error("failed to mark certificate request resolved",
			"request_id", requestID, "status", status, "error", result.Error)
		return fmt.Errorf("mark certificate request %s resolved: %w", requestID, result.Error)
	}
	if result.RowsAffected == 0 {
		slog.Debug("certificate request was already resolved",
			"request_id", requestID, "status", status)
	}
	return nil
}
