package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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
// created_at and created_at+RequestTTL (it expires after that), so the
// latest it can possibly have started signing is created_at+RequestTTL:
//
//	stranded if created_at < now - (RequestTTL + SigningTimeout)
//
// That bound can never catch a request still legitimately in flight. It
// errs long — a request approved immediately waits RequestTTL longer than
// necessary — which is the right direction: the cost is a client waiting
// longer for bad news, where erring short would cancel certificates that
// were about to be issued.
//
// When RequestTTL is disabled (zero), approval is unbounded and no such
// derivation is possible, so this reports every signing request as
// stranded. That's correct at startup, when the in-memory queue is
// definitionally empty, and wrong at any other time — which is why
// bootstrap only schedules the periodic pass when RequestTTL is set. See
// docs/signing-pipeline.md.
func (s *CertRequestService) SweepStrandedRequests(ctx context.Context) error {
	var stranded []model.CertificateRequest
	q := s.db.WithContext(ctx).Where("status = ?", model.CertificateRequestStatusSigning)
	if cutoff := s.strandedCutoff(); !cutoff.IsZero() {
		q = q.Where("created_at < ?", cutoff)
	}
	if err := q.Find(&stranded).Error; err != nil {
		return fmt.Errorf("failed to list stranded certificate requests: %w", err)
	}

	for _, req := range stranded {
		s.failStranded(ctx, req.ID)
	}
	return nil
}

// strandedCutoff returns the created_at threshold before which a signing
// request is considered stranded, or the zero Time when RequestTTL is
// disabled and no bound can be derived (see SweepStrandedRequests).
func (s *CertRequestService) strandedCutoff() time.Time {
	if s.config.CertOptions.RequestTTL <= 0 {
		return time.Time{}
	}
	return time.Now().Add(-(s.config.CertOptions.RequestTTL + s.config.CertOptions.SigningTimeout))
}

// failStranded marks one request failed and wakes anything waiting on it.
//
// Guarded on the current status the same way Deny, expire, and the
// listener's markResolved are, so a request that resolved between the
// select above and this update isn't overwritten — zero rows affected is an
// expected outcome, not an error.
//
// Notifying matters as much as the update: Wait only re-reads the database
// when a message lands on the request's wake topic, so a client blocked on
// a swept request would keep blocking against a row that already reads
// failed.
func (s *CertRequestService) failStranded(ctx context.Context, requestID string) {
	result := s.db.WithContext(ctx).Model(&model.CertificateRequest{}).
		Where("id = ? AND status = ?", requestID, model.CertificateRequestStatusSigning).
		Updates(map[string]any{
			"status":         model.CertificateRequestStatusFailed,
			"failure_reason": FailureReasonStranded,
			"resolved_at":    time.Now(),
		})
	if result.Error != nil {
		slog.Error("failed to invalidate stranded certificate request",
			"request_id", requestID, "error", result.Error)
		return
	}
	if result.RowsAffected == 0 {
		slog.Debug("stranded certificate request was already resolved", "request_id", requestID)
		return
	}

	slog.Warn("invalidated stranded certificate request",
		"request_id", requestID, "reason", FailureReasonStranded)

	s.notifyWaiter(requestID, requestOutcome{status: model.CertificateRequestStatusFailed})
}
