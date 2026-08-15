package service

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/signer"
)

// TestPipeline_EndToEnd exercises the whole certificate pipeline in one
// process: create → approve → sign → deliver. It's the first test that
// covers the components together rather than in isolation, and the only one
// that would catch a mismatch between them — a topic name typo, a message
// schema drift, or handlers that individually pass their own tests but never
// hand off to each other.
//
// Deliberately uses the real signer and real gochannel transport rather than
// fakes; the assertion that matters is that a genuine, verifiable
// certificate comes out the far end.
func TestPipeline_EndToEnd(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		User: config.CertOptionsUser{
			Extensions:    []string{"permit-pty"},
			ValidDuration: time.Hour,
		},
	})
	if err := svc.db.AutoMigrate(&model.Certificate{}, &model.User{}); err != nil {
		t.Fatalf("failed to migrate certificates table: %v", err)
	}

	// A throwaway CA, and the signer/listener wired onto the same transport
	// the service already uses — mirroring bootstrap.initPipeline.
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
	signer.NewHandler(keys, svc.publisher).Register(router, svc.subscriber)
	NewSignedReplyHandler(svc.db, svc).Register(router, svc.subscriber)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		if err := router.Run(ctx); err != nil {
			t.Errorf("router stopped with an error: %v", err)
		}
	}()
	t.Cleanup(func() {
		if err := router.Close(); err != nil {
			t.Errorf("unexpected error closing router: %v", err)
		}
	})

	// Handlers must be consuming before anything is published — the
	// transport is non-persistent, so a job published earlier would be
	// dropped rather than queued.
	select {
	case <-router.Running():
	case <-time.After(5 * time.Second):
		t.Fatal("router did not start")
	}

	clientKeypair, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate client keypair: %v", err)
	}
	clientPub, err := clientKeypair.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("failed to marshal client public key: %v", err)
	}

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: clientPub,
		RequestedOptions: RequestedOptions{
			Extensions: []string{"permit-pty", "permit-agent-forwarding"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// The client is already waiting when approval lands, as it would be in
	// practice: `ssh login` opens its SSE stream and blocks.
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

	seedUser(t, svc.db, "sub-1")
	if err := svc.Approve(context.Background(), requestID, &Identity{Username: "alice", Subject: "sub-1"}); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	var res waitResult
	select {
	case res = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the pipeline never delivered a certificate to the waiting client")
	}
	if res.err != nil {
		t.Fatalf("unexpected error from Wait: %v", res.err)
	}
	if res.status != model.CertificateRequestStatusApproved {
		t.Fatalf("got status %q, want %q", res.status, model.CertificateRequestStatusApproved)
	}

	// The delivered certificate must be real: signed by this CA, for this
	// key, valid for the resolved principal.
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(res.cert))
	if err != nil {
		t.Fatalf("failed to parse the delivered certificate: %v", err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		t.Fatalf("expected an *ssh.Certificate, got %T", pub)
	}

	caPub := caKeypair.Public()
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return string(auth.Marshal()) == string(caPub.Marshal())
		},
	}
	if err := checker.CheckCert("alice", cert); err != nil {
		t.Errorf("delivered certificate did not validate: %v", err)
	}
	if string(cert.Key.Marshal()) != string(clientKeypair.Public().Marshal()) {
		t.Error("delivered certificate is not bound to the requesting public key")
	}
	if _, ok := cert.Permissions.Extensions["permit-pty"]; !ok {
		t.Errorf("expected permit-pty to survive narrowing, got %v", cert.Permissions.Extensions)
	}
	if _, ok := cert.Permissions.Extensions["permit-agent-forwarding"]; ok {
		t.Error("expected permit-agent-forwarding to be narrowed away by server config")
	}

	// And the audit trail must exist, with the same serial as the issued
	// certificate.
	var audit model.Certificate
	if err := svc.db.First(&audit).Error; err != nil {
		t.Fatalf("expected a certificate audit row, got error: %v", err)
	}
	if audit.SerialNumber != cert.Serial {
		t.Errorf("audit serial %d does not match issued serial %d", audit.SerialNumber, cert.Serial)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusApproved {
		t.Errorf("got final request status %q, want %q", req.Status, model.CertificateRequestStatusApproved)
	}
}
