package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// CertificateWithDecision combines a Certificate with its related decision
// record (if any) and, for a service certificate, the redemption that
// produced it. Used internally to avoid fetching either separately.
type CertificateWithDecision struct {
	Certificate model.Certificate
	Decision    *model.CertificateRequestDecision

	// Retrieval is set only for a service certificate, and names the machine
	// that actually fetched it. Nil for every other type, and for a service
	// certificate whose retrieval row has gone.
	Retrieval *CertificateRetrieval
}

// CertificateRetrieval is where a service certificate came from: the
// `service retrieve` redemption that produced it.
//
// This is a different question from the decision record beside it. A
// service certificate's decision is the *enrollment approval* — a human in
// a browser, possibly months earlier — and every certificate the code mints
// shares it. The retrieval is this one certificate's own origin, which for
// an unattended job is the only address that says where it ran.
type CertificateRetrieval struct {
	EnrollmentID string
	SourceIP     string
	RetrievedAt  time.Time
}

// CertificateProvider reads the issued-certificate audit trail.
// CertificateService is the production implementation.
type CertificateProvider interface {
	// ListForIdentity returns identity's issued certificates with their decision
	// records (if any), ordered newest first using cursor-based pagination. The
	// after parameter is the cursor (certificate ID) from the previous page, or
	// nil for the first page. limit controls the maximum number of rows returned.
	// Returns certificates, the next cursor (or nil if no more pages), and any error.
	// Scoped by the requesting identity's user row.
	ListForIdentity(ctx context.Context, identity *Identity, after *string, limit int) ([]CertificateWithDecision, *string, error)

	// GetByID returns the certificate identified by id if the caller has permission to read it.
	// Authorization: the certificate's approving user (the one whose users.id matches
	// certificate.user_id), or any identity with auditor-level access. Returns 404
	// uniformly for both "certificate does not exist" and "caller does not have
	// permission", so the response does not leak existence to an unauthorized caller.
	GetByID(ctx context.Context, id string, identity *Identity, cfg *config.Config) (CertificateWithDecision, error)
}

// CertificateService serves the per-user certificate history the web UI
// shows. It only reads: rows are written by SignedReplyHandler as
// certificates are issued.
//
// Note the history is metadata, never the certificate itself — certificates
// are ephemeral and deliberately not persisted (see
// docs/signing-pipeline.md).
type CertificateService struct {
	db *gorm.DB
}

// NewCertificateService constructs a CertificateService over db.
func NewCertificateService(db *gorm.DB) *CertificateService {
	return &CertificateService{db: db}
}

