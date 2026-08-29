package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/notify"
)

// capturingNotifier records what the certificate paths ask to be sent,
// without a broker or a mail relay in the way.
type capturingNotifier struct {
	mu     sync.Mutex
	events []capturedNotification
}

type capturedNotification struct {
	Kind notify.Kind
	// Exactly one of UserID and ServiceAccount is set, matching the two
	// ways notify.Event can be addressed.
	UserID         string
	ServiceAccount string
	Payload        any
}

func (n *capturingNotifier) Notify(_ context.Context, kind notify.Kind, userID string, payload any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, capturedNotification{Kind: kind, UserID: userID, Payload: payload})
}

func (n *capturingNotifier) NotifyServiceAccount(_ context.Context, kind notify.Kind, serviceAccount string, payload any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, capturedNotification{Kind: kind, ServiceAccount: serviceAccount, Payload: payload})
}

func (n *capturingNotifier) captured() []capturedNotification {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]capturedNotification, len(n.events))
	copy(out, n.events)
	return out
}

// only returns the single captured notification of kind, failing when
// there is not exactly one.
func (n *capturingNotifier) only(t *testing.T, kind notify.Kind) capturedNotification {
	t.Helper()

	var found []capturedNotification
	for _, event := range n.captured() {
		if event.Kind == kind {
			found = append(found, event)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d %s notifications, want 1 (all: %+v)", len(found), kind, n.captured())
	}
	return found[0]
}

// The enrollment-created notification tells everyone holding the service
// account what was just authorized for it, so the payload has to carry the
// detail that distinguishes one enrollment from another.
func TestApprove_shouldNotifyTheServiceAccountAboutANewEnrollment(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{ValidDuration: time.Hour, EnrollmentDuration: 90 * 24 * time.Hour},
	})
	notifier := &capturingNotifier{}
	svc.SetNotifier(notifier)

	const publicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ7VqQZ8Rz9k1Q4bF0nQXqLdY2mJ3H8sK5tW6uV9xYzA svc@example"
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: publicKey,
		SourceIP:  "198.51.100.7",
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1", Email: "alice@example.com", ServiceAccounts: []string{"deploy-bot"}}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity,
		DecisionContext{SourceIP: "198.51.100.7"}, ApprovalSelection{ServiceAccount: "deploy-bot"}); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	event := notifier.only(t, notify.KindServiceEnrollmentCreated)

	var enrollment model.Enrollment
	if err := svc.db.First(&enrollment, "certificate_request_id = ?", requestID).Error; err != nil {
		t.Fatalf("read enrollment: %v", err)
	}
	// Addressed to the account, not to the approver: everyone holding it
	// owns the enrollment from the moment it exists.
	if event.ServiceAccount != "deploy-bot" {
		t.Errorf("notified service account %q, want %q", event.ServiceAccount, "deploy-bot")
	}
	if event.UserID != "" {
		t.Errorf("notified user %q, want the event addressed to the account only", event.UserID)
	}

	payload, ok := event.Payload.(*notify.ServiceEnrollmentCreated)
	if !ok {
		t.Fatalf("payload is %T, want *notify.ServiceEnrollmentCreated", event.Payload)
	}
	if payload.ServiceAccount != "deploy-bot" {
		t.Errorf("ServiceAccount = %q", payload.ServiceAccount)
	}
	if payload.RequestID != requestID {
		t.Errorf("RequestID = %q, want %q", payload.RequestID, requestID)
	}
	if payload.EnrollmentID != enrollment.ID {
		t.Errorf("EnrollmentID = %q, want %q", payload.EnrollmentID, enrollment.ID)
	}
	if payload.KeyID != enrollment.KeyID {
		t.Errorf("KeyID = %q, want %q", payload.KeyID, enrollment.KeyID)
	}
	if len(payload.Principals) != 1 || payload.Principals[0] != "deploy-bot" {
		t.Errorf("Principals = %v", payload.Principals)
	}
	if payload.ApprovedByUsername != "alice" {
		t.Errorf("ApprovedByUsername = %q", payload.ApprovedByUsername)
	}
	if payload.RequestSourceIP != "198.51.100.7" {
		t.Errorf("RequestSourceIP = %q", payload.RequestSourceIP)
	}
	if !payload.CodeExpiresAt.Equal(enrollment.ExpiresAt) {
		t.Errorf("CodeExpiresAt = %v, want %v", payload.CodeExpiresAt, enrollment.ExpiresAt)
	}
	if payload.CertificateLifetime != time.Hour {
		t.Errorf("CertificateLifetime = %s, want 1h", payload.CertificateLifetime)
	}
	if payload.PublicKeyType != "ssh-ed25519" {
		t.Errorf("PublicKeyType = %q", payload.PublicKeyType)
	}
	if payload.PublicKeyFingerprint == "" {
		t.Error("PublicKeyFingerprint is empty")
	}
}

