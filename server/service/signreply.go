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
//     docs/signing-pipeline.md)
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
// to clean up (see docs/signing-pipeline.md), and by then
// the audit row is already durable.
func (h *SignedReplyHandler) resolveSuccess(ctx context.Context, reply certmsg.SignedReply) error {
	if err := h.recordCertificate(ctx, reply); err != nil {
		return err
	}

	h.certs.notifyWaiter(reply.RequestID, requestOutcome{
		status:      model.CertificateRequestStatusApproved,
		certificate: reply.Certificate,
	})

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

	h.certs.notifyWaiter(reply.RequestID, requestOutcome{
		status: model.CertificateRequestStatusFailed,
	})

	// Returned so a DB error nacks and retries (notifyWaiter is idempotent);
	// a benign zero-rows race comes back as nil. See resolveSuccess.
	return h.markResolved(ctx, reply.RequestID, model.CertificateRequestStatusFailed,
		fmt.Sprintf("%s: %s", reply.ErrorCode, reply.Error))
}

// recordCertificate writes the audit row for an issued certificate.
func (h *SignedReplyHandler) recordCertificate(ctx context.Context, reply certmsg.SignedReply) error {
	criticalOptions, err := json.Marshal(reply.CriticalOptions)
	if err != nil {
		return fmt.Errorf("failed to encode critical options: %w", err) // excluded from coverage: map[string]string, json.Marshal can't fail on it, see exclude-from-coverage.txt
	}
	extensions, err := json.Marshal(reply.Extensions)
	if err != nil {
		return fmt.Errorf("failed to encode extensions: %w", err) // excluded from coverage: []string, json.Marshal can't fail on it, see exclude-from-coverage.txt
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
	var req model.CertificateRequest
	switch err := h.db.WithContext(ctx).Select("user_id").First(&req, "id = ?", reply.RequestID).Error; {
	case err != nil:
		slog.Warn("could not resolve the owner of an issued certificate",
			"request_id", reply.RequestID, "error", err)
	case req.UserID == nil:
		// The request exists but was never bound to a user. Recording the
		// request ID is what keeps this row reattachable later rather than
		// permanently orphaned.
		requestID = &reply.RequestID
		slog.Warn("issued certificate has no owner: its request was never bound to a user",
			"request_id", reply.RequestID)
	default:
		userID = req.UserID
		requestID = &reply.RequestID
	}

	cert := model.Certificate{
		ID:                   uuid.NewString(),
		Type:                 reply.Type,
		UserID:               userID,
		CertificateRequestID: requestID,
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
		return fmt.Errorf("failed to persist certificate audit record: %w", err)
	}

	// For host certificates, populate the host_mappings table with the
	// principal mapping. This is the source of truth for what principals
	// `host sync` will pull down and sshd's AuthorizedPrincipalsCommand will
	// consult.
	if reply.Type == model.CertificateTypeHost && reply.Hostname != "" {
		principalsJSON, err := json.Marshal(reply.Principals)
		if err != nil {
			return fmt.Errorf("failed to encode host principals: %w", err) // excluded from coverage: []string, json.Marshal can't fail on it, see exclude-from-coverage.txt
		}

		mapping := model.HostMapping{
			ID:         uuid.NewString(),
			Hostname:   reply.Hostname,
			Principals: string(principalsJSON),
			UpdatedAt:  time.Now(),
		}

		// Upsert: create or update the mapping for this hostname.
		if err := h.db.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "hostname"}},
				UpdateAll: true,
			}).
			Create(&mapping).Error; err != nil {
			return fmt.Errorf("failed to update host mapping for %q: %w", reply.Hostname, err)
		}
	}

	return nil
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
