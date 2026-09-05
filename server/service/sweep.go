package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm/clause"

	"github.com/mnestor/ssoossh/server/model"
)

// FailureReasonStranded is recorded on requests the sweep invalidates.
const FailureReasonStranded = "stranded awaiting signature"

// SweepStrandedRequests fails every certificate request that has been
// awaiting signature for longer than it plausibly could be, and tells any
// client still waiting on one.
//
// Such a request is approved and queued but will never be signed: its
// signing job (or the signed reply) was lost. That happens on a restart —
// the queue is in-memory — but also in a perfectly healthy process, because
// server/pubsub's dropAfterRetries deliberately drops a message once
// retries are exhausted rather than redelivering it forever. Nothing else
// resolves these: Wait applies no timeout to the signing state, so without
// this sweep both the row and the client block indefinitely.
//
// # Deciding what's stranded
//
// Nothing records when a request entered the signing state, so its age is
// derived from creation instead. A request can only be approved between
// created_at and created_at+ApprovalTTL (it expires after that), so the
// latest it can possibly have started signing is created_at+ApprovalTTL:
//
//	stranded if created_at < now - (ApprovalTTL + SigningGrace)
//
// That bound can never catch a request still legitimately in flight. It
// errs long — a request approved immediately waits ApprovalTTL longer than
// necessary — which is the right direction: the cost is a client waiting
// longer for bad news, where erring short would cancel certificates that
// were about to be issued.
//
// When ApprovalTTL is disabled (zero), approval is unbounded and no such
// derivation is possible, so this reports every signing request as
// stranded. That's correct at startup, when the in-memory queue is
// definitionally empty, and wrong at any other time — which is why
// bootstrap only schedules the periodic pass when ApprovalTTL is set. See
// https://mnestor.github.io/ssoossh/internals/architecture/.
func (s *CertRequestService) SweepStrandedRequests(ctx context.Context) error {
	var stranded []model.CertificateRequest
	q := s.db.WithContext(ctx).Where("status = ?", model.CertificateRequestStatusSigning)
	if cutoff := s.strandedCutoff(); !cutoff.IsZero() {
		q = q.Where("created_at < ?", cutoff)
	}
	if err := q.Find(&stranded).Error; err != nil {
		return fmt.Errorf("failed to list stranded certificate requests: %w", err)
	}
	if len(stranded) == 0 {
		return nil
	}

	ids := make([]string, len(stranded))
	for i, req := range stranded {
		ids[i] = req.ID
	}
	s.failStranded(ctx, ids)
	return nil
}

// strandedCutoff returns the created_at threshold before which a signing
// request is considered stranded, or the zero Time when ApprovalTTL is
// disabled and no bound can be derived (see SweepStrandedRequests).
//
// UTC, and it has to be: this value is compared against created_at, which
// SQLite compares as a string, so a local-offset cutoff against UTC-stored
// rows compares by literal digits rather than by instant — the sweep would
// then skip stranded requests, or fail live ones, whenever the two offsets
// differ. See package dbtime.
func (s *CertRequestService) strandedCutoff() time.Time {
	if s.config.CertOptions.ApprovalTTL() <= 0 {
		return time.Time{}
	}
	return time.Now().Add(-(s.config.CertOptions.ApprovalTTL() + s.config.CertOptions.SigningGrace())).UTC()
}

// failStranded marks every request in ids failed, with a single UPDATE, and
// wakes anything waiting on each one it actually changed.
//
// Guarded on the current status the same way Deny, expire, and the
// listener's markResolved are, so a request that resolved between the
// select above and this update isn't overwritten — the WHERE clause simply
// excludes it, which is an expected outcome, not an error. RETURNING id
// reports exactly which of ids this call actually updated, so notifyWaiter
// — which caches its outcome for Wait to trust without a DB read — is only
// called for those, never for a request some other path already resolved.
func (s *CertRequestService) failStranded(ctx context.Context, ids []string) {
	// RETURNING the whole row rather than just id: the cert.sign_failed
	// event below carries the request's type and host context, and this
	// is the one read that already has them.
	var updated []model.CertificateRequest
	result := s.db.WithContext(ctx).
		Clauses(clause.Returning{}).
		Model(&updated).
		Where("id IN ? AND status = ?", ids, model.CertificateRequestStatusSigning).
		Updates(map[string]any{
			"status":         model.CertificateRequestStatusFailed,
			"failure_reason": FailureReasonStranded,
			"resolved_at":    time.Now(),
		})
	if result.Error != nil {
		slog.Error("failed to invalidate stranded certificate requests",
			"request_ids", ids, "error", result.Error)
		return
	}

	updatedIDs := make(map[string]bool, len(updated))
	for _, req := range updated {
		updatedIDs[req.ID] = true
		slog.Warn("invalidated stranded certificate request",
			"request_id", req.ID, "reason", FailureReasonStranded)
		// Same event the listener emits for a signer refusal: an approval
		// that never became a certificate, here because the job was lost
		// rather than rejected. The reason string tells the two apart.
		s.auditRecord(ctx, AuditEvent{
			Action:     AuditCertSignFailed,
			System:     true,
			OccurredAt: time.Now(),
			Detail: withDetail(hostContextDetail(req), map[string]any{
				"request_id": req.ID,
				"cert_type":  string(req.Type),
				"source_ip":  req.SourceIP,
				"error_code": "stranded",
				"error":      FailureReasonStranded,
			}),
		})
		s.notifyWaiter(req.ID, WaitOutcome{Status: model.CertificateRequestStatusFailed})
	}
	for _, id := range ids {
		if !updatedIDs[id] {
			slog.Debug("stranded certificate request was already resolved", "request_id", id)
		}
	}
}
