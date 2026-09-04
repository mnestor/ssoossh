package service

// Test methodology: unit tests against a real in-memory sqlite *gorm.DB,
// same as certrequest_test.go. These cover the scoping rule, which is the
// only interesting behavior here — a user must see their own certificates
// and nobody else's. Pagination tests cover the cursor-based seek logic.
// Decision join tests ensure no ambiguous column selection.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// newTestCertificateService returns a CertificateService over the same DB as
// svc, migrated for certificates.
func newTestCertificateService(t *testing.T, svc *CertRequestService) *CertificateService {
	t.Helper()

	// EnrollmentRetrieval is in the list because ListForIdentity LEFT JOINs
	// it to find where a service certificate was fetched from. The join is
	// on every row, service or not, so the table has to exist even for a
	// test that only seeds user certificates.
	if err := svc.db.AutoMigrate(&model.Certificate{}, &model.CertificateRequest{},
		&model.CertificateRequestDecision{}, &model.EnrollmentRetrieval{}); err != nil {
		t.Fatalf("failed to migrate certificate tables: %v", err)
	}
	return NewCertificateService(svc.db)
}

// seedCertificate inserts an issued-certificate audit row owned by userID
// (nil for none) and returns a pointer to the created certificate.
func seedCertificate(t *testing.T, svc *CertRequestService, userID *string, serial uint64, issuedAt time.Time) *model.Certificate {
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
	return &cert
}

// seedCertificateWithRequest inserts a certificate and links it to a request and decision.
func seedCertificateWithRequest(t *testing.T, svc *CertRequestService, userID *string, serial uint64, issuedAt time.Time, decision *model.CertificateRequestDecision) *model.Certificate {
	t.Helper()

	// Create a request first.
	req := model.CertificateRequest{
		ID:        uuid.NewString(),
		UserID:    userID,
		Type:      model.CertificateTypeUser,
		Status:    model.CertificateRequestStatusApproved,
		PublicKey: "test-key",
		SourceIP:  "127.0.0.1",
	}
	if err := svc.db.Create(&req).Error; err != nil {
		t.Fatalf("failed to seed certificate request: %v", err)
	}

	// Create the decision if provided.
	if decision != nil {
		decision.ID = uuid.NewString()
		decision.CertificateRequestID = req.ID
		if err := svc.db.Create(&decision).Error; err != nil {
			t.Fatalf("failed to seed decision: %v", err)
		}
	}

	// Create the certificate linked to the request.
	cert := model.Certificate{
		ID:                   uuid.NewString(),
		Type:                 model.CertificateTypeUser,
		UserID:               userID,
		SerialNumber:         serial,
		IssuedAt:             issuedAt,
		ExpiresAt:            issuedAt.Add(time.Hour),
		CertificateRequestID: &req.ID,
	}
	if err := svc.db.Create(&cert).Error; err != nil {
		t.Fatalf("failed to seed certificate with request: %v", err)
	}
	return &cert
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

	got, nextCursor, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("got %d certificates, want alice's 2", len(got))
	}
	for _, cd := range got {
		if cd.Certificate.SerialNumber == 3 || cd.Certificate.SerialNumber == 4 {
			t.Errorf("got certificate with serial %d, which does not belong to alice", cd.Certificate.SerialNumber)
		}
	}
	// Newest first, so the UI can render a history without re-sorting.
	if got[0].Certificate.SerialNumber != 2 {
		t.Errorf("got serial %d first, want the newest (2)", got[0].Certificate.SerialNumber)
	}
	// No next page with limit=100 and only 2 results.
	if nextCursor != nil {
		t.Errorf("got nextCursor = %v, want nil", nextCursor)
	}
}

// TestCertificateService_ShouldSurfaceAGenericDBErrorOnUserLookup covers
// the non-not-found error branch in the user lookup, distinct from the
// no-user-record test below (which expects an empty list, not an error).
func TestCertificateService_ShouldSurfaceAGenericDBErrorOnUserLookup(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)
	closeUnderlyingDB(t, reqSvc.db)

	if _, _, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 25); err == nil {
		t.Error("ListForIdentity() error = nil, want error")
	}
}

