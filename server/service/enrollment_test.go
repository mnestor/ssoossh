package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strconv"
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
		&model.UserLDAP{},
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
	keys, err := signer.NewConfigKeySource(string(caPEM), "")
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
		if err := svc.db.AutoMigrate(&model.User{}, &model.UserLDAP{}); err != nil {
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
		if err := svc.db.AutoMigrate(&model.User{}, &model.UserLDAP{}); err != nil {
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

		if err := svc.db.AutoMigrate(&model.User{}, &model.UserLDAP{}); err != nil {
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

	if err := svc.db.AutoMigrate(&model.User{}, &model.UserLDAP{}); err != nil {
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

	if err := svc.db.AutoMigrate(&model.User{}, &model.UserLDAP{}); err != nil {
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

// seedAccountHolder inserts one users row with the stored account lists a
// holder lookup reads, which seedUser deliberately leaves empty.
func seedAccountHolder(t *testing.T, db *gorm.DB, username, serviceAccounts, otherAccounts string, disabled bool) string {
	t.Helper()

	user := model.User{
		ID:              uuid.NewString(),
		Subject:         "sub-" + username,
		Username:        username,
		Email:           username + "@example.com",
		DisplayName:     strings.ToUpper(username),
		ServiceAccounts: serviceAccounts,
		OtherAccounts:   otherAccounts,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}
	if disabled {
		now := time.Now()
		user.DisabledAt = &now
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed holder %q: %v", username, err)
	}
	return user.ID
}

// holderUsernames flattens a holder list for comparison.
func holderUsernames(holders []AccountHolder) []string {
	out := make([]string, 0, len(holders))
	for _, h := range holders {
		out = append(out, h.Username)
	}
	return out
}

// A code belongs to its service account rather than to whoever approved it,
// so "who else has this" has no answer on the enrollment row. This is that
// answer, and it is the same authorization as the detail view: everyone who
// can already redeem the code may see who else can.
func TestListAccountHolders(t *testing.T) {
	t.Parallel()

	t.Run("should list every holder of the account", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)

		ownerID := seedAccountHolder(t, svc.db, "alice", `["svc-a"]`, `[]`, false)
		seedAccountHolder(t, svc.db, "bob", `["svc-a","svc-b"]`, `[]`, false)
		seedAccountHolder(t, svc.db, "carol", `["svc-b"]`, `[]`, false)
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1",
			ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		})

		got, err := enrollment.ListAccountHolders(context.Background(),
			"enrollment1", &Identity{Subject: "sub-alice", ServiceAccounts: []string{"svc-a"}})
		if err != nil {
			t.Fatalf("ListAccountHolders() error = %v", err)
		}
		if got.ServiceAccount != "svc-a" {
			t.Errorf("service account = %q, want svc-a", got.ServiceAccount)
		}
		if names := holderUsernames(got.Holders); !slices.Equal(names, []string{"alice", "bob"}) {
			t.Errorf("holders = %v, want alice and bob, not carol", names)
		}
	})

	// The panel showed nobody at all in a directory deployment: the sync
	// writes the accounts it resolves to user_ldap and leaves the claim
	// column as login found it, and this lookup read only the claim column.
	t.Run("should list a holder the directory knows about", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)
		svc.config.LDAP.Enabled = true

		ownerID := seedAccountHolder(t, svc.db, "alice", `["svc-a"]`, `[]`, false)
		bobID := seedAccountHolder(t, svc.db, "bob", `null`, `null`, false)
		if err := svc.db.Create(&model.UserLDAP{
			UserID:     bobID,
			DN:         "uid=bob,ou=people,dc=example,dc=com",
			Attributes: `{"service_accounts":["svc-a"],"other_accounts":[]}`,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}).Error; err != nil {
			t.Fatalf("failed to seed the directory row: %v", err)
		}
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1",
			ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		})

		got, err := enrollment.ListAccountHolders(context.Background(),
			"enrollment1", &Identity{Subject: "sub-alice", ServiceAccounts: []string{"svc-a"}})
		if err != nil {
			t.Fatalf("ListAccountHolders() error = %v", err)
		}
		if names := holderUsernames(got.Holders); !slices.Equal(names, []string{"alice", "bob"}) {
			t.Errorf("holders = %v, want the claimed holder and the directory one", names)
		}
	})

	// A disabled holder still carries the claim and comes back with the
	// account, so leaving them out would answer "who has access" with a set
	// that quietly grows again later.
	t.Run("should list a disabled holder and mark them disabled", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)

		ownerID := seedAccountHolder(t, svc.db, "alice", `["svc-a"]`, `[]`, false)
		seedAccountHolder(t, svc.db, "mallory", `["svc-a"]`, `[]`, true)
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1",
			ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		})

		got, err := enrollment.ListAccountHolders(context.Background(),
			"enrollment1", &Identity{Subject: "sub-alice", ServiceAccounts: []string{"svc-a"}})
		if err != nil {
			t.Fatalf("ListAccountHolders() error = %v", err)
		}
		if len(got.Holders) != 2 {
			t.Fatalf("holders = %v, want both the enabled and the disabled one", holderUsernames(got.Holders))
		}
		for _, holder := range got.Holders {
			if (holder.Username == "mallory") != holder.Disabled {
				t.Errorf("holder %q disabled = %v, want it to match the account state", holder.Username, holder.Disabled)
			}
		}
	})

	// The LIKE prefilter can match inside a longer name; the decode is what
	// makes the answer exact.
	t.Run("should not match an account name that merely contains the one asked for", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)

		ownerID := seedAccountHolder(t, svc.db, "alice", `["svc-a"]`, `[]`, false)
		seedAccountHolder(t, svc.db, "bob", `["svc-append"]`, `[]`, false)
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1",
			ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		})

		got, err := enrollment.ListAccountHolders(context.Background(),
			"enrollment1", &Identity{Subject: "sub-alice", ServiceAccounts: []string{"svc-a"}})
		if err != nil {
			t.Fatalf("ListAccountHolders() error = %v", err)
		}
		if names := holderUsernames(got.Holders); !slices.Equal(names, []string{"alice"}) {
			t.Errorf("holders = %v, want only the exact holder", names)
		}
	})

	t.Run("should refuse an unrelated user", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)

		ownerID := seedAccountHolder(t, svc.db, "alice", `["svc-a"]`, `[]`, false)
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1",
			ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		})

		_, err := enrollment.ListAccountHolders(context.Background(),
			"enrollment1", &Identity{Subject: "sub-stranger", ServiceAccounts: []string{"svc-z"}})

		var forbidden *errorresponses.ForbiddenError
		if !errors.As(err, &forbidden) {
			t.Errorf("ListAccountHolders() error = %v, want ForbiddenError", err)
		}
	})

	t.Run("should report an unknown enrollment as not found", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)

		_, err := enrollment.ListAccountHolders(context.Background(),
			"no-such-enrollment", &Identity{Subject: "sub-alice"})

		var notFound *errorresponses.NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("ListAccountHolders() error = %v, want NotFoundError", err)
		}
	})

	// With allow_user_accounts on the account may be a person's own, and
	// then the person holds it — and the panel says which kind of holder
	// they are, because "this is their own account" reads very differently
	// from "a claim vouches for them".
	t.Run("should include the person whose own account this is", func(t *testing.T) {
		t.Parallel()
		cfg := &config.Config{}
		cfg.CertOptions.Service.AllowUserAccounts = true
		svc := newTestCertRequestServiceWithConfig(t, cfg)
		enrollment := newTestEnrollmentService(t, svc)

		ownerID := seedAccountHolder(t, svc.db, "alice", `[]`, `["alice.adm"]`, false)
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
			Principals: `["alice.adm"]`, ServiceAccount: "alice.adm", KeyID: "key1",
			ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		})

		got, err := enrollment.ListAccountHolders(context.Background(),
			"enrollment1", &Identity{Subject: "sub-alice", Username: "alice", OtherAccounts: []string{"alice.adm"}})
		if err != nil {
			t.Fatalf("ListAccountHolders() error = %v", err)
		}
		if len(got.Holders) != 1 || !got.Holders[0].Own {
			t.Errorf("holders = %+v, want alice listed as holding her own account", got.Holders)
		}
	})
}

