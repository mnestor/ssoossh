package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/signer"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// newTestEnrollmentService wires an EnrollmentService onto svc's database
// and transport, migrating the tables Retrieve touches.
func newTestEnrollmentService(t *testing.T, svc *CertRequestService) *EnrollmentService {
	t.Helper()

	if err := svc.db.AutoMigrate(
		&model.Certificate{},
		&model.User{},
		&model.EnrollmentRetrieval{},
		&model.EnrollmentReassignment{},
	); err != nil {
		t.Fatalf("failed to migrate retrieval tables: %v", err)
	}
	enrollment, err := NewEnrollmentService(svc.config, svc.db, svc.publisher, svc.subscriber)
	if err != nil {
		t.Fatalf("NewEnrollmentService() error = %v", err)
	}
	return enrollment
}

// startTestPipeline runs the real signer and signed-reply listener on svc's
// transport, mirroring bootstrap.initPipeline the same way the pipeline
// end-to-end tests do.
func startTestPipeline(t *testing.T, svc *CertRequestService) ssh.PublicKey {
	t.Helper()

	caKeypair, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate CA keypair: %v", err)
	}
	caPEM, err := caKeypair.MarshalPrivateKey()
	if err != nil {
		t.Fatalf("failed to marshal CA private key: %v", err)
	}
	keys, err := signer.NewConfigKeySource(string(caPEM))
	if err != nil {
		t.Fatalf("failed to build key source: %v", err)
	}

	router, err := message.NewRouter(message.RouterConfig{CloseTimeout: time.Second},
		watermill.NewSlogLogger(slog.Default()))
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}
	signer.NewHandler(keys, svc.publisher, false, newDefaultTestSignerLimits()).Register(router, svc.subscriber)
	NewSignedReplyHandler(svc.db, svc).Register(router, svc.subscriber)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := router.Run(ctx); err != nil {
			t.Errorf("router stopped with an error: %v", err)
		}
	}()
	t.Cleanup(func() {
		cancel()
		if err := router.Close(); err != nil {
			t.Errorf("unexpected error closing router: %v", err)
		}
	})

	select {
	case <-router.Running():
	case <-time.After(5 * time.Second):
		t.Fatal("router did not start")
	}

	return caKeypair.Public()
}

// enrollService drives the real approval path — create request, approve as
// identity — and returns the enrollment code the approval minted.
func enrollService(t *testing.T, svc *CertRequestService, publicKey string) string {
	t.Helper()

	requestID, err := svc.createRequestID(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: publicKey,
	})
	if err != nil {
		t.Fatalf("unexpected error creating service request: %v", err)
	}

	seedUser(t, svc.db, "sub-svc")
	err = svc.Approve(context.Background(), requestID,
		&Identity{Username: "approver", Subject: "sub-svc", ServiceAccounts: []string{"svc-deploy"}},
		DecisionContext{}, ApprovalSelection{ServiceAccount: "svc-deploy"})
	if err != nil {
		t.Fatalf("unexpected error approving service request: %v", err)
	}

	var enrollment model.Enrollment
	if err := svc.db.First(&enrollment).Error; err != nil {
		t.Fatalf("expected an enrollment row, got error: %v", err)
	}
	return enrollment.Code
}

// should redeem a code end-to-end through the real signer: certificate
// signed by the CA for the enrolled key and approval-time principal, audit
// row linked to the approver, retrieval logged, redemption stamped.
func TestEnrollmentRetrieve_EndToEnd(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{
			ValidDuration:      time.Hour,
			EnrollmentDuration: 90 * 24 * time.Hour,
		},
		ClientTimeout: 50 * time.Second, // signing grace = 5s
	})
	enrollment := newTestEnrollmentService(t, svc)
	caPub := startTestPipeline(t, svc)

	clientKeypair, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate client keypair: %v", err)
	}
	clientPub, err := clientKeypair.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("failed to marshal client public key: %v", err)
	}
	code := enrollService(t, svc, clientPub)

	certText, err := enrollment.Retrieve(context.Background(), code, "203.0.113.7")
	if err != nil {
		t.Fatalf("Retrieve() error = %v", err)
	}

	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(certText))
	if err != nil {
		t.Fatalf("failed to parse the delivered certificate: %v", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		t.Fatalf("expected an *ssh.Certificate, got %T", pub)
	}

	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return string(auth.Marshal()) == string(caPub.Marshal())
		},
	}
	// The principal is the service account chosen at approval, not the
	// approver's own username.
	if err := checker.CheckCert("svc-deploy", cert); err != nil {
		t.Errorf("delivered certificate did not validate for the approval-time principal: %v", err)
	}
	if string(cert.Key.Marshal()) != string(clientKeypair.Public().Marshal()) {
		t.Error("delivered certificate is not bound to the enrolled public key")
	}

	// The certificate carries the lifetime fixed at approval, measured from
	// this redemption — not the code's own, much longer, expiry.
	var row model.Enrollment
	if err := svc.db.First(&row).Error; err != nil {
		t.Fatalf("failed to read back enrollment: %v", err)
	}
	assertUnixWithin(t, "certificate ValidBefore", cert.ValidBefore, time.Now().Add(time.Hour), time.Minute)
	//nolint:gosec // Unix timestamps are non-negative; conversion is safe.
	if int64(cert.ValidBefore) >= row.ExpiresAt.Unix() {
		t.Errorf("certificate ValidBefore %d is not shorter than the code's expiry %d",
			cert.ValidBefore, row.ExpiresAt.Unix())
	}
	if row.RedeemedAt == nil {
		t.Error("expected RedeemedAt to be stamped on first redemption")
	}

	// Audit: certificates row linked to the approving user and the original
	// request; retrieval row logged with source IP and marked succeeded.
	var audit model.Certificate
	if err := svc.db.First(&audit, "serial_number = ?", cert.Serial).Error; err != nil {
		t.Fatalf("expected a certificate audit row, got error: %v", err)
	}
	if audit.UserID == nil {
		t.Error("expected the audit row to be linked to the approving user")
	}
	if audit.CertificateRequestID == nil {
		t.Error("expected the audit row to be linked to the approved request")
	}
	var retrieval model.EnrollmentRetrieval
	if err := svc.db.First(&retrieval).Error; err != nil {
		t.Fatalf("expected a retrieval log row, got error: %v", err)
	}
	if retrieval.SourceIP != "203.0.113.7" {
		t.Errorf("retrieval source IP %q, want %q", retrieval.SourceIP, "203.0.113.7")
	}
	if !retrieval.Succeeded {
		t.Error("expected the retrieval row to be marked succeeded")
	}
	if retrieval.CertificateSerial != cert.Serial {
		t.Errorf("retrieval serial %d does not match issued serial %d", retrieval.CertificateSerial, cert.Serial)
	}
}