// ListForIdentity returns identity's issued certificates with their decision
// records (if any), ordered newest first, using cursor-based pagination. The
// after parameter is the cursor (certificate ID) to start after; nil means
// the first page. limit is the number of rows to return.
//
// Returns the certificates, the next cursor (nil if no more pages), and any error.
// Scoped by the users row behind the OIDC subject rather than by anything in the
// certificate itself, so a user cannot see another's history by guessing a
// principal or serial. An identity with no users row has no certificates by
// definition, which is an empty list rather than an error.
//
// The seek-based pagination uses a stable total ordering of (issued_at DESC, id DESC),
// matching the composite index on certificates(user_id, issued_at DESC). The cursor
// certificate's issued_at is looked up and scoped by user_id to prevent cross-user
// access probes. Each certificate is paired with its originating request's decision
// record (if any) via LEFT JOIN.
func (s *CertificateService) ListForIdentity(ctx context.Context, identity *Identity, after *string, limit int) ([]CertificateWithDecision, *string, error) {
	var user model.User
	err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []CertificateWithDecision{}, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to look up the requesting user: %w", err)
	}

	query := s.db.WithContext(ctx).Where("certificates.user_id = ?", user.ID)

	// If a cursor is provided, look it up to get its issued_at, then build the seek predicate.
	if after != nil {
		var cursorCert model.Certificate
		err := s.db.WithContext(ctx).
			Where("user_id = ? AND id = ?", user.ID, *after).
			First(&cursorCert).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, nil, fmt.Errorf("cursor certificate not found or does not belong to this user")
			}
			return nil, nil, fmt.Errorf("failed to look up cursor certificate: %w", err)
		}

		// Seek predicate: (issued_at < ?) OR (issued_at = ? AND id < ?)
		// This ensures stable ordering across concurrent issuance.
		query = query.Where("certificates.issued_at < ? OR (certificates.issued_at = ? AND certificates.id < ?)",
			cursorCert.IssuedAt, cursorCert.IssuedAt, *after)
	}

	// Fetch certificates with their related decision data via a LEFT JOIN.
	// Select explicit, qualified columns to avoid ambiguity (both certificates
	// and certificate_request_decisions have id and timestamp columns).
	type rawRow struct {
		CertID                       string
		CertType                     model.CertificateType
		CertUserID                   *string
		CertCertificateRequestID     *string
		CertSerialNumber             uint64
		CertKeyID                    string
		CertPrincipals               string
		CertPublicKeyFingerprint     string
		CertIssuedAt                 time.Time
		CertExpiresAt                time.Time
		DecisionID                   *string
		DecisionCertificateRequestID *string
		DecisionOutcome              *model.CertificateRequestDecisionOutcome
		DecisionSubject              *string
		DecisionUsername             *string
		DecisionEmail                *string
		DecisionSourceIP             *string
		DecisionUserAgent            *string
		DecisionAcceptLanguage       *string
		DecisionForwardedFor         *string
		DecisionGroups               *string
		DecisionOtherAccounts        *string
		DecisionServiceAccounts      *string
		DecisionDecidedAt            *time.Time
		RetrievalEnrollmentID        *string
		RetrievalSourceIP            *string
		RetrievalRetrievedAt         *time.Time
	}

	var results []rawRow
	if err := query.
		Model(&model.Certificate{}).
		Select(`certificates.id as cert_id, certificates.type as cert_type,
			certificates.user_id as cert_user_id,
			certificates.certificate_request_id as cert_certificate_request_id,
			certificates.serial_number as cert_serial_number,
			certificates.key_id as cert_key_id, certificates.principals as cert_principals,
			certificates.public_key_fingerprint as cert_public_key_fingerprint,
			certificates.issued_at as cert_issued_at,
			certificates.expires_at as cert_expires_at,
			certificate_request_decisions.id as decision_id,
			certificate_request_decisions.certificate_request_id as decision_certificate_request_id,
			certificate_request_decisions.outcome as decision_outcome,
			certificate_request_decisions.subject as decision_subject,
			certificate_request_decisions.username as decision_username,
			certificate_request_decisions.email as decision_email,
			certificate_request_decisions.source_ip as decision_source_ip,
			certificate_request_decisions.user_agent as decision_user_agent,
			certificate_request_decisions.accept_language as decision_accept_language,
			certificate_request_decisions.forwarded_for as decision_forwarded_for,
			certificate_request_decisions.groups as decision_groups,
			certificate_request_decisions.other_accounts as decision_other_accounts,
			certificate_request_decisions.service_accounts as decision_service_accounts,
			certificate_request_decisions.decided_at as decision_decided_at,
			enrollment_retrievals.enrollment_id as retrieval_enrollment_id,
			enrollment_retrievals.source_ip as retrieval_source_ip,
			enrollment_retrievals.retrieved_at as retrieval_retrieved_at`).
		Joins("LEFT JOIN certificate_requests ON certificates.certificate_request_id = certificate_requests.id").
		Joins("LEFT JOIN certificate_request_decisions ON certificate_requests.id = certificate_request_decisions.certificate_request_id").
		// Joined on the serial rather than a foreign key, because that is the
		// only thing the two rows share: EnrollmentService.Retrieve allocates
		// the serial, writes it to the retrieval row, and sends the same value
		// to the signer, which is what ends up on the certificate. Serials are
		// uniquely indexed on certificates and only service redemptions write
		// enrollment_retrievals, so this matches at most one row and stays null
		// for every user and PAM certificate.
		Joins("LEFT JOIN enrollment_retrievals ON enrollment_retrievals.certificate_serial = certificates.serial_number").
		Order("certificates.issued_at DESC, certificates.id DESC").
		Limit(limit + 1).
		Scan(&results).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to list certificates with decisions: %w", err)
	}

	// If we got more rows than limit, we have a next page.
	var nextCursor *string
	if len(results) > limit {
		results = results[:limit]
		if len(results) > 0 {
			nextCursor = &results[len(results)-1].CertID
		}
	}

	out := make([]CertificateWithDecision, 0, len(results))
	for _, r := range results {
		cert := model.Certificate{
			ID:                   r.CertID,
			Type:                 r.CertType,
			UserID:               r.CertUserID,
			CertificateRequestID: r.CertCertificateRequestID,
			SerialNumber:         r.CertSerialNumber,
			KeyID:                r.CertKeyID,
			Principals:           r.CertPrincipals,
			PublicKeyFingerprint: r.CertPublicKeyFingerprint,
			IssuedAt:             r.CertIssuedAt,
			ExpiresAt:            r.CertExpiresAt,
		}

		var decision *model.CertificateRequestDecision
		// Only construct the decision if it exists (decision_id is not nil).
		if r.DecisionID != nil {
			decision = &model.CertificateRequestDecision{
				ID:                   *r.DecisionID,
				CertificateRequestID: *r.DecisionCertificateRequestID,
				Outcome:              *r.DecisionOutcome,
				Subject:              *r.DecisionSubject,
				Username:             *r.DecisionUsername,
				Email:                *r.DecisionEmail,
				SourceIP:             *r.DecisionSourceIP,
				UserAgent:            *r.DecisionUserAgent,
				AcceptLanguage:       *r.DecisionAcceptLanguage,
				ForwardedFor:         *r.DecisionForwardedFor,
				Groups:               *r.DecisionGroups,
				OtherAccounts:        *r.DecisionOtherAccounts,
				ServiceAccounts:      *r.DecisionServiceAccounts,
				DecidedAt:            *r.DecisionDecidedAt,
			}
		}

		// Keyed on the enrollment id: a retrieval row always has one, so its
		// presence is what says the join matched, the same way decision_id
		// does above.
		var retrieval *CertificateRetrieval
		if r.RetrievalEnrollmentID != nil {
			retrieval = &CertificateRetrieval{
				EnrollmentID: *r.RetrievalEnrollmentID,
				SourceIP:     *r.RetrievalSourceIP,
				RetrievedAt:  *r.RetrievalRetrievedAt,
			}
		}

		out = append(out, CertificateWithDecision{
			Certificate: cert,
			Decision:    decision,
			Retrieval:   retrieval,
		})
	}

	return out, nextCursor, nil
}