// An approver who mints a code under their own account has to be able to
// open it afterwards. The set that decides what may be approved and the set
// that decides what its author can then see must be the same one.
func TestEnrollmentOwnership_ShouldFollowAllowUserAccounts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		allowUserAccounts bool
		wantVisible       bool
	}{
		{name: "should hide an own-account enrollment while the setting is off", allowUserAccounts: false},
		{name: "should show an own-account enrollment once the setting is on", allowUserAccounts: true, wantVisible: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{}
			cfg.CertOptions.Service.AllowUserAccounts = tt.allowUserAccounts
			svc := newTestCertRequestServiceWithConfig(t, cfg)
			enrollment := newTestEnrollmentService(t, svc)

			ownerID := seedAccountHolder(t, svc.db, "alice", `[]`, `[]`, false)
			seedEnrollment(t, svc, model.Enrollment{
				ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
				Principals: `["alice"]`, ServiceAccount: "alice", KeyID: "key1",
				ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
			})

			identity := &Identity{Subject: "sub-alice", Username: "alice"}
			list, err := enrollment.ListForIdentity(context.Background(), identity)
			if err != nil {
				t.Fatalf("ListForIdentity() error = %v", err)
			}
			if (len(list) > 0) != tt.wantVisible {
				t.Errorf("ListForIdentity returned %d enrollments, want visible=%v", len(list), tt.wantVisible)
			}

			_, err = enrollment.GetEnrollmentDetail(context.Background(), "enrollment1", identity)
			if tt.wantVisible && err != nil {
				t.Errorf("GetEnrollmentDetail() error = %v, want the approver to be able to open their own code", err)
			}
			if !tt.wantVisible && err == nil {
				t.Error("GetEnrollmentDetail() succeeded, want it refused while own accounts are not service accounts")
			}
		})
	}
}