// TestCertificateService_ShouldSurfaceAGenericDBErrorListingCertificates
// covers the Find error specifically — distinct from the user-lookup error
// above, since it needs the user lookup to succeed first. Dropping just the
// certificates table (rather than closing the whole connection) makes only
// the second query fail.
func TestCertificateService_ShouldSurfaceAGenericDBErrorListingCertificates(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)
	seedUser(t, reqSvc.db, "sub-alice")

	if err := reqSvc.db.Migrator().DropTable(&model.Certificate{}); err != nil {
		t.Fatalf("failed to drop the certificates table: %v", err)
	}

	if _, _, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 25); err == nil {
		t.Error("ListForIdentity() error = nil, want error")
	}
}

// TestCertificateService_ShouldReturnNothingForAnIdentityWithNoUserRecord
// keeps a session that outlived its user row from erroring the history page:
// no user means no certificates, which is an empty list.
func TestCertificateService_ShouldReturnNothingForAnIdentityWithNoUserRecord(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	got, nextCursor, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-ghost"}, nil, 25)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d certificates, want none", len(got))
	}
	if nextCursor != nil {
		t.Errorf("got nextCursor = %v, want nil", nextCursor)
	}
}

// TestCertificateService_PaginationBasics tests basic cursor pagination:
// empty result, single page (no next cursor), and exact page boundary.
func TestCertificateService_PaginationBasics(t *testing.T) {
	tests := []struct {
		name           string
		totalCerts     int
		pageSize       int
		wantFirstPage  int
		wantNextCursor bool
	}{
		{
			name:           "empty result",
			totalCerts:     0,
			pageSize:       25,
			wantFirstPage:  0,
			wantNextCursor: false,
		},
		{
			name:           "single page (limit larger than results)",
			totalCerts:     10,
			pageSize:       25,
			wantFirstPage:  10,
			wantNextCursor: false,
		},
		{
			name:           "exact page boundary (results exactly equal to page size)",
			totalCerts:     25,
			pageSize:       25,
			wantFirstPage:  25,
			wantNextCursor: false,
		},
		{
			name:           "more results than first page",
			totalCerts:     26,
			pageSize:       25,
			wantFirstPage:  25,
			wantNextCursor: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reqSvc := newTestCertRequestService(t, time.Hour)
			svc := newTestCertificateService(t, reqSvc)

			userID := seedUser(t, reqSvc.db, "sub-alice")

			// Seed certificates with slightly different issued_at times to ensure stable ordering.
			now := time.Now()
			for i := 0; i < tt.totalCerts; i++ {
				seedCertificate(t, reqSvc, &userID, uint64(i), now.Add(-time.Duration(i)*time.Second))
			}

			got, nextCursor, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, tt.pageSize)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(got) != tt.wantFirstPage {
				t.Errorf("got %d certificates, want %d", len(got), tt.wantFirstPage)
			}

			hasNext := nextCursor != nil
			if hasNext != tt.wantNextCursor {
				t.Errorf("got nextCursor = %v, want nextCursor present = %v", nextCursor, tt.wantNextCursor)
			}
		})
	}
}

// TestCertificateService_CursorPagination tests that cursor-based pagination
// correctly returns the next page of results and maintains stable ordering.
func TestCertificateService_CursorPagination(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	userID := seedUser(t, reqSvc.db, "sub-alice")

	// Seed 10 certificates with slightly different issued_at times.
	now := time.Now()
	for i := 0; i < 10; i++ {
		seedCertificate(t, reqSvc, &userID, uint64(i), now.Add(-time.Duration(i)*time.Second))
	}

	// Get first page (5 results).
	page1, cursor1, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 5)
	if err != nil {
		t.Fatalf("unexpected error on page 1: %v", err)
	}
	if len(page1) != 5 {
		t.Fatalf("got %d results on page 1, want 5", len(page1))
	}
	if cursor1 == nil {
		t.Errorf("got nextCursor = nil on page 1, want a cursor")
	}

	// Verify page 1 is ordered newest first (serials 0-4, which have issued_at from most recent to least).
	for i, cd := range page1 {
		if cd.Certificate.SerialNumber != uint64(i) {
			t.Errorf("page 1 result %d has serial %d, want %d", i, cd.Certificate.SerialNumber, i)
		}
	}

	// Get second page using cursor.
	page2, cursor2, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, cursor1, 5)
	if err != nil {
		t.Fatalf("unexpected error on page 2: %v", err)
	}
	if len(page2) != 5 {
		t.Fatalf("got %d results on page 2, want 5", len(page2))
	}
	if cursor2 != nil {
		t.Errorf("got nextCursor = %v on page 2, want nil (last page)", cursor2)
	}

	// Verify page 2 is ordered newest first (serials 5-9).
	for i, cd := range page2 {
		expectedSerial := uint64(5 + i)
		if cd.Certificate.SerialNumber != expectedSerial {
			t.Errorf("page 2 result %d has serial %d, want %d", i, cd.Certificate.SerialNumber, expectedSerial)
		}
	}

	// Verify no overlap between pages.
	page1Serials := make(map[uint64]bool)
	for _, cd := range page1 {
		page1Serials[cd.Certificate.SerialNumber] = true
	}
	for _, cd := range page2 {
		if page1Serials[cd.Certificate.SerialNumber] {
			t.Errorf("certificate with serial %d appears on both pages", cd.Certificate.SerialNumber)
		}
	}
}

