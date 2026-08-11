package service

// Test methodology: unit tests for CertRequestService against a real
// in-memory sqlite *gorm.DB (AutoMigrate'd from model.CertificateRequest —
// this exercises the service's query/expiry logic, not full migration
// correctness, which server/bootstrap/db_test.go covers). Tests run in
// parallel where they don't share a DB/service instance.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/mnestor/ssoossh/server/certmsg"
	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// newTestCertRequestService opens an in-memory sqlite DB migrated for
// model.CertificateRequest and returns a CertRequestService backed by it,
// with RequestTTL as given (0 disables expiry). The wake-topic broker is a
// real, non-persistent gochannel pair — matching server/pubsub.New exactly
// (see its Persistent:false doc comment for why) — closed on test cleanup.
func newTestCertRequestService(t *testing.T, ttl time.Duration) *CertRequestService {
	t.Helper()
	return newTestCertRequestServiceWithOptions(t, config.CertificateOptions{RequestTTL: ttl})
}

// newTestCertRequestServiceWithOptions is newTestCertRequestService but
// lets Approve tests control the full per-type policy (Extensions,
// ValidDuration, RequireGroup), not just RequestTTL.
func newTestCertRequestServiceWithOptions(t *testing.T, opts config.CertificateOptions) *CertRequestService {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.CertificateRequest{}); err != nil {
		t.Fatalf("failed to migrate certificate_requests table: %v", err)
	}

	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	svc, err := NewCertRequestService(&config.Config{CertOptions: opts}, db, channel, channel)
	if err != nil {
		t.Fatalf("failed to construct CertRequestService: %v", err)
	}
	return svc
}

func TestCertRequestService_Wait_ShouldReturnErrorForUnknownID(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)

	_, _, _, err := svc.Wait(context.Background(), "does-not-exist")

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
		status, _, _, err := svc.Wait(context.Background(), requestID)
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

	status, _, _, err := svc.Wait(context.Background(), requestID)
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
	if _, _, _, err := svc.Wait(cancelCtx, requestID); !errors.Is(err, context.Canceled) {
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
		status, _, _, err := svc.Wait(context.Background(), requestID)
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

// TestCertRequestService_Wait_ShouldReportApprovedCertificateAsUnavailable
// is the regression test for the ephemeral-certificate design: certificates
// are never persisted, so a client reaching a terminal "approved" row with
// nothing cached (i.e. after a restart) has genuinely missed its delivery.
// It must be told so, not handed a successful outcome with an empty
// certificate it might not check.
func TestCertRequestService_Wait_ShouldReportApprovedCertificateAsUnavailable(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Resolve the row directly, leaving the in-memory cache cold — exactly
	// the state a restarted server comes back to.
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusApproved).Error; err != nil {
		t.Fatalf("failed to mark request approved: %v", err)
	}

	_, certificate, _, err := svc.Wait(context.Background(), requestID)

	var unavailable *errorresponses.CertificateUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("expected a *errorresponses.CertificateUnavailableError, got %T: %v", err, err)
	}
	if certificate != "" {
		t.Errorf("expected no certificate alongside the error, got %q", certificate)
	}
}