// should honor reusable codes: a second redemption issues a fresh
// certificate, logs a second retrieval, and leaves the first redemption
// stamp untouched.
func TestEnrollmentRetrieve_ShouldAllowRedeemingTwice(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{
			ValidDuration:      time.Hour,
			EnrollmentDuration: 90 * 24 * time.Hour,
		},
		ClientTimeout: 50 * time.Second, // signing grace = 5s
	})
	enrollment := newTestEnrollmentService(t, svc)
	startTestPipeline(t, svc)

	clientKeypair, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate client keypair: %v", err)
	}
	clientPub, err := clientKeypair.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("failed to marshal client public key: %v", err)
	}
	code := enrollService(t, svc, clientPub)

	if _, err := enrollment.Retrieve(context.Background(), code, "203.0.113.7"); err != nil {
		t.Fatalf("first Retrieve() error = %v", err)
	}
	var first model.Enrollment
	if err := svc.db.First(&first).Error; err != nil {
		t.Fatalf("failed to read back enrollment: %v", err)
	}

	if _, err := enrollment.Retrieve(context.Background(), code, "203.0.113.8"); err != nil {
		t.Fatalf("second Retrieve() error = %v", err)
	}

	var second model.Enrollment
	if err := svc.db.First(&second).Error; err != nil {
		t.Fatalf("failed to read back enrollment: %v", err)
	}
	if second.RedeemedAt == nil || !second.RedeemedAt.Equal(*first.RedeemedAt) {
		t.Error("expected RedeemedAt to keep its first-redemption stamp")
	}

	var retrievals []model.EnrollmentRetrieval
	if err := svc.db.Find(&retrievals).Error; err != nil {
		t.Fatalf("failed to list retrievals: %v", err)
	}
	if len(retrievals) != 2 {
		t.Fatalf("got %d retrieval rows, want 2", len(retrievals))
	}
	var audits []model.Certificate
	if err := svc.db.Find(&audits).Error; err != nil {
		t.Fatalf("failed to list certificate audit rows: %v", err)
	}
	if len(audits) != 2 {
		t.Errorf("got %d certificate audit rows, want 2", len(audits))
	}
}

// should answer an unknown code with not-found.
func TestEnrollmentRetrieve_ShouldRejectUnknownCode(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{})
	enrollment := newTestEnrollmentService(t, svc)

	_, err := enrollment.Retrieve(context.Background(), "no-such-code", "203.0.113.7")
	var notFound *errorresponses.NotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("Retrieve() error = %v, want NotFoundError", err)
	}
}

// should answer an expired code exactly like an unknown one.
func TestEnrollmentRetrieve_ShouldRejectExpiredCode(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{})
	enrollment := newTestEnrollmentService(t, svc)

	principals, _ := json.Marshal([]string{"approver"})
	seedEnrollment(t, svc, model.Enrollment{
		Code:       "expired-code",
		Principals: string(principals),
		ExpiresAt:  time.Now().Add(-time.Minute),
	})

	_, err := enrollment.Retrieve(context.Background(), "expired-code", "203.0.113.7")
	var notFound *errorresponses.NotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("Retrieve() error = %v, want NotFoundError", err)
	}
}

// should refuse an enrollment that predates approval-time principals rather
// than signing something policy never fixed.
func TestEnrollmentRetrieve_ShouldRejectEnrollmentWithoutPrincipals(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{})
	enrollment := newTestEnrollmentService(t, svc)

	seedEnrollment(t, svc, model.Enrollment{
		Code:      "legacy-code",
		ExpiresAt: time.Now().Add(time.Hour),
	})

	if _, err := enrollment.Retrieve(context.Background(), "legacy-code", "203.0.113.7"); err == nil {
		t.Error("Retrieve() error = nil, want error for enrollment without principals")
	}
}

// should surface a signing failure as a terminal error and leave the
// retrieval row unmarked.
func TestEnrollmentRetrieve_ShouldReportSigningFailure(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		ClientTimeout: 50 * time.Second, // signing grace = 5s
	})
	enrollment := newTestEnrollmentService(t, svc)
	startTestPipeline(t, svc)

	principals, _ := json.Marshal([]string{"approver"})
	seedEnrollment(t, svc, model.Enrollment{
		Code:       "bad-key-code",
		PublicKey:  "not a public key",
		Principals: string(principals),
		ExpiresAt:  time.Now().Add(time.Hour),
	})

	if _, err := enrollment.Retrieve(context.Background(), "bad-key-code", "203.0.113.7"); err == nil {
		t.Fatal("Retrieve() error = nil, want signing failure")
	}

	var retrieval model.EnrollmentRetrieval
	if err := svc.db.First(&retrieval).Error; err != nil {
		t.Fatalf("expected a retrieval log row for the failed attempt, got error: %v", err)
	}
	if retrieval.Succeeded {
		t.Error("expected the failed retrieval row to stay unmarked")
	}
}

// should give up after the signing timeout when no signer answers.
func TestEnrollmentRetrieve_ShouldTimeOutWithoutSigner(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		ClientTimeout: time.Second, // signing grace = 100ms
	})
	enrollment := newTestEnrollmentService(t, svc)

	principals, _ := json.Marshal([]string{"approver"})
	seedEnrollment(t, svc, model.Enrollment{
		Code:       "orphan-code",
		PublicKey:  "ssh-ed25519 AAAA orphan",
		Principals: string(principals),
		ExpiresAt:  time.Now().Add(time.Hour),
	})

	if _, err := enrollment.Retrieve(context.Background(), "orphan-code", "203.0.113.7"); err == nil {
		t.Error("Retrieve() error = nil, want timeout error")
	}
}