// GetByID returns a single certificate by its ID if the caller is authorized to read it.
//
// Authorization: the certificate's approving user (identity whose users row ID matches
// the certificate's UserID), or any identity whose groups grant auditor-level access
// (config.AdminConfig.GrantsAuditor). Returns NotFoundError uniformly for both
// "certificate does not exist" and "caller lacks permission", which keeps unauthorized
// callers from probing for certificate existence. Follows the pattern established by
// EnrollmentService.ListRetrievals, but with unified error handling to prevent
// existence leakage.
func (s *CertificateService) GetByID(ctx context.Context, id string, identity *Identity, cfg *config.Config) (CertificateWithDecision, error) {
	// Fetch the certificate and its related decision via LEFT JOIN, same as ListForIdentity.
	type rawRow struct {
		CertID                       string
		CertType                     model.CertificateType
		CertUserID                   *string
		CertCertificateRequestID     *string
		CertSerialNumber             uint64
		CertKeyID                    string
		CertPrincipals               string
		CertPublicKeyFingerprint     string
		CertIssuedAt                 time.Time
		CertExpiresAt                time.Time
		DecisionID                   *string
		DecisionCertificateRequestID *string
		DecisionOutcome              *model.CertificateRequestDecisionOutcome
		DecisionSubject              *string
		DecisionUsername             *string
		DecisionEmail                *string
		DecisionSourceIP             *string
		DecisionUserAgent            *string
		DecisionAcceptLanguage       *string
		DecisionForwardedFor         *string
		DecisionGroups               *string
		DecisionOtherAccounts        *string
		DecisionServiceAccounts      *string
		DecisionDecidedAt            *time.Time
		RetrievalEnrollmentID        *string
		RetrievalSourceIP            *string
		RetrievalRetrievedAt         *time.Time
	}

	var result rawRow
	if err := s.db.WithContext(ctx).
		Model(&model.Certificate{}).
		Select(`certificates.id as cert_id, certificates.type as cert_type,
			certificates.user_id as cert_user_id,
			certificates.certificate_request_id as cert_certificate_request_id,
			certificates.serial_number as cert_serial_number,
			certificates.key_id as cert_key_id, certificates.principals as cert_principals,
			certificates.public_key_fingerprint as cert_public_key_fingerprint,
			certificates.issued_at as cert_issued_at,
			certificates.expires_at as cert_expires_at,
			certificate_request_decisions.id as decision_id,
			certificate_request_decisions.certificate_request_id as decision_certificate_request_id,
			certificate_request_decisions.outcome as decision_outcome,
			certificate_request_decisions.subject as decision_subject,
			certificate_request_decisions.username as decision_username,
			certificate_request_decisions.email as decision_email,
			certificate_request_decisions.source_ip as decision_source_ip,
			certificate_request_decisions.user_agent as decision_user_agent,
			certificate_request_decisions.accept_language as decision_accept_language,
			certificate_request_decisions.forwarded_for as decision_forwarded_for,
			certificate_request_decisions.groups as decision_groups,
			certificate_request_decisions.other_accounts as decision_other_accounts,
			certificate_request_decisions.service_accounts as decision_service_accounts,
			certificate_request_decisions.decided_at as decision_decided_at,
			enrollment_retrievals.enrollment_id as retrieval_enrollment_id,
			enrollment_retrievals.source_ip as retrieval_source_ip,
			enrollment_retrievals.retrieved_at as retrieval_retrieved_at`).
		Joins("LEFT JOIN certificate_requests ON certificates.certificate_request_id = certificate_requests.id").
		Joins("LEFT JOIN certificate_request_decisions ON certificate_requests.id = certificate_request_decisions.certificate_request_id").
		Joins("LEFT JOIN enrollment_retrievals ON enrollment_retrievals.certificate_serial = certificates.serial_number").
		Where("certificates.id = ?", id).
		Scan(&result).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CertificateWithDecision{}, &errorresponses.NotFoundError{Resource: "certificate"}
		}
		return CertificateWithDecision{}, fmt.Errorf("failed to look up certificate: %w", err)
	}

	// Check if we got a row. If not, return 404.
	if result.CertID == "" {
		return CertificateWithDecision{}, &errorresponses.NotFoundError{Resource: "certificate"}
	}

	// Authorization: check if the caller is the approver or has auditor-level access.
	// If cfg is nil (for tests), only allow the approver.
	isAuditor := cfg != nil && cfg.Admin.GrantsAuditor(identity.Groups)
	if !isAuditor {
		// Not an auditor: check if they're the owner (via users row).
		var user model.User
		err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error
		if err != nil || (result.CertUserID == nil || user.ID != *result.CertUserID) {
			// Certificate belongs to someone else, or caller has no user row.
			// Return uniform 404 to not leak existence.
			return CertificateWithDecision{}, &errorresponses.NotFoundError{Resource: "certificate"}
		}
	}

	// Authorized: construct and return the response.
	cert := model.Certificate{
		ID:                   result.CertID,
		Type:                 result.CertType,
		UserID:               result.CertUserID,
		CertificateRequestID: result.CertCertificateRequestID,
		SerialNumber:         result.CertSerialNumber,
		KeyID:                result.CertKeyID,
		Principals:           result.CertPrincipals,
		PublicKeyFingerprint: result.CertPublicKeyFingerprint,
		IssuedAt:             result.CertIssuedAt,
		ExpiresAt:            result.CertExpiresAt,
	}

	var decision *model.CertificateRequestDecision
	if result.DecisionID != nil {
		decision = &model.CertificateRequestDecision{
			ID:                   *result.DecisionID,
			CertificateRequestID: *result.DecisionCertificateRequestID,
			Outcome:              *result.DecisionOutcome,
			Subject:              *result.DecisionSubject,
			Username:             *result.DecisionUsername,
			Email:                *result.DecisionEmail,
			SourceIP:             *result.DecisionSourceIP,
			UserAgent:            *result.DecisionUserAgent,
			AcceptLanguage:       *result.DecisionAcceptLanguage,
			ForwardedFor:         *result.DecisionForwardedFor,
			Groups:               *result.DecisionGroups,
			OtherAccounts:        *result.DecisionOtherAccounts,
			ServiceAccounts:      *result.DecisionServiceAccounts,
			DecidedAt:            *result.DecisionDecidedAt,
		}
	}

	var retrieval *CertificateRetrieval
	if result.RetrievalEnrollmentID != nil {
		retrieval = &CertificateRetrieval{
			EnrollmentID: *result.RetrievalEnrollmentID,
			SourceIP:     *result.RetrievalSourceIP,
			RetrievedAt:  *result.RetrievalRetrievedAt,
		}
	}

	return CertificateWithDecision{
		Certificate: cert,
		Decision:    decision,
		Retrieval:   retrieval,
	}, nil
}