// TestCertificateService_CursorWithConcurrentIssuance tests that the seek
// predicate correctly handles multiple certificates with the same issued_at.
func TestCertificateService_CursorWithConcurrentIssuance(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	userID := seedUser(t, reqSvc.db, "sub-alice")

	// Seed 6 certificates: 3 with time T, 3 with time T-1.
	// This tests the (issued_at, id DESC) ordering when multiple certs share issued_at.
	now := time.Now()
	sharedTime1 := now
	sharedTime2 := now.Add(-time.Second)

	// The seeded rows are asserted on through the paged reads below, not
	// through the return values, so they are deliberately not collected.
	seedCertificate(t, reqSvc, &userID, 1, sharedTime1)
	seedCertificate(t, reqSvc, &userID, 2, sharedTime1)
	seedCertificate(t, reqSvc, &userID, 3, sharedTime1)
	seedCertificate(t, reqSvc, &userID, 4, sharedTime2)
	seedCertificate(t, reqSvc, &userID, 5, sharedTime2)
	seedCertificate(t, reqSvc, &userID, 6, sharedTime2)

	// Get first page (3 results) — should be certs at sharedTime1.
	page1, cursor1, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 3)
	if err != nil {
		t.Fatalf("unexpected error on page 1: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("got %d results on page 1, want 3", len(page1))
	}

	// Verify page 1 contains the newer certs (1, 2, 3).
	for _, cd := range page1 {
		if cd.Certificate.SerialNumber > 3 {
			t.Errorf("page 1 contains cert with serial %d, want only 1-3", cd.Certificate.SerialNumber)
		}
	}

	if cursor1 == nil {
		t.Errorf("got nextCursor = nil on page 1, want a cursor")
	}

	// Get second page using cursor — should be certs 4, 5, 6.
	page2, cursor2, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, cursor1, 3)
	if err != nil {
		t.Fatalf("unexpected error on page 2: %v", err)
	}
	if len(page2) != 3 {
		t.Fatalf("got %d results on page 2, want 3", len(page2))
	}
	if cursor2 != nil {
		t.Errorf("got nextCursor = %v on page 2, want nil (last page)", cursor2)
	}

	// Verify page 2 contains the older certs (4, 5, 6).
	for _, cd := range page2 {
		if cd.Certificate.SerialNumber < 4 {
			t.Errorf("page 2 contains cert with serial %d, want only 4-6", cd.Certificate.SerialNumber)
		}
	}
}

// TestCertificateService_InvalidCursor tests that a nonexistent or
// wrong-user cursor returns an error.
func TestCertificateService_InvalidCursor(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	userID := seedUser(t, reqSvc.db, "sub-alice")
	bobID := seedUser(t, reqSvc.db, "sub-bob")

	now := time.Now()
	aliceCert := seedCertificate(t, reqSvc, &userID, 1, now)
	bobCert := seedCertificate(t, reqSvc, &bobID, 2, now)

	tests := []struct {
		name    string
		cursor  string
		user    string
		wantErr bool
	}{
		{
			name:    "nonexistent cursor",
			cursor:  uuid.New().String(),
			user:    "sub-alice",
			wantErr: true,
		},
		{
			name:    "cursor belongs to different user",
			cursor:  bobCert.ID,
			user:    "sub-alice",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := svc.ListForIdentity(context.Background(), &Identity{Subject: tt.user}, &tt.cursor, 25)
			if (err != nil) != tt.wantErr {
				t.Errorf("got error = %v, want error = %v", err, tt.wantErr)
			}
		})
	}

	// Sanity check: alice's own certificate as cursor should work.
	_, _, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, &aliceCert.ID, 25)
	if err != nil {
		t.Errorf("got error with alice's own certificate as cursor: %v", err)
	}
}

