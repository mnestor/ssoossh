package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// Test methodology: the listener is driven by calling handle directly with a
// constructed reply, so each case asserts one outcome (audit row, request
// status, what a waiting client receives) without router scaffolding. The
// full pipeline is covered by pipeline_test.go.

// newTestSignedReplyHandler returns a handler over svc's database, with the
// certificates table migrated.
func newTestSignedReplyHandler(t *testing.T, svc *CertRequestService) *SignedReplyHandler {
	t.Helper()

	if err := svc.db.AutoMigrate(&model.Certificate{}, &model.User{}); err != nil {
		t.Fatalf("failed to migrate certificates table: %v", err)
	}
	return NewSignedReplyHandler(svc.db, svc)
}

// signingRequest creates a request and moves it to Signing, the state a
// signed reply arrives for.
func signingRequest(t *testing.T, svc *CertRequestService) string {
	t.Helper()

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusSigning).Error; err != nil {
		t.Fatalf("failed to move request to signing: %v", err)
	}
	return requestID
}

// deliver runs h.handle against reply.
func deliver(t *testing.T, h *SignedReplyHandler, reply certmsg.SignedReply) error {
	t.Helper()

	payload, err := json.Marshal(reply)
	if err != nil {
		t.Fatalf("failed to encode reply: %v", err)
	}
	return h.handle(message.NewMessage(watermill.NewUUID(), payload))
}

func TestSignedReplyHandler_ShouldRecordAuditRowAndResolveRequest(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{})
	h := newTestSignedReplyHandler(t, svc)
	requestID := signingRequest(t, svc)

	issued := time.Now().Truncate(time.Second)
	reply := certmsg.SignedReply{
		RequestID:            requestID,
		Type:                 model.CertificateTypeUser,
		Certificate:          "ssh-ed25519-cert-v01@openssh.com AAAA...",
		Serial:               12345,
		KeyID:                "alice",
		Principals:           []string{"alice", "alice@example.com"},
		PublicKeyFingerprint: "SHA256:abc",
		Extensions:           []string{"permit-pty"},
		ValidAfter:           issued,
		ValidBefore:          issued.Add(time.Hour),
	}

	if err := deliver(t, h, reply); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cert model.Certificate
	if err := svc.db.First(&cert).Error; err != nil {
		t.Fatalf("expected an audit row, got error: %v", err)
	}
	if cert.SerialNumber != 12345 {
		t.Errorf("got serial %d, want 12345", cert.SerialNumber)
	}
	if cert.PublicKeyFingerprint != "SHA256:abc" {
		t.Errorf("got fingerprint %q, want %q", cert.PublicKeyFingerprint, "SHA256:abc")
	}
	if cert.Principals != "alice,alice@example.com" {
		t.Errorf("got principals %q, want %q", cert.Principals, "alice,alice@example.com")
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusApproved {
		t.Errorf("got status %q, want %q", req.Status, model.CertificateRequestStatusApproved)
	}
}

func TestSignedReplyHandler_ShouldDeliverCertificateToWaitingClient(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{})
	h := newTestSignedReplyHandler(t, svc)
	requestID := signingRequest(t, svc)

	type waitResult struct {
		status model.CertificateRequestStatus
		cert   string
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		status, cert, _, err := svc.Wait(context.Background(), requestID)
		done <- waitResult{status, cert, err}
	}()
	time.Sleep(50 * time.Millisecond)

	const certificate = "ssh-ed25519-cert-v01@openssh.com AAAA..."
	if err := deliver(t, h, certmsg.SignedReply{
		RequestID:   requestID,
		Type:        model.CertificateTypeUser,
		Certificate: certificate,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("unexpected error from Wait: %v", res.err)
		}
		if res.status != model.CertificateRequestStatusApproved {
			t.Errorf("got status %q, want %q", res.status, model.CertificateRequestStatusApproved)
		}
		if res.cert != certificate {
			t.Errorf("got certificate %q, want %q", res.cert, certificate)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not receive the delivered certificate")
	}
}

