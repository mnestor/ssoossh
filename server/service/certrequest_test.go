package service

// Test methodology: unit tests for CertRequestService against a real
// in-memory sqlite *gorm.DB (AutoMigrate'd from model.CertificateRequest —
// this exercises the service's query/expiry logic, not full migration
// correctness, which server/bootstrap/db_test.go covers). Tests run in
// parallel where they don't share a DB/service instance.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// newTestCertRequestService opens an in-memory sqlite DB migrated for
// model.CertificateRequest and returns a CertRequestService backed by it,
// with RequestTTL as given (0 disables expiry).
func newTestCertRequestService(t *testing.T, ttl time.Duration) *CertRequestService {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.CertificateRequest{}); err != nil {
		t.Fatalf("failed to migrate certificate_requests table: %v", err)
	}

	svc, err := NewCertRequestService(&config.Config{
		CertOptions: config.CertificateOptions{RequestTTL: ttl},
	}, db)
	if err != nil {
		t.Fatalf("failed to construct CertRequestService: %v", err)
	}
	return svc
}

func TestCertRequestService_Wait_ShouldReturnErrorForUnknownID(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)

	_, _, err := svc.Wait(context.Background(), "does-not-exist")

	var notFound *errorresponses.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected a *errorresponses.NotFoundError, got %T: %v", err, err)
	}
}

func TestCertRequestService_Wait_ShouldUnblockOnDeny(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	type waitResult struct {
		status model.CertificateRequestStatus
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		status, _, err := svc.Wait(context.Background(), requestID)
		done <- waitResult{status, err}
	}()

	// Give Wait a moment to actually block before denying, so this
	// exercises the "already blocked" path rather than racing CreateRequest.
	time.Sleep(10 * time.Millisecond)

	if err := svc.Deny(context.Background(), requestID); err != nil {
		t.Fatalf("unexpected error denying request: %v", err)
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("unexpected error from Wait: %v", res.err)
		}
		if res.status != model.CertificateRequestStatusDenied {
			t.Errorf("got status %q, want %q", res.status, model.CertificateRequestStatusDenied)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not unblock after Deny")
	}
}

func TestCertRequestService_Wait_ShouldReturnCachedOutcomeOnLateReconnect(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Resolve it with nobody blocked in Wait yet — simulates the web UI
	// approving/denying before the client's SSE connection ever attaches.
	if err := svc.Deny(context.Background(), requestID); err != nil {
		t.Fatalf("unexpected error denying request: %v", err)
	}

	status, _, err := svc.Wait(context.Background(), requestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != model.CertificateRequestStatusDenied {
		t.Errorf("got status %q, want %q", status, model.CertificateRequestStatusDenied)
	}
}

func TestCertRequestService_Wait_ShouldResumeAfterContextCancellation(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Simulate a dropped SSE connection: Wait's context is canceled while
	// the request is still pending.
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := svc.Wait(cancelCtx, requestID); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// A fresh Wait call (the reconnect) for the same requestID must still
	// work, not error with "no such waiter".
	type waitResult struct {
		status model.CertificateRequestStatus
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		status, _, err := svc.Wait(context.Background(), requestID)
		done <- waitResult{status, err}
	}()

	time.Sleep(10 * time.Millisecond)
	if err := svc.Deny(context.Background(), requestID); err != nil {
		t.Fatalf("unexpected error denying request: %v", err)
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("unexpected error from reconnected Wait: %v", res.err)
		}
		if res.status != model.CertificateRequestStatusDenied {
			t.Errorf("got status %q, want %q", res.status, model.CertificateRequestStatusDenied)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconnected Wait did not unblock after Deny")
	}
}

func TestCertRequestService_Wait_ShouldExpireRequestPastTTL(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Millisecond)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	status, _, err := svc.Wait(context.Background(), requestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != model.CertificateRequestStatusExpired {
		t.Errorf("got status %q, want %q", status, model.CertificateRequestStatusExpired)
	}
}

func TestCertRequestService_Deny_ShouldErrorWhenNotPending(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}
	if err := svc.Deny(context.Background(), requestID); err != nil {
		t.Fatalf("unexpected error on first deny: %v", err)
	}

	if err := svc.Deny(context.Background(), requestID); err == nil {
		t.Fatal("expected an error denying an already-denied request, got nil")
	}
}

func TestCertRequestService_ListPending_ShouldExcludeExpiredRequests(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Millisecond)
	if _, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."}); err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	requests, err := svc.ListPending(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(requests) != 0 {
		t.Errorf("expected the TTL-expired request to be excluded from ListPending, got %d results", len(requests))
	}
}