// TestCertificateService_CertificateWithDecision tests that certificates
// with associated decision records are properly retrieved and the certificate
// ID is not clobbered by the decision ID (testing the SELECT column ambiguity trap).
func TestCertificateService_CertificateWithDecision(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	userID := seedUser(t, reqSvc.db, "sub-alice")

	now := time.Now()
	decision := &model.CertificateRequestDecision{
		Outcome:   model.CertificateRequestDecisionApproved,
		Subject:   "sub-approver",
		Username:  "approver",
		Email:     "approver@example.com",
		DecidedAt: now,
	}
	cert := seedCertificateWithRequest(t, reqSvc, &userID, 1, now, decision)

	got, _, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 25)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("got %d certificates, want 1", len(got))
	}

	// Verify the certificate ID is the certificate's ID, not the decision's.
	if got[0].Certificate.ID != cert.ID {
		t.Errorf("got certificate ID %s, want %s (decision ID clobbered it)", got[0].Certificate.ID, cert.ID)
	}

	// Verify decision is populated.
	if got[0].Decision == nil {
		t.Error("got Decision = nil, want populated decision")
	} else {
		if got[0].Decision.Subject != "sub-approver" {
			t.Errorf("got decision subject %s, want sub-approver", got[0].Decision.Subject)
		}
	}
}

// TestCertificateService_CertificateWithoutDecision tests that certificates
// without associated decision records (orphaned requests) are properly handled
// with nil decision pointer.
func TestCertificateService_CertificateWithoutDecision(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	userID := seedUser(t, reqSvc.db, "sub-alice")

	now := time.Now()
	// Seed certificate without a request.
	cert := seedCertificate(t, reqSvc, &userID, 1, now)

	got, _, err := svc.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 25)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("got %d certificates, want 1", len(got))
	}

	// Verify the certificate is present with its own ID intact.
	if got[0].Certificate.ID != cert.ID {
		t.Errorf("got certificate ID %s, want %s", got[0].Certificate.ID, cert.ID)
	}

	// Verify decision is nil.
	if got[0].Decision != nil {
		t.Errorf("got Decision = %+v, want nil", got[0].Decision)
	}
}

// The serial is the only thing a service certificate and the redemption
// that produced it share: EnrollmentService.Retrieve allocates it onto the
// retrieval row and sends the same value to the signer.
func TestCertificateService_ServiceCertificateRetrieval(t *testing.T) {
	t.Parallel()

	// seed inserts a service certificate and, unless skipRetrieval, the
	// retrieval row carrying its serial.
	seed := func(t *testing.T, skipRetrieval bool) (*CertificateService, string, string) {
		t.Helper()
		svc := newTestCertRequestService(t, time.Minute)
		certs := newTestCertificateService(t, svc)
		userID := seedUser(t, svc.db, "sub-alice")

		const serial = uint64(7777)
		cert := model.Certificate{
			ID: uuid.NewString(), Type: model.CertificateTypeService,
			UserID: &userID, SerialNumber: serial,
			IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		}
		if err := svc.db.Create(&cert).Error; err != nil {
			t.Fatalf("failed to seed certificate: %v", err)
		}

		enrollmentID := uuid.NewString()
		if !skipRetrieval {
			if err := svc.db.Create(&model.EnrollmentRetrieval{
				ID: uuid.NewString(), EnrollmentID: enrollmentID,
				SourceIP: "198.51.100.44", CertificateSerial: serial,
				RetrievedAt: time.Now(), Succeeded: true,
			}).Error; err != nil {
				t.Fatalf("failed to seed retrieval: %v", err)
			}
		}
		return certs, enrollmentID, "sub-alice"
	}

	t.Run("should report the address the certificate was retrieved from", func(t *testing.T) {
		t.Parallel()
		certs, _, subject := seed(t, false)

		rows, _, err := certs.ListForIdentity(context.Background(), &Identity{Subject: subject}, nil, 25)
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if rows[0].Retrieval == nil {
			t.Fatalf("expected a retrieval, got nil")
		}
		if rows[0].Retrieval.SourceIP != "198.51.100.44" {
			t.Errorf("got source IP %q, want %q", rows[0].Retrieval.SourceIP, "198.51.100.44")
		}
	})

	// The enrollment id is what lets the UI link a certificate back to the
	// code it came from.
	t.Run("should name the enrollment the certificate was redeemed from", func(t *testing.T) {
		t.Parallel()
		certs, enrollmentID, subject := seed(t, false)

		rows, _, err := certs.ListForIdentity(context.Background(), &Identity{Subject: subject}, nil, 25)
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if rows[0].Retrieval.EnrollmentID != enrollmentID {
			t.Errorf("got enrollment %q, want %q", rows[0].Retrieval.EnrollmentID, enrollmentID)
		}
	})

	t.Run("should leave the retrieval nil when no row carries the serial", func(t *testing.T) {
		t.Parallel()
		certs, _, subject := seed(t, true)

		rows, _, err := certs.ListForIdentity(context.Background(), &Identity{Subject: subject}, nil, 25)
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if rows[0].Retrieval != nil {
			t.Errorf("got retrieval %+v, want nil", rows[0].Retrieval)
		}
	})
}