// should require the approver to choose one of their own service accounts,
// store it on the request, and make it the enrollment principal.
func TestApprove_ServiceAccountLinkage(t *testing.T) {
	t.Parallel()

	newRequest := func(t *testing.T, svc *CertRequestService) string {
		t.Helper()
		requestID, err := svc.createRequestID(context.Background(), NewCertRequestParams{
			Type:      model.CertificateTypeService,
			PublicKey: "ssh-ed25519 AAAA... svc",
		})
		if err != nil {
			t.Fatalf("unexpected error creating request: %v", err)
		}
		return requestID
	}

	t.Run("should refuse approval without a service account", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		requestID := newRequest(t, svc)
		identity := &Identity{Username: "alice", Subject: "sub-1", ServiceAccounts: []string{"svc-a"}}
		seedUser(t, svc.db, identity.Subject)

		if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}, ApprovalSelection{}); err == nil {
			t.Fatal("expected an error approving without a service account")
		}
	})

	t.Run("should refuse a service account the approver is not associated with", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		requestID := newRequest(t, svc)
		identity := &Identity{Username: "alice", Subject: "sub-1", ServiceAccounts: []string{"svc-a"}}
		seedUser(t, svc.db, identity.Subject)

		err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}, ApprovalSelection{ServiceAccount: "svc-b"})
		if err == nil {
			t.Fatal("expected an error for a service account outside the approver's own")
		}
		var req model.CertificateRequest
		if dbErr := svc.db.First(&req, "id = ?", requestID).Error; dbErr != nil {
			t.Fatalf("failed to read back request: %v", dbErr)
		}
		if req.Status != model.CertificateRequestStatusPending {
			t.Errorf("expected the request to remain pending, got %q", req.Status)
		}
	})

	t.Run("should store the account on the request and make it the principal", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		requestID := newRequest(t, svc)
		identity := &Identity{Username: "alice", Subject: "sub-1", ServiceAccounts: []string{"svc-a", "svc-b"}}
		seedUser(t, svc.db, identity.Subject)

		if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}, ApprovalSelection{ServiceAccount: "svc-b"}); err != nil {
			t.Fatalf("unexpected error approving: %v", err)
		}

		var req model.CertificateRequest
		if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
			t.Fatalf("failed to read back request: %v", err)
		}
		if req.ServiceAccount != "svc-b" {
			t.Errorf("got stored service account %q, want %q", req.ServiceAccount, ApprovalSelection{ServiceAccount: "svc-b"})
		}

		var enrollment model.Enrollment
		if err := svc.db.First(&enrollment).Error; err != nil {
			t.Fatalf("failed to read back enrollment: %v", err)
		}
		var principals []string
		if err := json.Unmarshal([]byte(enrollment.Principals), &principals); err != nil {
			t.Fatalf("failed to decode enrollment principals: %v", err)
		}
		if len(principals) != 1 || principals[0] != "svc-b" {
			t.Errorf("got enrollment principals %v, want [svc-b]", principals)
		}
	})
}

// should scope the retrieval log to the approving user and auditors.
func TestListRetrievals_Authorization(t *testing.T) {
	t.Parallel()

	auditorCfg := &config.Config{Admin: config.AdminConfig{AuditorGroup: "auditors"}}

	setup := func(t *testing.T) (*CertRequestService, *EnrollmentService, string, string) {
		t.Helper()
		svc := newTestCertRequestServiceWithConfig(t, auditorCfg)
		enrollment := newTestEnrollmentService(t, svc)

		requestID := uuid.NewString()
		approverID := seedUser(t, svc.db, "sub-approver")
		if err := svc.db.Create(&model.CertificateRequest{
			ID: requestID, Type: model.CertificateTypeService,
			PublicKey: "ssh-ed25519 AAAA...", UserID: &approverID,
			Status: model.CertificateRequestStatusEnrolled, CreatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatalf("failed to seed request: %v", err)
		}
		enrollmentID := uuid.NewString()
		if err := svc.db.Create(&model.Enrollment{
			ID: enrollmentID, Code: "code-" + enrollmentID, PublicKey: "k",
			OptionSet: "{}", Principals: `["svc-a"]`, ServiceAccount: "svc-a", UserID: approverID,
			CertificateRequestID: &requestID,
			CreatedAt:            time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		}).Error; err != nil {
			t.Fatalf("failed to seed enrollment: %v", err)
		}
		if err := svc.db.Create(&model.EnrollmentRetrieval{
			ID: uuid.NewString(), EnrollmentID: enrollmentID,
			SourceIP: "203.0.113.9", CertificateSerial: 42,
			RetrievedAt: time.Now(), Succeeded: true,
		}).Error; err != nil {
			t.Fatalf("failed to seed retrieval: %v", err)
		}
		return svc, enrollment, requestID, approverID
	}

	t.Run("should allow a holder of the service account", func(t *testing.T) {
		t.Parallel()
		_, enrollment, requestID, _ := setup(t)

		log, err := enrollment.ListRetrievals(context.Background(),
			requestID, &Identity{Subject: "sub-approver", ServiceAccounts: []string{"svc-a"}})
		if err != nil {
			t.Fatalf("ListRetrievals() error = %v", err)
		}
		if len(log.Retrievals) != 1 || log.Retrievals[0].SourceIP != "203.0.113.9" {
			t.Errorf("got %v, want the seeded retrieval", log.Retrievals)
		}
	})

	t.Run("should allow an auditor who is not the approver", func(t *testing.T) {
		t.Parallel()
		svc, enrollment, requestID, _ := setup(t)
		seedUser(t, svc.db, "sub-auditor")

		log, err := enrollment.ListRetrievals(context.Background(),
			requestID, &Identity{Subject: "sub-auditor", Groups: []string{"auditors"}})
		if err != nil {
			t.Fatalf("ListRetrievals() error = %v", err)
		}
		if len(log.Retrievals) != 1 {
			t.Errorf("got %d rows, want 1", len(log.Retrievals))
		}
	})

	t.Run("should refuse anyone else", func(t *testing.T) {
		t.Parallel()
		svc, enrollment, requestID, _ := setup(t)
		seedUser(t, svc.db, "sub-other")

		_, err := enrollment.ListRetrievals(context.Background(),
			requestID, &Identity{Subject: "sub-other"})
		var forbidden *errorresponses.ForbiddenError
		if !errors.As(err, &forbidden) {
			t.Errorf("ListRetrievals() error = %v, want ForbiddenError", err)
		}
	})

	t.Run("should answer not-found for a request without an enrollment", func(t *testing.T) {
		t.Parallel()
		_, enrollment, _, _ := setup(t)

		_, err := enrollment.ListRetrievals(context.Background(),
			uuid.NewString(), &Identity{Subject: "sub-approver"})
		var notFound *errorresponses.NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("ListRetrievals() error = %v, want NotFoundError", err)
		}
	})
}

