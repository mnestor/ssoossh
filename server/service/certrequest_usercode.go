package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// ResolveUserCode turns a code a human typed into the web UI into the
// request ID behind it, and binds that request to them.
//
// This is the console flow's equivalent of opening /approve/<id>: a machine
// with no browser in front of it cannot print a URL anyone will transcribe,
// so it prints eight characters instead and the approver carries them to a
// device that does have a browser.
//
// Three properties are load-bearing, and none of them are conveniences:
//
//   - The caller must already be authenticated. This method is reachable
//     only from a session-guarded route, so an unauthenticated caller never
//     learns whether a code is live and never receives a request ID — and
//     the request ID is the credential the certificate is delivered
//     against (see NewCertRequestController). Turning 40 bits into an
//     unauthenticated path to that ID is the one thing this design must not
//     do.
//   - Resolving claims the request, through the same bindRequester the
//     approval page uses. Claiming here rather than at the redirect target
//     is what resolves a race between two sessions submitting the same code
//     before either has seen anything about the request.
//   - The three failure modes stay distinct — no such code, expired,
//     claimed by someone else — because they send the person in front of
//     the console to three different next actions.
func (s *CertRequestService) ResolveUserCode(ctx context.Context, submitted string, identity *Identity) (string, error) {
	code, err := NormalizeUserCode(submitted)
	if err != nil {
		return "", &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("that is not a valid code: %s", err)}
	}

	// Scoped to still-approvable rows, which is also what the partial
	// unique index covers: a code is unique among those, and only among
	// those, so a query without the status predicate could match several
	// long-resolved rows that happened to share a value.
	var req model.CertificateRequest
	err = s.db.WithContext(ctx).
		Where("user_code = ? AND status IN ?", code, []model.CertificateRequestStatus{
			model.CertificateRequestStatusPending,
			model.CertificateRequestStatusSigning,
		}).
		First(&req).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		// Deliberately says nothing about whether this code was ever real.
		// The caller is authenticated, so this is not the oracle an
		// unauthenticated one would be, but there is still no reason to
		// help someone map the space.
		return "", &errorresponses.NotFoundError{Resource: "console login request for that code"}
	case err != nil:
		return "", fmt.Errorf("failed to look up certificate request by user code: %w", err)
	}

	// A pending row past its own type's budget has not been flipped to
	// expired yet — that happens lazily, in Wait. Do it here too, so the
	// person typing the code is told the login timed out rather than being
	// walked onto an approval page for a request the waiting console has
	// already given up on.
	if req.Status == model.CertificateRequestStatusPending {
		if cutoff := s.ttlCutoffFor(req.Type); !cutoff.IsZero() && req.CreatedAt.Before(cutoff) {
			s.expire(ctx, req.ID)
			return "", &errorresponses.ExpiredError{Resource: "console login request"}
		}
	}

	user, err := s.resolveUser(ctx, identity)
	if err != nil {
		return "", err
	}
	// bindRequester returns ForbiddenError when the request already
	// belongs to someone else, which is the "one code, one request, one
	// shot" rule: a second session submitting the same code is refused the
	// same way a second browser opening /approve/<id> is.
	if err := s.bindRequester(ctx, &req, user); err != nil {
		slog.Warn("console user code submitted for a request bound to another user",
			slog.String("request_id", req.ID),
			slog.String("subject", identity.Subject),
		)
		return "", err
	}

	// Recorded because this is the moment an unauthenticated machine's
	// console login acquires a named human, which is exactly the step the
	// consent-phishing case turns on. The code itself is absent: it is a
	// credential, and the never-log-sensitive-data rule covers audit
	// payloads too (see approveServiceEnrollment).
	s.auditRecord(ctx, AuditEvent{
		Action: AuditCertCodeResolved,
		Actor:  AuditSubjectFromIdentity(identity, user.ID),
		Detail: map[string]any{
			"request_id": req.ID,
			"cert_type":  string(req.Type),
			"hostname":   req.Hostname,
			"tty":        req.TTY,
		},
	})

	return req.ID, nil
}
