package service

// Test methodology: unit tests against a real in-memory sqlite *gorm.DB,
// same as certrequest_test.go. These cover the scoping rule, which is the
// only interesting behavior here — a user must see their own certificates
// and nobody else's.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mnestor/ssoossh/server/model"
)

// newTestCertificateService returns a CertificateService over the same DB as
// svc, migrated for certificates.
func newTestCertificateService(t *testing.T, svc *CertRequestService) *CertificateService {
	t.Helper()

	if err := svc.db.AutoMigrate(&model.Certificate{}); err != nil {
		t.Fatalf("failed to migrate certificates table: %v", err)
	}
	return NewCertificateService(svc.db)
}

// seedCertificate inserts an issued-certificate audit row owned by userID
// (nil for none).
func seedCertificate(t *testing.T, svc *CertRequestService, userID *string, serial uint64, issuedAt time.Time) {
	t.Helper()

	cert := model.Certificate{
		ID:           uuid.NewString(),
		Type:         model.CertificateTypeUser,
		UserID:       userID,
		SerialNumber: serial,
		IssuedAt:     issuedAt,
		ExpiresAt:    issuedAt.Add(time.Hour),
	}
	if err := svc.db.Create(&cert).Error; err != nil {
		t.Fatalf("failed to seed certificate: %v", err)
	}
}

// TestCertificateService_ShouldReturnOnlyTheCallersCertificates is the
// scoping guarantee the history endpoint rests on. Scoping is by users row,
// so nothing the caller can send widens it.
func TestCertificateService_ShouldReturnOnlyTheCallersCertificates(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	aliceID := seedUser(t, reqSvc.db, "sub-alice")
	bobID := seedUser(t, reqSvc.db, "sub-bob")

	now := time.Now()
	seedCertificate(t, reqSvc, &aliceID, 1, now.Add(-2*time.Hour))
	seedCertificate(t, reqSvc, &aliceID, 2, now.Add(-time.Hour))
	seedCertificate(t, reqSvc, &bobID, 3, now)
	seedCertificate(t, reqSvc, nil, 4, now)

	got, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d certificates, want alice's 2", len(got))
	}
	for _, c := range got {
		if c.SerialNumber == 3 || c.SerialNumber == 4 {
			t.Errorf("got certificate with serial %d, which does not belong to alice", c.SerialNumber)
		}
	}
	// Newest first, so the UI can render a history without re-sorting.
	if got[0].SerialNumber != 2 {
		t.Errorf("got serial %d first, want the newest (2)", got[0].SerialNumber)
	}
}

// TestCertificateService_ShouldReturnNothingForAnIdentityWithNoUserRecord
// keeps a session that outlived its user row from erroring the history page:
// no user means no certificates, which is an empty list.
func TestCertificateService_ShouldReturnNothingForAnIdentityWithNoUserRecord(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	got, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-ghost"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d certificates, want none", len(got))
	}
}