// TestCertRequestService_Wait_ShouldRebuildEnrollmentTokenFromTheRow is the
// counterpart: unlike a certificate, an enrollment token *is* durable, so a
// cold cache must be answered from the database rather than returning an
// empty code.
func TestCertRequestService_Wait_ShouldRebuildEnrollmentTokenFromTheRow(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Updates(map[string]any{
			"status":           model.CertificateRequestStatusEnrolled,
			"enrollment_token": "token-abc",
		}).Error; err != nil {
		t.Fatalf("failed to mark request enrolled: %v", err)
	}

	status, _, code, err := svc.Wait(context.Background(), requestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != model.CertificateRequestStatusEnrolled {
		t.Errorf("got status %q, want %q", status, model.CertificateRequestStatusEnrolled)
	}
	if code != "token-abc" {
		t.Errorf("got code %q, want %q", code, "token-abc")
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

	status, _, _, err := svc.Wait(context.Background(), requestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != model.CertificateRequestStatusExpired {
		t.Errorf("got status %q, want %q", status, model.CertificateRequestStatusExpired)
	}
}

// TestCertRequestService_Wait_ShouldReceiveWakeMessageViaPubSub exercises
// the actual pub/sub wake path (not just the resolved-cache fast path):
// Wait is left blocked in its select on the subscription for a beat before
// Deny publishes, so the unblock has to come from the subscribed message
// arriving, not from resolved already being populated when Wait's loop
// started.
func TestCertRequestService_Wait_ShouldReceiveWakeMessageViaPubSub(t *testing.T) {
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
		status, _, _, err := svc.Wait(context.Background(), requestID)
		done <- waitResult{status, err}
	}()

	// Long enough for Wait to have subscribed and reached its blocking
	// select before Deny (and therefore notifyWaiter's publish) runs.
	time.Sleep(50 * time.Millisecond)

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
		t.Fatal("Wait did not unblock via the pub/sub wake message")
	}
}

func TestCertRequestService_Approve_ShouldNarrowExtensionsAndPublishSigningJob(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		User: config.CertOptionsUser{
			Extensions:    []string{"permit-pty"},
			ValidDuration: time.Hour,
		},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA...",
		RequestedOptions: RequestedOptions{
			Extensions: []string{"permit-pty", "permit-agent-forwarding"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	messages, err := svc.subscriber.Subscribe(ctx, certmsg.SignQueueTopic)
	if err != nil {
		t.Fatalf("unexpected error subscribing to the sign queue: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-1", Email: "alice@example.com"}
	if err := svc.Approve(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusSigning {
		t.Errorf("got status %q, want %q", req.Status, model.CertificateRequestStatusSigning)
	}

	select {
	case msg := <-messages:
		var job certmsg.SigningJob
		if err := json.Unmarshal(msg.Payload, &job); err != nil {
			t.Fatalf("failed to decode signing job: %v", err)
		}
		msg.Ack()

		if job.RequestID != requestID {
			t.Errorf("got RequestID %q, want %q", job.RequestID, requestID)
		}
		if len(job.RequestedOptions.Extensions) != 1 || job.RequestedOptions.Extensions[0] != "permit-pty" {
			t.Errorf("expected extensions to be narrowed to [\"permit-pty\"], got %v", job.RequestedOptions.Extensions)
		}
		if len(job.Principals) != 1 || job.Principals[0] != "alice" {
			t.Errorf("expected principals to be [\"alice\"], got %v", job.Principals)
		}
		if job.KeyID != "alice" {
			t.Errorf("got KeyID %q, want %q (default template is username)", job.KeyID, "alice")
		}
		if !job.ValidBefore.After(job.ValidAfter) {
			t.Errorf("expected ValidBefore to be after ValidAfter, got %v / %v", job.ValidBefore, job.ValidAfter)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for the signing job to be published")
	}
}

func TestCertRequestService_Approve_ShouldRejectWhenIdentityLacksRequiredGroup(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{RequireGroup: "admins", ValidDuration: time.Hour},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: "ssh-ed25519 AAAA...",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "alice", Groups: []string{"engineers"}}
	if err := svc.Approve(context.Background(), requestID, identity); err == nil {
		t.Fatal("expected an error approving without the required group, got nil")
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusPending {
		t.Errorf("expected the request to remain pending after a rejected approval, got %q", req.Status)
	}
}

func TestCertRequestService_Approve_ShouldEnrollServiceRequestsInsteadOfQueueing(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		Service: config.CertOptionsService{
			Extensions:    []string{"permit-pty"},
			ValidDuration: time.Hour,
		},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeService,
		PublicKey: "ssh-ed25519 AAAA...",
		RequestedOptions: RequestedOptions{
			Extensions: []string{"permit-pty", "permit-agent-forwarding"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Wait is already blocked before Approve runs, to prove the wake
	// comes directly from Approve itself (no signer/queue round trip) —
	// same shape as TestCertRequestService_Wait_ShouldReceiveWakeMessageViaPubSub.
	type waitResult struct {
		status model.CertificateRequestStatus
		code   string
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		status, _, code, err := svc.Wait(context.Background(), requestID)
		done <- waitResult{status, code, err}
	}()
	time.Sleep(50 * time.Millisecond)

	identity := &Identity{Username: "alice"}
	if err := svc.Approve(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	var res waitResult
	select {
	case res = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not unblock after Approve enrolled the request")
	}
	if res.err != nil {
		t.Fatalf("unexpected error from Wait: %v", res.err)
	}
	if res.status != model.CertificateRequestStatusEnrolled {
		t.Errorf("got status %q, want %q", res.status, model.CertificateRequestStatusEnrolled)
	}
	if res.code == "" {
		t.Error("expected a non-empty enrollment code")
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusEnrolled {
		t.Errorf("got DB status %q, want %q", req.Status, model.CertificateRequestStatusEnrolled)
	}
	if req.EnrollmentToken == "" {
		t.Error("expected EnrollmentToken to be set on the row")
	}
	if req.EnrollmentToken != res.code {
		t.Errorf("expected Wait's code to match the persisted EnrollmentToken, got %q vs %q", res.code, req.EnrollmentToken)
	}

	var narrowed RequestedOptions
	if err := json.Unmarshal([]byte(req.RequestedOptions), &narrowed); err != nil {
		t.Fatalf("failed to decode persisted requested options: %v", err)
	}
	if len(narrowed.Extensions) != 1 || narrowed.Extensions[0] != "permit-pty" {
		t.Errorf("expected the persisted options to be narrowed to [\"permit-pty\"], got %v", narrowed.Extensions)
	}
}

func TestCertRequestService_Approve_ShouldErrorWhenNotPending(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		User: config.CertOptionsUser{ValidDuration: time.Hour},
	})
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "alice"}
	if err := svc.Approve(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error on first approve: %v", err)
	}

	if err := svc.Approve(context.Background(), requestID, identity); err == nil {
		t.Fatal("expected an error approving an already-signing request, got nil")
	}
}

func TestResolveCertOptions_ShouldDropForceCommandAndSourceAddresses(t *testing.T) {
	t.Parallel()

	narrowed, _, _, err := resolveCertOptions(config.CertificateOptions{
		User: config.CertOptionsUser{Extensions: []string{"permit-pty"}},
	}, model.CertificateTypeUser, RequestedOptions{
		Extensions:      []string{"permit-pty"},
		ForceCommand:    "/bin/true",
		SourceAddresses: []string{"10.0.0.1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if narrowed.ForceCommand != "" {
		t.Errorf("expected ForceCommand to be dropped, got %q", narrowed.ForceCommand)
	}
	if narrowed.SourceAddresses != nil {
		t.Errorf("expected SourceAddresses to be dropped, got %v", narrowed.SourceAddresses)
	}
}

func TestResolveCertOptions_ShouldOnlyGrantNoTouchRequiredForService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		certType model.CertificateType
		want     bool
	}{
		{"should grant for service", model.CertificateTypeService, true},
		{"should not grant for user", model.CertificateTypeUser, false},
		{"should not grant for host", model.CertificateTypeHost, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			narrowed, _, _, err := resolveCertOptions(config.CertificateOptions{}, tt.certType, RequestedOptions{NoTouchRequired: true})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if narrowed.NoTouchRequired != tt.want {
				t.Errorf("got NoTouchRequired %v, want %v", narrowed.NoTouchRequired, tt.want)
			}
		})
	}
}

func TestResolvePrincipals_ShouldUseHostnameForHostCertificates(t *testing.T) {
	t.Parallel()

	got := resolvePrincipals(model.CertificateTypeHost, "db01.internal", &Identity{Username: "alice"})
	if len(got) != 1 || got[0] != "db01.internal" {
		t.Errorf("got %v, want [\"db01.internal\"]", got)
	}
}

func TestResolvePrincipals_ShouldUseUsernameForUserAndServiceCertificates(t *testing.T) {
	t.Parallel()

	for _, certType := range []model.CertificateType{model.CertificateTypeUser, model.CertificateTypeService} {
		got := resolvePrincipals(certType, "db01.internal", &Identity{Username: "alice"})
		if len(got) != 1 || got[0] != "alice" {
			t.Errorf("for %s: got %v, want [\"alice\"]", certType, got)
		}
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
