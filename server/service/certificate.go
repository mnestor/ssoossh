package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/model"
)

// CertificateProvider reads the issued-certificate audit trail.
// CertificateService is the production implementation.
type CertificateProvider interface {
	// ListForIdentity returns identity's issued certificates ordered newest first,
	// using cursor-based pagination. The after parameter is the cursor (certificate
	// ID) from the previous page, or nil for the first page. limit controls the
	// maximum number of rows returned. Returns certificates, the next cursor (or nil
	// if no more pages), and any error. Scoped by the requesting identity's user row.
	ListForIdentity(ctx context.Context, identity *Identity, after *string, limit int) ([]model.Certificate, *string, error)
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

// ListForIdentity returns identity's issued certificates ordered newest first,
// using cursor-based pagination. The after parameter is the cursor (certificate ID)
// to start after; nil means the first page. limit is the number of rows to return.
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
// access probes.
func (s *CertificateService) ListForIdentity(ctx context.Context, identity *Identity, after *string, limit int) ([]model.Certificate, *string, error) {
	var user model.User
	err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return []model.Certificate{}, nil, nil
		}
		return nil, nil, fmt.Errorf("failed to look up the requesting user: %w", err)
	}

	query := s.db.WithContext(ctx).Where("user_id = ?", user.ID)

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
		query = query.Where("issued_at < ? OR (issued_at = ? AND id < ?)",
			cursorCert.IssuedAt, cursorCert.IssuedAt, *after)
	}

	// Query for limit+1 rows to determine if there's a next page.
	var certs []model.Certificate
	if err := query.
		Order("issued_at DESC, id DESC").
		Limit(limit + 1).
		Find(&certs).Error; err != nil {
		return nil, nil, fmt.Errorf("failed to list certificates: %w", err)
	}

	// If we got more rows than limit, we have a next page.
	var nextCursor *string
	if len(certs) > limit {
		certs = certs[:limit]
		if len(certs) > 0 {
			lastID := certs[len(certs)-1].ID
			nextCursor = &lastID
		}
	}

	return certs, nextCursor, nil
}