func TestSignedReplyHandler_ShouldMarkRequestFailedOnSigningFailure(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{})
	h := newTestSignedReplyHandler(t, svc)
	requestID := signingRequest(t, svc)

	if err := deliver(t, h, certmsg.SignedReply{
		RequestID: requestID,
		Type:      model.CertificateTypeUser,
		Error:     "ssh-agent unreachable",
		ErrorCode: certmsg.ErrCodeCAUnavailable,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var count int64
	if err := svc.db.Model(&model.Certificate{}).Count(&count).Error; err != nil {
		t.Fatalf("unexpected error counting certificates: %v", err)
	}
	if count != 0 {
		t.Errorf("expected no audit row for a failed signing, got %d", count)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusFailed {
		t.Errorf("got status %q, want %q", req.Status, model.CertificateRequestStatusFailed)
	}
	// The signer's error detail must survive in the row, not just in a log
	// line, so a failure can still be explained after the fact.
	if !strings.Contains(req.FailureReason, certmsg.ErrCodeCAUnavailable) {
		t.Errorf("expected failure reason to mention %q, got %q", certmsg.ErrCodeCAUnavailable, req.FailureReason)
	}
	if !strings.Contains(req.FailureReason, "ssh-agent unreachable") {
		t.Errorf("expected failure reason to include the signer's message, got %q", req.FailureReason)
	}

	// The waiting client must get a terminal answer rather than hang.
	status, _, _, err := svc.Wait(context.Background(), requestID)
	if err != nil {
		t.Fatalf("unexpected error from Wait: %v", err)
	}
	if status != model.CertificateRequestStatusFailed {
		t.Errorf("got status %q, want %q", status, model.CertificateRequestStatusFailed)
	}
}

// TestSignedReplyHandler_ShouldNotOverwriteAnAlreadyResolvedRequest covers
// the race where a request was denied (or expired) while the signer was
// working: the guarded update matches nothing, which is expected, and the
// reply is still acked rather than redelivered forever.
func TestSignedReplyHandler_ShouldNotOverwriteAnAlreadyResolvedRequest(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{})
	h := newTestSignedReplyHandler(t, svc)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}
	if err := svc.Deny(context.Background(), requestID, &Identity{Username: "approver", Subject: "sub-approver"}, DecisionContext{}); err != nil {
		t.Fatalf("unexpected error denying request: %v", err)
	}

	if err := deliver(t, h, certmsg.SignedReply{
		RequestID:   requestID,
		Type:        model.CertificateTypeUser,
		Certificate: "ssh-ed25519-cert-v01@openssh.com AAAA...",
	}); err != nil {
		t.Fatalf("expected the reply to be acked, got %v", err)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusDenied {
		t.Errorf("expected the denial to stand, got status %q", req.Status)
	}
}

func TestSignedReplyHandler_ShouldAckAnUnparseableReply(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{})
	h := newTestSignedReplyHandler(t, svc)

	if err := h.handle(message.NewMessage(watermill.NewUUID(), []byte("not json"))); err != nil {
		t.Fatalf("expected the message to be acked (nil error), got %v", err)
	}
}