// A user certificate has no redemption behind it at all, and the join must
// not invent one by colliding on a serial.
func TestCertificateService_ShouldLeaveANonServiceCertificateWithoutARetrieval(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Minute)
	certs := newTestCertificateService(t, svc)
	userID := seedUser(t, svc.db, "sub-alice")
	seedCertificate(t, svc, &userID, 4242, time.Now())

	rows, _, err := certs.ListForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 25)
	if err != nil {
		t.Fatalf("ListForIdentity() error = %v", err)
	}
	if rows[0].Retrieval != nil {
		t.Errorf("got retrieval %+v, want nil for a user certificate", rows[0].Retrieval)
	}
}

// TestCertificateService_GetByID_ApproverCanRead tests that the approver can read a certificate.
func TestCertificateService_GetByID_ApproverCanRead(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	certSvc := newTestCertificateService(t, svc)

	userID := seedUser(t, svc.db, "sub-alice")
	cert := seedCertificate(t, svc, &userID, 1000, time.Now())

	result, err := certSvc.GetByID(context.Background(), cert.ID, &Identity{Subject: "sub-alice"}, nil)
	if err != nil {
		t.Fatalf("approver should be able to read their certificate: %v", err)
	}

	if result.Certificate.ID != cert.ID {
		t.Errorf("got certificate ID %q, want %q", result.Certificate.ID, cert.ID)
	}
}

// TestCertificateService_GetByID_UnrelatedUserCannot tests that an unrelated user cannot read a certificate.
func TestCertificateService_GetByID_UnrelatedUserCannot(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	certSvc := newTestCertificateService(t, svc)

	userID := seedUser(t, svc.db, "sub-alice")
	_ = seedUser(t, svc.db, "sub-bob")
	cert := seedCertificate(t, svc, &userID, 1000, time.Now())

	// Bob tries to read Alice's certificate
	_, err := certSvc.GetByID(context.Background(), cert.ID, &Identity{Subject: "sub-bob"}, nil)
	if err == nil {
		t.Fatal("unrelated user should not be able to read certificate")
	}
	if err.Error() != "certificate not found" {
		t.Errorf("got error %q, want uniform 404", err)
	}
}

// TestCertificateService_GetByID_AuditorCanRead tests that an auditor can read any certificate.
func TestCertificateService_GetByID_AuditorCanRead(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	certSvc := newTestCertificateService(t, svc)

	userID := seedUser(t, svc.db, "sub-alice")
	cert := seedCertificate(t, svc, &userID, 1000, time.Now())

	cfg := &config.Config{
		Admin: config.AdminConfig{
			AuditorGroup: "auditors",
		},
	}

	result, err := certSvc.GetByID(context.Background(), cert.ID, &Identity{
		Subject: "sub-auditor",
		Groups:  []string{"auditors"},
	}, cfg)
	if err != nil {
		t.Fatalf("auditor should be able to read certificate: %v", err)
	}

	if result.Certificate.ID != cert.ID {
		t.Errorf("got certificate ID %q, want %q", result.Certificate.ID, cert.ID)
	}
}