// TestListForIdentity covers the read behind the web UI's service-codes
// page: which rows an identity sees, what is decoded onto them, and what a
// row with no retrievals reports.
func TestListForIdentity(t *testing.T) {
	t.Parallel()

	// setup returns the services plus the two seeded identities, so each
	// subtest can assert the scoping boundary between them.
	setup := func(t *testing.T) (*CertRequestService, *EnrollmentService, string, string) {
		t.Helper()
		svc := newTestCertRequestServiceWithConfig(t, &config.Config{})
		enrollment := newTestEnrollmentService(t, svc)
		mine := seedUser(t, svc.db, "sub-mine")
		theirs := seedUser(t, svc.db, "sub-theirs")
		return svc, enrollment, mine, theirs
	}

	t.Run("should return only the caller's own enrollments", func(t *testing.T) {
		t.Parallel()
		svc, enrollment, mine, theirs := setup(t)
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-mine", PublicKey: "k", Principals: `["svc-mine"]`, ServiceAccount: "svc-mine",
			UserID: mine, ExpiresAt: time.Now().Add(time.Hour),
		})
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-theirs", PublicKey: "k", Principals: `["svc-theirs"]`, ServiceAccount: "svc-theirs",
			UserID: theirs, ExpiresAt: time.Now().Add(time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(), &Identity{Subject: "sub-mine", ServiceAccounts: []string{"svc-mine"}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d enrollments, want 1", len(rows))
		}
		if rows[0].Principals[0] != "svc-mine" {
			t.Errorf("got principal %q, want %q", rows[0].Principals[0], "svc-mine")
		}
	})

	t.Run("should order newest first", func(t *testing.T) {
		t.Parallel()
		svc, enrollment, mine, _ := setup(t)
		now := time.Now()
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-old", PublicKey: "k", Principals: `["svc-old"]`, ServiceAccount: "svc-old", UserID: mine,
			CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(time.Hour),
		})
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-new", PublicKey: "k", Principals: `["svc-new"]`, ServiceAccount: "svc-new", UserID: mine,
			CreatedAt: now, ExpiresAt: now.Add(time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(), &Identity{Subject: "sub-mine", ServiceAccounts: []string{"svc-new", "svc-old"}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("got %d enrollments, want 2", len(rows))
		}
		if rows[0].Principals[0] != "svc-new" {
			t.Errorf("got %q first, want the newest enrollment", rows[0].Principals[0])
		}
	})

	// A code that has stopped working is exactly what the approver needs to
	// see to decide whether the job behind it still needs one.
	t.Run("should include expired enrollments", func(t *testing.T) {
		t.Parallel()
		svc, enrollment, mine, _ := setup(t)
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-dead", PublicKey: "k", Principals: `["svc-dead"]`, ServiceAccount: "svc-dead",
			UserID: mine, ExpiresAt: time.Now().Add(-time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(), &Identity{Subject: "sub-mine", ServiceAccounts: []string{"svc-dead"}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if len(rows) != 1 {
			t.Errorf("got %d enrollments, want the expired one included", len(rows))
		}
	})

	t.Run("should summarize the retrieval log", func(t *testing.T) {
		t.Parallel()
		svc, enrollment, mine, _ := setup(t)
		enrollmentID := uuid.NewString()
		seedEnrollment(t, svc, model.Enrollment{
			ID: enrollmentID, Code: "code-used", PublicKey: "k",
			Principals: `["svc-used"]`, ServiceAccount: "svc-used", UserID: mine, ExpiresAt: time.Now().Add(time.Hour),
		})
		newest := time.Now().Truncate(time.Second)
		for i, at := range []time.Time{newest.Add(-2 * time.Hour), newest.Add(-time.Hour), newest} {
			if err := svc.db.Create(&model.EnrollmentRetrieval{
				ID: uuid.NewString(), EnrollmentID: enrollmentID,
				SourceIP: "203.0.113.9", CertificateSerial: uint64(i + 1),
				RetrievedAt: at, Succeeded: true,
			}).Error; err != nil {
				t.Fatalf("failed to seed retrieval: %v", err)
			}
		}

		rows, err := enrollment.ListForIdentity(context.Background(), &Identity{Subject: "sub-mine", ServiceAccounts: []string{"svc-used"}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if rows[0].RetrievalCount != 3 {
			t.Errorf("got retrieval count %d, want 3", rows[0].RetrievalCount)
		}
		if rows[0].LastRetrievedAt == nil {
			t.Fatalf("expected a last-retrieved timestamp")
		}
		if !rows[0].LastRetrievedAt.UTC().Equal(newest.UTC()) {
			t.Errorf("got last retrieval %v, want %v", rows[0].LastRetrievedAt.UTC(), newest.UTC())
		}
	})

	t.Run("should report a never-redeemed code as unused", func(t *testing.T) {
		t.Parallel()
		svc, enrollment, mine, _ := setup(t)
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-fresh", PublicKey: "k", Principals: `["svc-fresh"]`, ServiceAccount: "svc-fresh",
			UserID: mine, ExpiresAt: time.Now().Add(time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(), &Identity{Subject: "sub-mine", ServiceAccounts: []string{"svc-fresh"}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if rows[0].RetrievalCount != 0 {
			t.Errorf("got retrieval count %d, want 0", rows[0].RetrievalCount)
		}
		if rows[0].LastRetrievedAt != nil {
			t.Errorf("got last retrieval %v, want nil", rows[0].LastRetrievedAt)
		}
	})

	t.Run("should decode the stored option set", func(t *testing.T) {
		t.Parallel()
		svc, enrollment, mine, _ := setup(t)
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-opts", PublicKey: "k", Principals: `["svc-opts"]`, ServiceAccount: "svc-opts", UserID: mine,
			OptionSet: `{"extensions":["permit-pty"],"force_command":"/usr/bin/true"}`,
			ExpiresAt: time.Now().Add(time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(), &Identity{Subject: "sub-mine", ServiceAccounts: []string{"svc-opts"}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if rows[0].Options.ForceCommand != "/usr/bin/true" {
			t.Errorf("got force command %q, want %q", rows[0].Options.ForceCommand, "/usr/bin/true")
		}
	})

	t.Run("should fingerprint the bound public key", func(t *testing.T) {
		t.Parallel()
		svc, enrollment, mine, _ := setup(t)
		kp, err := keypair.NewEd25519KeyPair()
		if err != nil {
			t.Fatalf("failed to generate keypair: %v", err)
		}
		authorizedKey, err := kp.MarshalAuthorizedKey()
		if err != nil {
			t.Fatalf("failed to marshal public key: %v", err)
		}
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-fp", PublicKey: authorizedKey, Principals: `["svc-fp"]`, ServiceAccount: "svc-fp",
			UserID: mine, ExpiresAt: time.Now().Add(time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(), &Identity{Subject: "sub-mine", ServiceAccounts: []string{"svc-fp"}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		parsed, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorizedKey))
		if err != nil {
			t.Fatalf("failed to parse the seeded key: %v", err)
		}
		if rows[0].Fingerprint != ssh.FingerprintSHA256(parsed) {
			t.Errorf("got fingerprint %q, want %q", rows[0].Fingerprint, ssh.FingerprintSHA256(parsed))
		}
	})

	// One unreadable column is not a reason to withhold the dates beside
	// it: the page exists to say when a code was approved and when it dies.
	// The account column is what ownership reads, so a row whose display
	// JSON is corrupt is still findable by the people who hold it.
	t.Run("should still return a row whose stored JSON does not parse", func(t *testing.T) {
		t.Parallel()
		svc, enrollment, mine, _ := setup(t)
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-broken", PublicKey: "not-a-key", Principals: "{{{",
			ServiceAccount: "svc-broken",
			OptionSet:      "{{{", UserID: mine, ExpiresAt: time.Now().Add(time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(),
			&Identity{Subject: "sub-mine", ServiceAccounts: []string{"svc-broken"}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d enrollments, want 1", len(rows))
		}
		if len(rows[0].Principals) != 0 || rows[0].Fingerprint != "" {
			t.Errorf("expected the unreadable fields empty, got %+v", rows[0])
		}
		if rows[0].Enrollment.ExpiresAt.IsZero() {
			t.Errorf("expected the readable fields intact, got a zero expiry")
		}
	})

	t.Run("should return an empty list for an identity with no users row", func(t *testing.T) {
		t.Parallel()
		_, enrollment, _, _ := setup(t)

		rows, err := enrollment.ListForIdentity(context.Background(), &Identity{Subject: "sub-unknown"})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d enrollments, want none", len(rows))
		}
	})
}

// A reusable code redeemed from cron accumulates thousands of rows over its
// life. The panel that reads them wants the recent end and a count, not a
// year of history in one response.
func TestListRetrievals_Truncation(t *testing.T) {
	t.Parallel()

	// seedLog builds an enrollment with count redemptions, the newest last,
	// and returns the request id the log is fetched by.
	seedLog := func(t *testing.T, count int) (*EnrollmentService, string) {
		t.Helper()
		svc := newTestCertRequestServiceWithConfig(t, &config.Config{})
		enrollment := newTestEnrollmentService(t, svc)

		requestID := uuid.NewString()
		approverID := seedUser(t, svc.db, "sub-approver")
		if err := svc.db.Create(&model.CertificateRequest{
			ID: requestID, Type: model.CertificateTypeService,
			PublicKey: "ssh-ed25519 AAAA...", UserID: &approverID,
			Status: model.CertificateRequestStatusEnrolled, CreatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatalf("failed to seed request: %v", err)
		}
		enrollmentID := uuid.NewString()
		seedEnrollment(t, svc, model.Enrollment{
			ID: enrollmentID, Code: "code-" + enrollmentID, PublicKey: "k",
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", UserID: approverID,
			CertificateRequestID: &requestID, ExpiresAt: time.Now().Add(time.Hour),
		})

		base := time.Now().Add(-time.Duration(count) * time.Minute).Truncate(time.Second)
		for i := range count {
			if err := svc.db.Create(&model.EnrollmentRetrieval{
				ID: uuid.NewString(), EnrollmentID: enrollmentID,
				SourceIP: "203.0.113.9", CertificateSerial: uint64(i + 1),
				RetrievedAt: base.Add(time.Duration(i) * time.Minute), Succeeded: true,
			}).Error; err != nil {
				t.Fatalf("failed to seed retrieval: %v", err)
			}
		}
		return enrollment, requestID
	}

	t.Run("should cap the page at the retrieval page size", func(t *testing.T) {
		t.Parallel()
		enrollment, requestID := seedLog(t, RetrievalPageSize+25)

		log, err := enrollment.ListRetrievals(context.Background(),
			requestID, &Identity{Subject: "sub-approver", ServiceAccounts: []string{"svc-a"}})
		if err != nil {
			t.Fatalf("ListRetrievals() error = %v", err)
		}
		if len(log.Retrievals) != RetrievalPageSize {
			t.Errorf("got %d rows, want %d", len(log.Retrievals), RetrievalPageSize)
		}
	})

	// A full page says only "at least this many", so the count has to be its
	// own query — it is the difference between the two that gets rendered.
	t.Run("should report the untruncated total", func(t *testing.T) {
		t.Parallel()
		enrollment, requestID := seedLog(t, RetrievalPageSize+25)

		log, err := enrollment.ListRetrievals(context.Background(),
			requestID, &Identity{Subject: "sub-approver", ServiceAccounts: []string{"svc-a"}})
		if err != nil {
			t.Fatalf("ListRetrievals() error = %v", err)
		}
		if log.Total != RetrievalPageSize+25 {
			t.Errorf("got total %d, want %d", log.Total, RetrievalPageSize+25)
		}
	})

	t.Run("should return the newest redemptions rather than the oldest", func(t *testing.T) {
		t.Parallel()
		enrollment, requestID := seedLog(t, RetrievalPageSize+25)

		log, err := enrollment.ListRetrievals(context.Background(),
			requestID, &Identity{Subject: "sub-approver", ServiceAccounts: []string{"svc-a"}})
		if err != nil {
			t.Fatalf("ListRetrievals() error = %v", err)
		}
		// Serials ascend with time in the fixture, so the newest row carries
		// the highest one.
		if log.Retrievals[0].CertificateSerial != uint64(RetrievalPageSize+25) {
			t.Errorf("got serial %d first, want the newest redemption",
				log.Retrievals[0].CertificateSerial)
		}
	})

	t.Run("should report a total matching the page when nothing is truncated", func(t *testing.T) {
		t.Parallel()
		enrollment, requestID := seedLog(t, 3)

		log, err := enrollment.ListRetrievals(context.Background(),
			requestID, &Identity{Subject: "sub-approver", ServiceAccounts: []string{"svc-a"}})
		if err != nil {
			t.Fatalf("ListRetrievals() error = %v", err)
		}
		if log.Total != 3 || len(log.Retrievals) != 3 {
			t.Errorf("got %d of %d, want 3 of 3", len(log.Retrievals), log.Total)
		}
	})
}

// seedEnrollment inserts row with generated ID and approver linkage
// defaults filled in.
func seedEnrollment(t *testing.T, svc *CertRequestService, row model.Enrollment) {
	t.Helper()

	if row.ID == "" {
		row.ID = uuid.NewString()
	}
	if row.OptionSet == "" {
		row.OptionSet = "{}"
	}
	if row.UserID == "" {
		row.UserID = seedUser(t, svc.db, "sub-seed-"+row.ID)
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	if err := svc.db.Create(&row).Error; err != nil {
		t.Fatalf("failed to seed enrollment: %v", err)
	}
}

// TestListForAdmin tests the admin list of enrollments across all users.
func TestListForAdmin(t *testing.T) {
	t.Parallel()

	t.Run("should list all enrollments newest first to an auditor", func(t *testing.T) {
		t.Parallel()
		auditorCfg := &config.Config{Admin: config.AdminConfig{AuditorGroup: "auditors"}}
		svc := newTestCertRequestServiceWithConfig(t, auditorCfg)
		enrollment := newTestEnrollmentService(t, svc)

		// Seed two enrollments by different users, with the second created later
		user1ID := seedUser(t, svc.db, "sub-user1")
		user2ID := seedUser(t, svc.db, "sub-user2")

		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: user1ID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1", ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})
		time.Sleep(10 * time.Millisecond) // Ensure creation order is obvious
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment2", Code: "code2", PublicKey: "key2", UserID: user2ID,
			Principals: `["svc-b"]`, ServiceAccount: "svc-b", KeyID: "key2", ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})

		// Migrate user models so we can load them
		if err := svc.db.AutoMigrate(&model.User{}); err != nil {
			t.Fatalf("failed to migrate users: %v", err)
		}

		list, err := enrollment.ListForAdmin(context.Background(),
			&Identity{Subject: "sub-auditor", Groups: []string{"auditors"}},
			AdminListParams{Limit: 25, Offset: 0, Query: ""})
		if err != nil {
			t.Fatalf("ListForAdmin() error = %v", err)
		}

		if len(list.Enrollments) != 2 || list.Total != 2 {
			t.Errorf("got %d of %d, want 2 of 2", len(list.Enrollments), list.Total)
		}
		if list.Enrollments[0].Enrollment.ID != "enrollment2" {
			t.Errorf("first enrollment is %q, want newest (enrollment2)", list.Enrollments[0].Enrollment.ID)
		}
	})

	t.Run("should search enrollments by approver username", func(t *testing.T) {
		t.Parallel()
		auditorCfg := &config.Config{Admin: config.AdminConfig{AuditorGroup: "auditors"}}
		svc := newTestCertRequestServiceWithConfig(t, auditorCfg)
		enrollment := newTestEnrollmentService(t, svc)

		// Seed users with specific usernames
		if err := svc.db.AutoMigrate(&model.User{}); err != nil {
			t.Fatalf("failed to migrate users: %v", err)
		}
		user1 := model.User{
			ID: uuid.NewString(), Subject: "sub-alice", Username: "alice", Email: "alice@example.com",
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		user2 := model.User{
			ID: uuid.NewString(), Subject: "sub-bob", Username: "bob", Email: "bob@example.com",
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}
		if err := svc.db.Create(&user1).Error; err != nil {
			t.Fatalf("failed to seed user1: %v", err)
		}
		if err := svc.db.Create(&user2).Error; err != nil {
			t.Fatalf("failed to seed user2: %v", err)
		}

		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: user1.ID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1", ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment2", Code: "code2", PublicKey: "key2", UserID: user2.ID,
			Principals: `["svc-b"]`, ServiceAccount: "svc-b", KeyID: "key2", ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})

		// Search for alice
		list, err := enrollment.ListForAdmin(context.Background(),
			&Identity{Subject: "sub-auditor", Groups: []string{"auditors"}},
			AdminListParams{Limit: 25, Offset: 0, Query: "alice"})
		if err != nil {
			t.Fatalf("ListForAdmin() error = %v", err)
		}

		if len(list.Enrollments) != 1 || list.Enrollments[0].Enrollment.ID != "enrollment1" {
			t.Errorf("search for 'alice' got %d enrollments, want 1 (enrollment1)", len(list.Enrollments))
		}
	})

	t.Run("should refuse non-auditors", func(t *testing.T) {
		t.Parallel()
		auditorCfg := &config.Config{Admin: config.AdminConfig{AuditorGroup: "auditors"}}
		svc := newTestCertRequestServiceWithConfig(t, auditorCfg)
		enrollment := newTestEnrollmentService(t, svc)

		_, err := enrollment.ListForAdmin(context.Background(),
			&Identity{Subject: "sub-user", Groups: []string{}},
			AdminListParams{Limit: 25, Offset: 0, Query: ""})

		var forbidden *errorresponses.ForbiddenError
		if !errors.As(err, &forbidden) {
			t.Errorf("ListForAdmin() error = %v, want ForbiddenError", err)
		}
	})

	t.Run("should return empty list for no matches", func(t *testing.T) {
		t.Parallel()
		auditorCfg := &config.Config{Admin: config.AdminConfig{AuditorGroup: "auditors"}}
		svc := newTestCertRequestServiceWithConfig(t, auditorCfg)
		enrollment := newTestEnrollmentService(t, svc)

		// Seed one enrollment
		user1ID := seedUser(t, svc.db, "sub-user1")
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: user1ID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1", ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})

		if err := svc.db.AutoMigrate(&model.User{}); err != nil {
			t.Fatalf("failed to migrate users: %v", err)
		}

		// Search for something that doesn't match
		list, err := enrollment.ListForAdmin(context.Background(),
			&Identity{Subject: "sub-auditor", Groups: []string{"auditors"}},
			AdminListParams{Limit: 25, Offset: 0, Query: "nonexistent"})
		if err != nil {
			t.Fatalf("ListForAdmin() error = %v", err)
		}

		if len(list.Enrollments) != 0 {
			t.Errorf("got %d enrollments for non-matching search, want 0", len(list.Enrollments))
		}
	})
}

// TestGetEnrollmentDetail tests retrieval of a single enrollment with full details.
func TestGetEnrollmentDetail(t *testing.T) {
	t.Parallel()

	t.Run("should allow a holder of the service account to view details", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)

		ownerID := seedUser(t, svc.db, "sub-owner")
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1", ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})

		detail, err := enrollment.GetEnrollmentDetail(context.Background(),
			"enrollment1", &Identity{Subject: "sub-owner", ServiceAccounts: []string{"svc-a"}})
		if err != nil {
			t.Fatalf("GetEnrollmentDetail() error = %v", err)
		}

		if detail.Enrollment.ID != "enrollment1" {
			t.Errorf("got enrollment %q, want enrollment1", detail.Enrollment.ID)
		}
	})

	t.Run("should allow an auditor to view details", func(t *testing.T) {
		t.Parallel()
		auditorCfg := &config.Config{Admin: config.AdminConfig{AuditorGroup: "auditors"}}
		svc := newTestCertRequestServiceWithConfig(t, auditorCfg)
		enrollment := newTestEnrollmentService(t, svc)

		ownerID := seedUser(t, svc.db, "sub-owner")
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1", ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})

		detail, err := enrollment.GetEnrollmentDetail(context.Background(),
			"enrollment1", &Identity{Subject: "sub-auditor", Groups: []string{"auditors"}})
		if err != nil {
			t.Fatalf("GetEnrollmentDetail() error = %v", err)
		}

		if detail.Enrollment.ID != "enrollment1" {
			t.Errorf("got enrollment %q, want enrollment1", detail.Enrollment.ID)
		}
	})

	t.Run("should refuse an unrelated user", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)

		ownerID := seedUser(t, svc.db, "sub-owner")
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1", ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})

		_, err := enrollment.GetEnrollmentDetail(context.Background(),
			"enrollment1", &Identity{Subject: "sub-other"})

		var forbidden *errorresponses.ForbiddenError
		if !errors.As(err, &forbidden) {
			t.Errorf("GetEnrollmentDetail() error = %v, want ForbiddenError", err)
		}
	})

	t.Run("should return NotFound for unknown enrollment", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)

		_, err := enrollment.GetEnrollmentDetail(context.Background(),
			"unknown-id", &Identity{Subject: "sub-auditor"})

		var notFound *errorresponses.NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("GetEnrollmentDetail() error = %v, want NotFoundError", err)
		}
	})
}

// TestListForAdmin_PagingWithSearch tests that search applies server-side and returns correct totals.
func TestListForAdmin_PagingWithSearch(t *testing.T) {
	t.Parallel()

	auditorCfg := &config.Config{Admin: config.AdminConfig{AuditorGroup: "auditors"}}
	svc := newTestCertRequestServiceWithConfig(t, auditorCfg)
	enrollment := newTestEnrollmentService(t, svc)

	if err := svc.db.AutoMigrate(&model.User{}); err != nil {
		t.Fatalf("failed to migrate users: %v", err)
	}

	// Create two users
	alice := model.User{
		ID:        uuid.NewString(),
		Subject:   "sub-alice",
		Username:  "alice",
		Email:     "alice@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	bob := model.User{
		ID:        uuid.NewString(),
		Subject:   "sub-bob",
		Username:  "bob",
		Email:     "bob@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := svc.db.Create(&alice).Error; err != nil {
		t.Fatalf("failed to seed alice: %v", err)
	}
	if err := svc.db.Create(&bob).Error; err != nil {
		t.Fatalf("failed to seed bob: %v", err)
	}

	// Seed 10 enrollments for alice
	for i := 0; i < 10; i++ {
		seedEnrollment(t, svc, model.Enrollment{
			ID:         uuid.NewString(),
			Code:       uuid.NewString(),
			PublicKey:  "key1",
			UserID:     alice.ID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a",
			KeyID:     "key1",
			ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})
	}

	// Seed 20 enrollments for bob
	for i := 0; i < 20; i++ {
		seedEnrollment(t, svc, model.Enrollment{
			ID:         uuid.NewString(),
			Code:       uuid.NewString(),
			PublicKey:  "key2",
			UserID:     bob.ID,
			Principals: `["svc-b"]`, ServiceAccount: "svc-b",
			KeyID:     "key2",
			ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})
	}

	// Search for "alice" should find only 10 enrollments, not the full 30
	list, err := enrollment.ListForAdmin(context.Background(),
		&Identity{Subject: "sub-auditor", Groups: []string{"auditors"}},
		AdminListParams{Limit: 25, Offset: 0, Query: "alice"})
	if err != nil {
		t.Fatalf("ListForAdmin() error = %v", err)
	}

	if list.Total != 10 {
		t.Errorf("ListForAdmin() Total = %d, want 10 (alice's enrollments only)", list.Total)
	}
	if len(list.Enrollments) != 10 {
		t.Errorf("ListForAdmin() returned %d enrollments, want 10", len(list.Enrollments))
	}

	// Verify all returned enrollments are alice's
	for _, row := range list.Enrollments {
		if row.Approver.Username != "alice" {
			t.Errorf("got enrollment approved by %q, expected alice", row.Approver.Username)
		}
	}
}

// TestListForAdmin_SearchFilteringPinnedToSQL verifies that search filtering
// is executed in SQL (via LIKE predicates), not in-memory. This is crucial
// because the same result set is produced either way: both approaches return
// the correct Total and bounded rows. A test that only checks the return
// values cannot distinguish in-memory from SQL filtering. This test pins the
// behavior by verifying the actual SQL contains LIKE clauses, the signature
// of server-side filtering. A future refactoring that moves filtering back
// to memory would lose the LIKE predicates and this test would fail.
func TestListForAdmin_SearchFilteringPinnedToSQL(t *testing.T) {
	t.Parallel()

	auditorCfg := &config.Config{Admin: config.AdminConfig{AuditorGroup: "auditors"}}
	svc := newTestCertRequestServiceWithConfig(t, auditorCfg)
	enrollment := newTestEnrollmentService(t, svc)

	if err := svc.db.AutoMigrate(&model.User{}); err != nil {
		t.Fatalf("failed to migrate users: %v", err)
	}

	// Create two users with different numbers of enrollments.
	alice := model.User{
		ID:        uuid.NewString(),
		Subject:   "sub-alice",
		Username:  "alice",
		Email:     "alice@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	bob := model.User{
		ID:        uuid.NewString(),
		Subject:   "sub-bob",
		Username:  "bob",
		Email:     "bob@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := svc.db.Create(&alice).Error; err != nil {
		t.Fatalf("failed to seed alice: %v", err)
	}
	if err := svc.db.Create(&bob).Error; err != nil {
		t.Fatalf("failed to seed bob: %v", err)
	}

	// Seed 1000 enrollments for alice.
	for i := 0; i < 1000; i++ {
		seedEnrollment(t, svc, model.Enrollment{
			ID:         uuid.NewString(),
			Code:       uuid.NewString(),
			PublicKey:  "key-alice",
			UserID:     alice.ID,
			Principals: `["svc-alice"]`, ServiceAccount: "svc-alice",
			KeyID:     "key-alice",
			ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})
	}

	// Seed 50 enrollments for bob (so alice's enrollments dominate the result set).
	for i := 0; i < 50; i++ {
		seedEnrollment(t, svc, model.Enrollment{
			ID:         uuid.NewString(),
			Code:       uuid.NewString(),
			PublicKey:  "key-bob",
			UserID:     bob.ID,
			Principals: `["svc-bob"]`, ServiceAccount: "svc-bob",
			KeyID:     "key-bob",
			ExpiresAt: time.Now().Add(time.Hour),
			CreatedAt: time.Now(),
		})
	}

	// Record the SQL every query actually issues. This is the whole point of
	// the test: Total=1000 with 10 rows returned is what BOTH implementations
	// produce, so the return values cannot tell them apart. Only the statement
	// text can -- an in-memory filter selects every row and narrows in Go, so
	// its SQL carries no LIKE predicate at all.
	var executed []string
	if err := svc.db.Callback().Query().After("gorm:query").
		Register("test:record_sql", func(tx *gorm.DB) {
			executed = append(executed, tx.Statement.SQL.String())
		}); err != nil {
		t.Fatalf("failed to register the SQL recorder: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.db.Callback().Query().Remove("test:record_sql")
	})

	list, err := enrollment.ListForAdmin(context.Background(),
		&Identity{Subject: "sub-auditor", Groups: []string{"auditors"}},
		AdminListParams{Limit: 10, Offset: 0, Query: "alice"})
	if err != nil {
		t.Fatalf("ListForAdmin() error = %v", err)
	}

	if list.Total != 1000 {
		t.Errorf("ListForAdmin() Total = %d, want 1000 (all alice's enrollments)", list.Total)
	}
	if len(list.Enrollments) != 10 {
		t.Errorf("ListForAdmin() returned %d enrollments, want 10", len(list.Enrollments))
	}

	// Verify all returned enrollments are alice's.
	for _, row := range list.Enrollments {
		if row.Approver.Username != "alice" {
			t.Errorf("got enrollment approved by %q, expected alice", row.Approver.Username)
		}
	}

	// The assertion that actually pins the behaviour. Without a LIKE in the
	// statement the search never reached the database, whatever the returned
	// rows look like.
	var sawLike bool
	for _, stmt := range executed {
		if strings.Contains(strings.ToUpper(stmt), "LIKE") {
			sawLike = true
			break
		}
	}
	if !sawLike {
		t.Errorf("no executed statement carried a LIKE predicate, so the search was not done in SQL; statements were:\n%s",
			strings.Join(executed, "\n"))
	}
}

// Ownership is membership in the enrollment's service account, so a code
// approved by a colleague is the caller's own — this is the change group
// ownership makes to what the service codes page shows.
func TestListForIdentity_Ownership(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (*CertRequestService, *EnrollmentService) {
		t.Helper()
		svc := newTestCertRequestServiceWithConfig(t, &config.Config{})
		return svc, newTestEnrollmentService(t, svc)
	}

	t.Run("should return a code approved by somebody else for an account I hold", func(t *testing.T) {
		t.Parallel()
		svc, enrollment := setup(t)
		colleague := seedUser(t, svc.db, "sub-colleague")
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-shared", PublicKey: "k", Principals: `["svc-shared"]`,
			ServiceAccount: "svc-shared", UserID: colleague,
			ExpiresAt: time.Now().Add(time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(),
			&Identity{Subject: "sub-me", ServiceAccounts: []string{"svc-shared"}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d enrollments, want the colleague's code for my account", len(rows))
		}
	})

	// The other half of the same rule: approving a code grants nothing that
	// outlives holding the account it was approved for.
	t.Run("should not return a code I approved for an account I have lost", func(t *testing.T) {
		t.Parallel()
		svc, enrollment := setup(t)
		me := seedUser(t, svc.db, "sub-me")
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-lost", PublicKey: "k", Principals: `["svc-lost"]`,
			ServiceAccount: "svc-lost", UserID: me,
			ExpiresAt: time.Now().Add(time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(),
			&Identity{Subject: "sub-me", ServiceAccounts: []string{"svc-other"}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d enrollments, want none for an account no longer held", len(rows))
		}
	})

	t.Run("should return an empty list for an identity holding no accounts", func(t *testing.T) {
		t.Parallel()
		svc, enrollment := setup(t)
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-any", PublicKey: "k", Principals: `["svc-any"]`,
			ServiceAccount: "svc-any", ExpiresAt: time.Now().Add(time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(), &Identity{Subject: "sub-me"})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d enrollments, want none", len(rows))
		}
	})

	// A row whose principals never parsed carries no account. It has to stay
	// owned by nobody, or a blank entry in a claim would hand it to whoever
	// carries one.
	t.Run("should not hand an accountless row to an identity with a blank account", func(t *testing.T) {
		t.Parallel()
		svc, enrollment := setup(t)
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-orphan", PublicKey: "k", Principals: "{{{",
			ExpiresAt: time.Now().Add(time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(),
			&Identity{Subject: "sub-me", ServiceAccounts: []string{""}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if len(rows) != 0 {
			t.Errorf("got %d enrollments, want an accountless row owned by nobody", len(rows))
		}
	})

	t.Run("should report who approved each code", func(t *testing.T) {
		t.Parallel()
		svc, enrollment := setup(t)
		colleague := seedUser(t, svc.db, "sub-colleague")
		if err := svc.db.Model(&model.User{}).Where("id = ?", colleague).
			Update("username", "colleague").Error; err != nil {
			t.Fatalf("failed to name the approver: %v", err)
		}
		seedEnrollment(t, svc, model.Enrollment{
			Code: "code-shared", PublicKey: "k", Principals: `["svc-shared"]`,
			ServiceAccount: "svc-shared", UserID: colleague,
			ExpiresAt: time.Now().Add(time.Hour),
		})

		rows, err := enrollment.ListForIdentity(context.Background(),
			&Identity{Subject: "sub-me", ServiceAccounts: []string{"svc-shared"}})
		if err != nil {
			t.Fatalf("ListForIdentity() error = %v", err)
		}
		if rows[0].ApproverUsername != "colleague" {
			t.Errorf("got approver %q, want %q", rows[0].ApproverUsername, "colleague")
		}
	})
}

// The per-row reads apply the same rule as the list, so a holder can open a
// colleague's code and a non-holder cannot open one at all.
func TestGetEnrollmentDetail_Ownership(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) *EnrollmentService {
		t.Helper()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1",
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1",
			ExpiresAt: time.Now().Add(time.Hour),
		})
		return enrollment
	}

	t.Run("should allow a holder who did not approve it", func(t *testing.T) {
		t.Parallel()
		enrollment := setup(t)

		if _, err := enrollment.GetEnrollmentDetail(context.Background(), "enrollment1",
			&Identity{Subject: "sub-stranger", ServiceAccounts: []string{"svc-a"}}); err != nil {
			t.Errorf("GetEnrollmentDetail() error = %v, want a holder to be allowed", err)
		}
	})

	t.Run("should refuse an identity holding a different account", func(t *testing.T) {
		t.Parallel()
		enrollment := setup(t)

		_, err := enrollment.GetEnrollmentDetail(context.Background(), "enrollment1",
			&Identity{Subject: "sub-stranger", ServiceAccounts: []string{"svc-b"}})
		var forbidden *errorresponses.ForbiddenError
		if !errors.As(err, &forbidden) {
			t.Errorf("GetEnrollmentDetail() error = %v, want ForbiddenError", err)
		}
	})
}

// Approval is the only writer of enrollments.service_account, and the
// column is the whole of ownership: a row written without it would be owned
// by nobody the moment it was created.
func TestApproveServiceEnrollment_ShouldRecordTheServiceAccount(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Second)
	enrollService(t, svc, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ7VqQZ8Rz9k1Q4bF0nQXqLdY2mJ3H8sK5tW6uV9xYzA svc@example")

	var enrollment model.Enrollment
	if err := svc.db.First(&enrollment).Error; err != nil {
		t.Fatalf("expected an enrollment row, got error: %v", err)
	}
	if enrollment.ServiceAccount != "svc-deploy" {
		t.Errorf("ServiceAccount = %q, want %q", enrollment.ServiceAccount, "svc-deploy")
	}
	// The same account, written twice for two jobs: principals is what
	// certificates are minted from, service_account is what ownership is
	// queried by. They must not drift.
	if principals := decodeEnrollmentPrincipals(enrollment); len(principals) != 1 ||
		principals[0] != enrollment.ServiceAccount {
		t.Errorf("principals %v disagree with the service account %q",
			principals, enrollment.ServiceAccount)
	}
}
