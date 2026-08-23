package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

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

	if err := svc.db.AutoMigrate(&model.Certificate{}, &model.User{}, &model.EnrollmentRetrieval{}); err != nil {
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

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: publicKey,
	})
	if err != nil {
		t.Fatalf("unexpected error creating service request: %v", err)
	}

	seedUser(t, svc.db, "sub-svc")
	err = svc.Approve(context.Background(), requestID,
		&Identity{Username: "approver", Subject: "sub-svc"}, DecisionContext{})
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
		Service:        config.CertOptionsService{ValidDuration: time.Hour},
		SigningTimeout: 5 * time.Second,
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
	if err := checker.CheckCert("approver", cert); err != nil {
		t.Errorf("delivered certificate did not validate for the approval-time principal: %v", err)
	}
	if string(cert.Key.Marshal()) != string(clientKeypair.Public().Marshal()) {
		t.Error("delivered certificate is not bound to the enrolled public key")
	}

	// The certificate must not outlive the enrollment window fixed at
	// approval time.
	var row model.Enrollment
	if err := svc.db.First(&row).Error; err != nil {
		t.Fatalf("failed to read back enrollment: %v", err)
	}
	//nolint:gosec // Unix timestamps are non-negative; conversion is safe.
	if got, want := int64(cert.ValidBefore), row.ExpiresAt.Unix(); got != want {
		t.Errorf("certificate ValidBefore %d, want enrollment expiry %d", got, want)
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
		Service:        config.CertOptionsService{ValidDuration: time.Hour},
		SigningTimeout: 5 * time.Second,
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
		SigningTimeout: 5 * time.Second,
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
		SigningTimeout: 100 * time.Millisecond,
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