// TestCertificateService_GetByID_AdminCanRead tests that an admin can read any certificate.
func TestCertificateService_GetByID_AdminCanRead(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	certSvc := newTestCertificateService(t, svc)

	userID := seedUser(t, svc.db, "sub-alice")
	cert := seedCertificate(t, svc, &userID, 1000, time.Now())

	cfg := &config.Config{
		Admin: config.AdminConfig{
			RequireGroup: "admins",
		},
	}

	result, err := certSvc.GetByID(context.Background(), cert.ID, &Identity{
		Subject: "sub-admin",
		Groups:  []string{"admins"},
	}, cfg)
	if err != nil {
		t.Fatalf("admin should be able to read certificate: %v", err)
	}

	if result.Certificate.ID != cert.ID {
		t.Errorf("got certificate ID %q, want %q", result.Certificate.ID, cert.ID)
	}
}

// TestCertificateService_GetByID_UnknownCertificate tests that an unknown certificate returns 404.
func TestCertificateService_GetByID_UnknownCertificate(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	certSvc := newTestCertificateService(t, svc)

	seedUser(t, svc.db, "sub-alice")

	_, err := certSvc.GetByID(context.Background(), "nonexistent", &Identity{Subject: "sub-alice"}, nil)
	if err == nil {
		t.Fatal("reading unknown certificate should fail")
	}
	if err.Error() != "certificate not found" {
		t.Errorf("got error %q, want NotFoundError", err)
	}
}

// TestCertificateService_GetByID_NullUserID tests that an auditor can read orphaned certificates.
func TestCertificateService_GetByID_NullUserID(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	certSvc := newTestCertificateService(t, svc)

	// Create a certificate with null UserID (orphaned)
	cert := seedCertificate(t, svc, nil, 1000, time.Now())

	cfg := &config.Config{
		Admin: config.AdminConfig{
			AuditorGroup: "auditors",
		},
	}

	// An auditor can read it
	result, err := certSvc.GetByID(context.Background(), cert.ID, &Identity{
		Subject: "sub-auditor",
		Groups:  []string{"auditors"},
	}, cfg)
	if err != nil {
		t.Fatalf("auditor should be able to read orphaned certificate: %v", err)
	}

	if result.Certificate.ID != cert.ID {
		t.Errorf("got certificate ID %q, want %q", result.Certificate.ID, cert.ID)
	}

	// An unrelated user cannot read it
	_, err = certSvc.GetByID(context.Background(), cert.ID, &Identity{
		Subject: "sub-alice",
	}, nil)
	if err == nil {
		t.Fatal("unrelated user should not be able to read orphaned certificate")
	}
}

// The two option columns are what the certificate actually grants, and the
// detail page is the only screen that shows them. GetByID selects an
// explicit column list rather than the whole row, so a column that is not
// named there arrives empty however faithfully it was written -- which is
// how these two reached the page as "None" on certificates that plainly
// carried permit-pty. A stubbed CertificateProvider cannot see that; this
// reads back through the real query.
func TestCertificateService_GetByID_ShouldReadTheIssuedOptions(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	certSvc := newTestCertificateService(t, svc)

	userID := seedUser(t, svc.db, "sub-alice")
	cert := seedCertificate(t, svc, &userID, 1001, time.Now())

	const (
		extensions      = `["permit-pty","permit-agent-forwarding"]`
		criticalOptions = `{"force-command":"/usr/bin/backup"}`
	)
	if err := svc.db.Model(&model.Certificate{}).
		Where("id = ?", cert.ID).
		Updates(map[string]any{"extensions": extensions, "critical_options": criticalOptions}).
		Error; err != nil {
		t.Fatalf("failed to set the option columns: %v", err)
	}

	result, err := certSvc.GetByID(context.Background(), cert.ID, &Identity{Subject: "sub-alice"}, nil)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}

	if result.Certificate.Extensions != extensions {
		t.Errorf("Extensions = %q, want %q", result.Certificate.Extensions, extensions)
	}
	if result.Certificate.CriticalOptions != criticalOptions {
		t.Errorf("CriticalOptions = %q, want %q", result.Certificate.CriticalOptions, criticalOptions)
	}
}