// The enrollment's own id is the identifier every other record of it
// carries: the notification email, the enrollment.* audit events, the
// server log lines. An operator holding one from any of those had nowhere
// to paste it — the certificate request id is a different identifier, and
// searching by it is not the same question.
func TestListForAdmin_ShouldFindAnEnrollmentByItsOwnID(t *testing.T) {
	t.Parallel()

	auditor := &Identity{Subject: "sub-auditor", Groups: []string{"auditors"}}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{
			name:  "should find the row by its whole id",
			query: "1f0a9c3e-0000-4000-8000-000000000001",
			want:  []string{"1f0a9c3e-0000-4000-8000-000000000001"},
		},
		{
			// Filter is a substring match, so the truncated id the detail
			// panel shows is enough to find the row it came from.
			name:  "should find the row by the prefix the panel displays",
			query: "1f0a9",
			want:  []string{"1f0a9c3e-0000-4000-8000-000000000001"},
		},
		{
			name:  "should match case-insensitively",
			query: "1F0A9C3E",
			want:  []string{"1f0a9c3e-0000-4000-8000-000000000001"},
		},
		{
			name:  "should not match an id belonging to another enrollment",
			query: "2b7d4e5f-0000-4000-8000-000000000002",
			want:  []string{"2b7d4e5f-0000-4000-8000-000000000002"},
		},
		{
			name:  "should return nothing for an id no enrollment carries",
			query: "9999ffff-0000-4000-8000-00000000ffff",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{Admin: config.AdminConfig{AuditorGroup: "auditors"}}
			svc := newTestCertRequestServiceWithConfig(t, cfg)
			enrollment := newTestEnrollmentService(t, svc)

			ownerID := seedUser(t, svc.db, "sub-owner")
			for i, id := range []string{
				"1f0a9c3e-0000-4000-8000-000000000001",
				"2b7d4e5f-0000-4000-8000-000000000002",
			} {
				seedEnrollment(t, svc, model.Enrollment{
					ID: id, Code: "code" + strconv.Itoa(i), PublicKey: "key" + strconv.Itoa(i),
					UserID: ownerID, Principals: `["svc-a"]`, ServiceAccount: "svc-a",
					KeyID: "key" + strconv.Itoa(i), ExpiresAt: time.Now().Add(time.Hour),
					CreatedAt: time.Now(),
				})
			}

			list, err := enrollment.ListForAdmin(context.Background(), auditor,
				AdminListParams{Limit: 25, Offset: 0, Query: tt.query})
			if err != nil {
				t.Fatalf("ListForAdmin() error = %v", err)
			}

			got := make([]string, 0, len(list.Enrollments))
			for _, row := range list.Enrollments {
				got = append(got, row.Enrollment.ID)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("search %q returned %v, want %v", tt.query, got, tt.want)
			}
		})
	}
}