// The code is the one thing that must never leave the terminal that ran
// `service enroll`. This asserts it at the point of emission, where a
// future field addition would be made.
func TestApprove_shouldNotPutTheEnrollmentCodeInTheNotification(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{ValidDuration: time.Hour, EnrollmentDuration: 90 * 24 * time.Hour},
	})
	notifier := &capturingNotifier{}
	svc.SetNotifier(notifier)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type: model.CertificateTypeService, PublicKey: "ssh-ed25519 AAAA... svc",
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1", ServiceAccounts: []string{"deploy-bot"}}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity,
		DecisionContext{}, ApprovalSelection{ServiceAccount: "deploy-bot"}); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	var enrollment model.Enrollment
	if err := svc.db.First(&enrollment, "certificate_request_id = ?", requestID).Error; err != nil {
		t.Fatalf("read enrollment: %v", err)
	}

	payload := notifier.only(t, notify.KindServiceEnrollmentCreated).Payload.(*notify.ServiceEnrollmentCreated)
	if containsValue(payload, enrollment.Code) {
		t.Error("the enrollment code reached the notification payload")
	}
}

// A user-type approval is not an enrollment and must not report one.
func TestApprove_shouldNotNotifyForANonServiceRequest(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		User: config.CertOptionsUser{ValidDuration: time.Hour},
	})
	notifier := &capturingNotifier{}
	svc.SetNotifier(notifier)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA... user",
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity,
		DecisionContext{}, ApprovalSelection{Principals: []string{"alice"}}); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	for _, event := range notifier.captured() {
		if event.Kind == notify.KindServiceEnrollmentCreated {
			t.Error("a user-type approval reported a service enrollment")
		}
	}
}

// A rejected approval created nothing, so it must report nothing.
func TestApprove_shouldNotNotifyWhenTheApprovalIsRejected(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{ValidDuration: time.Hour, EnrollmentDuration: 90 * 24 * time.Hour},
	})
	notifier := &capturingNotifier{}
	svc.SetNotifier(notifier)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type: model.CertificateTypeService, PublicKey: "ssh-ed25519 AAAA... svc",
	})
	if err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	// The identity holds no service accounts, so linkage rejects this.
	identity := &Identity{Username: "alice", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity,
		DecisionContext{}, ApprovalSelection{ServiceAccount: "deploy-bot"}); err == nil {
		t.Fatal("Approve accepted a service account the identity does not hold")
	}

	if got := notifier.captured(); len(got) != 0 {
		t.Errorf("a rejected approval sent %d notifications: %+v", len(got), got)
	}
}

// containsValue reports whether needle appears in any string field of the
// created-enrollment payload.
func containsValue(payload *notify.ServiceEnrollmentCreated, needle string) bool {
	if needle == "" {
		return false
	}
	candidates := []string{
		payload.ServiceAccount, payload.RequestID, payload.EnrollmentID, payload.KeyID,
		payload.PublicKeyFingerprint, payload.PublicKeyType, payload.ForceCommand,
		payload.RequestSourceIP, payload.ApprovedByUsername, payload.ServerURL,
	}
	candidates = append(candidates, payload.Principals...)
	candidates = append(candidates, payload.Extensions...)
	candidates = append(candidates, payload.SourceAddresses...)
	for _, candidate := range candidates {
		if candidate == needle {
			return true
		}
	}
	return false
}