// TestSignedReplyHandler_ShouldRecordTheCertificateOwner pins that an issued
// certificate is attributable to a user, which is what per-user history
// depends on. The owner comes off the request row rather than the signing
// job, so the signer stays database-free.
func TestSignedReplyHandler_ShouldRecordTheCertificateOwner(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	h := newTestSignedReplyHandler(t, svc)

	userID := seedUser(t, svc.db, "sub-alice")
	requestID := mustCreateUserRequest(t, svc)
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("user_id", userID).Error; err != nil {
		t.Fatalf("failed to bind the request to a user: %v", err)
	}

	if err := h.recordCertificate(context.Background(), certmsg.SignedReply{
		RequestID:            requestID,
		Type:                 model.CertificateTypeUser,
		PublicKeyFingerprint: "SHA256:test",
		Serial:               42,
		KeyID:                "alice",
		Principals:           []string{"alice"},
		ValidAfter:           time.Now(),
		ValidBefore:          time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("unexpected error recording the certificate: %v", err)
	}

	var cert model.Certificate
	if err := svc.db.First(&cert, "serial_number = ?", 42).Error; err != nil {
		t.Fatalf("unexpected error reading the audit row: %v", err)
	}
	if cert.UserID == nil {
		t.Fatal("expected the certificate audit row to record an owner")
	}
	if *cert.UserID != userID {
		t.Errorf("got user_id %q, want %q", *cert.UserID, userID)
	}
}

// TestSignedReplyHandler_ShouldWarnAndProceedForAnUnknownRequestID covers
// recordCertificate's owner-lookup err!=nil branch specifically — distinct
// from TestSignedReplyHandler_ShouldStillRecordACertificateWithNoOwner
// below, which uses a request that exists but was never bound (a different
// case in the same switch). Here the request row doesn't exist at all.
func TestSignedReplyHandler_ShouldWarnAndProceedForAnUnknownRequestID(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	h := newTestSignedReplyHandler(t, svc)

	if err := h.recordCertificate(context.Background(), certmsg.SignedReply{
		RequestID:            "does-not-exist",
		Type:                 model.CertificateTypeUser,
		PublicKeyFingerprint: "SHA256:test",
		Serial:               44,
		Principals:           []string{"alice"},
		ValidAfter:           time.Now(),
		ValidBefore:          time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("expected an unknown request ID to still record a certificate, got %v", err)
	}

	var cert model.Certificate
	if err := svc.db.First(&cert, "serial_number = ?", 44).Error; err != nil {
		t.Fatalf("unexpected error reading the audit row: %v", err)
	}
	if cert.UserID != nil {
		t.Errorf("got user_id %v, want nil when the owning request can't be resolved", *cert.UserID)
	}
}

// TestSignedReplyHandler_ShouldSurfaceACreateFailure covers
// recordCertificate's own Create error, and resolveSuccess's passthrough of
// it — a closed connection fails both the owner lookup (already covered by
// the unknown-request-ID case above) and the Create that follows it.
func TestSignedReplyHandler_ShouldSurfaceACreateFailure(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	h := newTestSignedReplyHandler(t, svc)
	requestID := signingRequest(t, svc)
	closeUnderlyingDB(t, svc.db)

	reply := certmsg.SignedReply{
		RequestID:   requestID,
		Type:        model.CertificateTypeUser,
		Serial:      45,
		Certificate: "ssh-ed25519-cert-v01@openssh.com AAAA...",
		Principals:  []string{"alice"},
		ValidAfter:  time.Now(),
		ValidBefore: time.Now().Add(time.Hour),
	}

	if err := h.recordCertificate(context.Background(), reply); err == nil {
		t.Error("recordCertificate() error = nil, want error")
	}
	if err := h.resolveSuccess(context.Background(), reply); err == nil {
		t.Error("resolveSuccess() error = nil, want error passed through from recordCertificate")
	}
}

// TestMarkResolved_ShouldLogAndReturnOnADBError covers markResolved's own
// error branch. markResolved has no return value to assert on — Deny/expire
// already prove the guarded-update semantics — so this only confirms it
// doesn't panic when the update itself fails.
func TestMarkResolved_ShouldLogAndReturnOnADBError(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	h := newTestSignedReplyHandler(t, svc)
	requestID := signingRequest(t, svc)
	closeUnderlyingDB(t, svc.db)

	h.markResolved(context.Background(), requestID, model.CertificateRequestStatusApproved, "")
}

// TestSignedReplyHandler_ShouldStillRecordACertificateWithNoOwner keeps a
// missing owner from failing issuance: the certificate is already signed by
// this point, so dropping the audit row would lose it entirely. It costs
// per-user history for that row and nothing else.
func TestSignedReplyHandler_ShouldStillRecordACertificateWithNoOwner(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	h := newTestSignedReplyHandler(t, svc)

	requestID := mustCreateUserRequest(t, svc)

	if err := h.recordCertificate(context.Background(), certmsg.SignedReply{
		RequestID:            requestID,
		Type:                 model.CertificateTypeUser,
		PublicKeyFingerprint: "SHA256:test",
		Serial:               43,
		Principals:           []string{"alice"},
		ValidAfter:           time.Now(),
		ValidBefore:          time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("expected an unbound request to still record a certificate, got %v", err)
	}

	var cert model.Certificate
	if err := svc.db.First(&cert, "serial_number = ?", 43).Error; err != nil {
		t.Fatalf("unexpected error reading the audit row: %v", err)
	}
	if cert.UserID != nil {
		t.Errorf("got user_id %v, want nil for an unbound request", *cert.UserID)
	}
}