// TestExpireForIdentity covers the holder-facing expiry. The authorization
// boundary is the point: this is the first path that lets somebody who is
// not an admin retire a code, so what matters is that it retires exactly
// the codes they hold and refuses the rest.
func TestExpireForIdentity(t *testing.T) {
	t.Parallel()

	// seedLiveEnrollment puts one live code for svc-a in the database and
	// returns the service under test alongside it.
	seedLiveEnrollment := func(t *testing.T) (*EnrollmentService, *CertRequestService, string) {
		t.Helper()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)
		ownerID := seedAccountHolder(t, svc.db, "alice", `["svc-a"]`, `[]`, false)
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1",
			ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
		})
		return enrollment, svc, ownerID
	}

	/** expiresAt reads the stored expiry back. */
	expiresAt := func(t *testing.T, svc *CertRequestService) time.Time {
		t.Helper()
		var row model.Enrollment
		if err := svc.db.First(&row, "id = ?", "enrollment1").Error; err != nil {
			t.Fatalf("reading the enrollment back: %v", err)
		}
		return row.ExpiresAt
	}

	t.Run("should expire a code for an account the caller holds", func(t *testing.T) {
		t.Parallel()
		enrollment, svc, _ := seedLiveEnrollment(t)

		err := enrollment.ExpireForIdentity(context.Background(), "enrollment1",
			&Identity{Subject: "sub-alice", ServiceAccounts: []string{"svc-a"}}, "job decommissioned")
		if err != nil {
			t.Fatalf("ExpireForIdentity() error = %v", err)
		}
		if at := expiresAt(t, svc); at.After(time.Now()) {
			t.Errorf("expires_at = %v, want it moved to now or earlier", at)
		}
	})

	// The whole reason this path exists is that a code belongs to its
	// account rather than its approver — but only to that account.
	t.Run("should refuse a code for an account the caller does not hold", func(t *testing.T) {
		t.Parallel()
		enrollment, svc, _ := seedLiveEnrollment(t)
		before := expiresAt(t, svc)

		err := enrollment.ExpireForIdentity(context.Background(), "enrollment1",
			&Identity{Subject: "sub-bob", ServiceAccounts: []string{"svc-b"}}, "not mine")

		var forbidden *errorresponses.ForbiddenError
		if !errors.As(err, &forbidden) {
			t.Fatalf("ExpireForIdentity() error = %v, want a forbidden error", err)
		}
		if at := expiresAt(t, svc); !at.Equal(before) {
			t.Errorf("expires_at = %v, want it untouched at %v", at, before)
		}
	})

	// A reason is required for this action, and the check has to happen
	// before anything is written.
	t.Run("should refuse an expiry with no reason", func(t *testing.T) {
		t.Parallel()
		enrollment, svc, _ := seedLiveEnrollment(t)
		before := expiresAt(t, svc)

		err := enrollment.ExpireForIdentity(context.Background(), "enrollment1",
			&Identity{Subject: "sub-alice", ServiceAccounts: []string{"svc-a"}}, "   ")

		var invalid *errorresponses.InvalidRequestError
		if !errors.As(err, &invalid) {
			t.Fatalf("ExpireForIdentity() error = %v, want an invalid request error", err)
		}
		if at := expiresAt(t, svc); !at.Equal(before) {
			t.Errorf("expires_at = %v, want it untouched at %v", at, before)
		}
	})

	t.Run("should report an enrollment that does not exist as missing", func(t *testing.T) {
		t.Parallel()
		enrollment, _, _ := seedLiveEnrollment(t)

		err := enrollment.ExpireForIdentity(context.Background(), "nope",
			&Identity{Subject: "sub-alice", ServiceAccounts: []string{"svc-a"}}, "tidying up")

		var notFound *errorresponses.NotFoundError
		if !errors.As(err, &notFound) {
			t.Errorf("ExpireForIdentity() error = %v, want a not found error", err)
		}
	})

	// Rewriting expires_at on a code that died last month would move the
	// moment it died to today, which is a lie told to every later reader.
	t.Run("should leave an already-expired code exactly as it is", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Second)
		enrollment := newTestEnrollmentService(t, svc)
		ownerID := seedAccountHolder(t, svc.db, "alice", `["svc-a"]`, `[]`, false)
		expiredAt := time.Now().Add(-720 * time.Hour)
		seedEnrollment(t, svc, model.Enrollment{
			ID: "enrollment1", Code: "code1", PublicKey: "key1", UserID: ownerID,
			Principals: `["svc-a"]`, ServiceAccount: "svc-a", KeyID: "key1",
			ExpiresAt: expiredAt, CreatedAt: time.Now().Add(-800 * time.Hour),
		})

		err := enrollment.ExpireForIdentity(context.Background(), "enrollment1",
			&Identity{Subject: "sub-alice", ServiceAccounts: []string{"svc-a"}}, "already gone")
		if err != nil {
			t.Fatalf("ExpireForIdentity() error = %v, want the already-expired case to succeed", err)
		}

		var row model.Enrollment
		if err := svc.db.First(&row, "id = ?", "enrollment1").Error; err != nil {
			t.Fatalf("reading the enrollment back: %v", err)
		}
		if !row.ExpiresAt.Truncate(time.Second).Equal(expiredAt.Truncate(time.Second)) {
			t.Errorf("expires_at = %v, want the original %v", row.ExpiresAt, expiredAt)
		}
	})
}