// The redemption notification is the one that turns a reusable code into
// something an operator can actually watch, so it has to name where the
// redemption came from and what it produced.
func TestRetrieve_shouldNotifyTheServiceAccountOnEveryRedemption(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{
			ValidDuration:      time.Hour,
			EnrollmentDuration: 90 * 24 * time.Hour,
		},
		ClientTimeout: 50 * time.Second,
	})
	enrollmentSvc := newTestEnrollmentService(t, svc)
	notifier := &capturingNotifier{}
	enrollmentSvc.SetNotifier(notifier)
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

	if _, err := enrollmentSvc.Retrieve(context.Background(), code, "203.0.113.7"); err != nil {
		t.Fatalf("Retrieve: %v", err)
	}

	event := notifier.only(t, notify.KindServiceEnrollmentRedeemed)
	payload, ok := event.Payload.(*notify.ServiceEnrollmentRedeemed)
	if !ok {
		t.Fatalf("payload is %T, want *notify.ServiceEnrollmentRedeemed", event.Payload)
	}

	var enrollment model.Enrollment
	if err := svc.db.First(&enrollment).Error; err != nil {
		t.Fatalf("read enrollment: %v", err)
	}
	// Addressed to the account, not to the approver: everyone holding it
	// owns the enrollment, so there is no one user to name.
	if event.ServiceAccount != enrollment.ServiceAccount {
		t.Errorf("notified service account %q, want %q", event.ServiceAccount, enrollment.ServiceAccount)
	}
	if event.UserID != "" {
		t.Errorf("notified user %q, want the event addressed to the account only", event.UserID)
	}
	if !payload.Succeeded {
		t.Error("Succeeded is false for a redemption that produced a certificate")
	}
	if !payload.FirstRedemption {
		t.Error("FirstRedemption is false for the first redemption")
	}
	if payload.SourceIP != "203.0.113.7" {
		t.Errorf("SourceIP = %q", payload.SourceIP)
	}
	if payload.ServiceAccount != "svc-deploy" {
		t.Errorf("ServiceAccount = %q", payload.ServiceAccount)
	}
	if payload.EnrollmentID != enrollment.ID {
		t.Errorf("EnrollmentID = %q, want %q", payload.EnrollmentID, enrollment.ID)
	}
	if payload.CertificateSerial == 0 {
		t.Error("CertificateSerial is unset")
	}
	if payload.RetrievalID == "" {
		t.Error("RetrievalID is unset")
	}
	if !payload.CodeExpiresAt.Equal(enrollment.ExpiresAt) {
		t.Errorf("CodeExpiresAt = %v, want %v", payload.CodeExpiresAt, enrollment.ExpiresAt)
	}

	// The second redemption is routine, and saying so is what keeps the
	// first one meaningful.
	if _, err := enrollmentSvc.Retrieve(context.Background(), code, "203.0.113.8"); err != nil {
		t.Fatalf("second Retrieve: %v", err)
	}

	var redeemed []capturedNotification
	for _, event := range notifier.captured() {
		if event.Kind == notify.KindServiceEnrollmentRedeemed {
			redeemed = append(redeemed, event)
		}
	}
	if len(redeemed) != 2 {
		t.Fatalf("got %d redemption notifications, want 2", len(redeemed))
	}
	if second := redeemed[1].Payload.(*notify.ServiceEnrollmentRedeemed); second.FirstRedemption {
		t.Error("FirstRedemption is true for the second redemption")
	}
}

// A code that validated and then failed at the signer is exactly what an
// operator wants to hear about, so it is reported rather than swallowed.
func TestRetrieve_shouldNotifyWhenSigningFails(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{
			ValidDuration:      time.Hour,
			EnrollmentDuration: 90 * 24 * time.Hour,
		},
		ClientTimeout: 50 * time.Second,
	})
	enrollmentSvc := newTestEnrollmentService(t, svc)
	notifier := &capturingNotifier{}
	enrollmentSvc.SetNotifier(notifier)

	// No signer on the transport, so the signing job is never answered and
	// the redemption times out.
	code := enrollService(t, svc, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ7VqQZ8Rz9k1Q4bF0nQXqLdY2mJ3H8sK5tW6uV9xYzA svc")

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := enrollmentSvc.Retrieve(ctx, code, "203.0.113.7"); err == nil {
		t.Fatal("Retrieve succeeded with no signer on the transport")
	}

	payload := notifier.only(t, notify.KindServiceEnrollmentRedeemed).Payload.(*notify.ServiceEnrollmentRedeemed)
	if payload.Succeeded {
		t.Error("Succeeded is true for a redemption that produced no certificate")
	}
}

// An unknown code creates no retrieval row and belongs to nobody, so there
// is no one to report it to and nothing to report.
func TestRetrieve_shouldNotNotifyForAnUnknownCode(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{ValidDuration: time.Hour, EnrollmentDuration: 90 * 24 * time.Hour},
	})
	enrollmentSvc := newTestEnrollmentService(t, svc)
	notifier := &capturingNotifier{}
	enrollmentSvc.SetNotifier(notifier)

	if _, err := enrollmentSvc.Retrieve(context.Background(), "no-such-code", "203.0.113.7"); err == nil {
		t.Fatal("Retrieve accepted an unknown code")
	}

	if got := notifier.captured(); len(got) != 0 {
		t.Errorf("an unknown code sent %d notifications: %+v", len(got), got)
	}
}
