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

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/model"
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
//     docs/watermill-phase4-signer-listener.md)
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
// in "signing" — which is precisely what Phase 5's invalidation sweep exists
// to clean up (see docs/watermill-phase5-invalidation-sweep.md), and by then
// the audit row is already durable.
func (h *SignedReplyHandler) resolveSuccess(ctx context.Context, reply certmsg.SignedReply) error {
	if err := h.recordCertificate(ctx, reply); err != nil {
		return err
	}

	h.certs.notifyWaiter(reply.RequestID, requestOutcome{
		status:      model.CertificateRequestStatusApproved,
		certificate: reply.Certificate,
	})

	h.markResolved(ctx, reply.RequestID, model.CertificateRequestStatusApproved, "")
	return nil
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

	h.certs.notifyWaiter(reply.RequestID, requestOutcome{
		status: model.CertificateRequestStatusFailed,
	})

	h.markResolved(ctx, reply.RequestID, model.CertificateRequestStatusFailed,
		fmt.Sprintf("%s: %s", reply.ErrorCode, reply.Error))
	return nil
}

// recordCertificate writes the audit row for an issued certificate.
func (h *SignedReplyHandler) recordCertificate(ctx context.Context, reply certmsg.SignedReply) error {
	criticalOptions, err := json.Marshal(reply.CriticalOptions)
	if err != nil {
		return fmt.Errorf("failed to encode critical options: %w", err)
	}
	extensions, err := json.Marshal(reply.Extensions)
	if err != nil {
		return fmt.Errorf("failed to encode extensions: %w", err)
	}

	cert := model.Certificate{
		ID:   uuid.NewString(),
		Type: reply.Type,
		// TODO: UserID is left unset — nothing currently resolves the
		// approving identity to a users row, and certificate_requests.user_id
		// is likewise never written. Populating both is its own change; see
		// docs/watermill-phase4-signer-listener.md's deferred items.
		Hostname:             reply.Hostname,
		PublicKeyFingerprint: reply.PublicKeyFingerprint,
		SerialNumber:         reply.Serial,
		KeyID:                reply.KeyID,
		Principals:           strings.Join(reply.Principals, ","),
		CriticalOptions:      string(criticalOptions),
		Extensions:           string(extensions),
		IssuedAt:             reply.ValidAfter,
		ExpiresAt:            reply.ValidBefore,
	}

	if err := h.db.WithContext(ctx).Create(&cert).Error; err != nil {
		return fmt.Errorf("failed to persist certificate audit record: %w", err)
	}
	return nil
}

// markResolved moves a request out of Signing into its terminal status,
// recording failureReason (empty on success) for operators to read back
// later — the signer's error detail is otherwise only a log line.
//
// Guarded on the current status the same way Deny and expire are, so a
// request already resolved some other way (denied or expired in a race)
// isn't overwritten. Zero rows affected is therefore an expected outcome,
// not an error — and this is deliberately not fatal to handling the reply:
// the certificate has already been delivered and audited by this point, so
// failing here would redeliver the whole reply and duplicate the audit row
// to fix nothing.
func (h *SignedReplyHandler) markResolved(ctx context.Context, requestID string, status model.CertificateRequestStatus, failureReason string) {
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
		return
	}
	if result.RowsAffected == 0 {
		slog.Debug("certificate request was already resolved",
			"request_id", requestID, "status", status)
	}
}
