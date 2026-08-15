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
	ListForIdentity(ctx context.Context, identity *Identity) ([]model.Certificate, error)
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

// ListForIdentity returns identity's issued certificates, newest first.
//
// Scoped by the users row behind the OIDC subject rather than by anything
// in the certificate itself, so a user cannot see another's history by
// guessing a principal or serial. An identity with no users row has no
// certificates by definition, which is an empty list rather than an error.
func (s *CertificateService) ListForIdentity(ctx context.Context, identity *Identity) ([]model.Certificate, error) {
	var user model.User
	err := s.db.WithContext(ctx).First(&user, "subject = ?", identity.Subject).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to look up the requesting user: %w", err)
	}

	var certs []model.Certificate
	if err := s.db.WithContext(ctx).
		Where("user_id = ?", user.ID).
		Order("issued_at DESC").
		Find(&certs).Error; err != nil {
		return nil, fmt.Errorf("failed to list certificates: %w", err)
	}

	return certs, nil
}
