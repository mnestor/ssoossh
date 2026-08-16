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
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
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
	if err := db.AutoMigrate(&model.CertificateRequest{}, &model.User{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
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

// closeUnderlyingDB closes db's connection, so any subsequent query fails
// with a real "sql: database is closed" error — the most direct way to
// exercise this package's generic-DB-error branches without a mock: every
// call still goes through the real *gorm.DB and the real sqlite driver,
// just against a connection that's genuinely gone.
func closeUnderlyingDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("failed to get underlying sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("failed to close the database: %v", err)
	}
}

// seedUser inserts the users row Approve resolves the approving identity
// against, returning its ID. Approve binds a request to a user (see
// bindRequester), so every approval path needs one to exist.
func seedUser(t *testing.T, db *gorm.DB, subject string) string {
	t.Helper()

	user := model.User{
		ID:        uuid.NewString(),
		Subject:   subject,
		Username:  "seeded-" + subject,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("failed to seed user %q: %v", subject, err)
	}
	return user.ID
}

// TestCertRequestService_ShouldSurfaceGenericDBErrors covers the
// generic-database-error branch in CreateRequest, Detail, Approve,
// bindRequester (both its guarded UPDATE and its racing-claim re-read),
// resolveUserID, approveServiceEnrollment, approveForSigning, and Deny —
// each distinct from that same method's not-found/not-pending handling,
// which is tested elsewhere. Closing the underlying connection (see
// closeUnderlyingDB) makes every one of these fail the same real way, so
// one table covers all of them without a mock for each.
func TestCertRequestService_ShouldSurfaceGenericDBErrors(t *testing.T) {
	t.Parallel()

	t.Run("CreateRequest", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, 0)
		closeUnderlyingDB(t, svc.db)

		if _, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."}); err == nil {
			t.Error("CreateRequest() error = nil, want error")
		}
	})

	t.Run("Detail", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		identity := &Identity{Username: "alice", Subject: "sub-alice"}
		seedUser(t, svc.db, identity.Subject)
		requestID := mustCreateUserRequest(t, svc)
		closeUnderlyingDB(t, svc.db)

		if _, err := svc.Detail(context.Background(), requestID, identity); err == nil {
			t.Error("Detail() error = nil, want error")
		}
	})

	t.Run("Approve", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		identity := &Identity{Username: "alice", Subject: "sub-alice"}
		seedUser(t, svc.db, identity.Subject)
		requestID := mustCreateUserRequest(t, svc)
		closeUnderlyingDB(t, svc.db)

		if err := svc.Approve(context.Background(), requestID, identity); err == nil {
			t.Error("Approve() error = nil, want error")
		}
	})

	t.Run("bindRequester's guarded UPDATE", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		identity := &Identity{Username: "alice", Subject: "sub-alice"}
		seedUser(t, svc.db, identity.Subject)
		requestID := mustCreateUserRequest(t, svc)
		closeUnderlyingDB(t, svc.db)

		req := &model.CertificateRequest{ID: requestID, UserID: nil}
		if err := svc.bindRequester(context.Background(), req, identity); err == nil {
			t.Error("bindRequester() error = nil, want error")
		}
	})

	t.Run("resolveUserID", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, 0)
		closeUnderlyingDB(t, svc.db)

		if _, err := svc.resolveUserID(context.Background(), &Identity{Subject: "sub-alice"}); err == nil {
			t.Error("resolveUserID() error = nil, want error")
		}
	})

	t.Run("approveServiceEnrollment", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeService, PublicKey: "ssh-ed25519 AAAA... svc"})
		if err != nil {
			t.Fatalf("unexpected error creating request: %v", err)
		}
		closeUnderlyingDB(t, svc.db)

		if err := svc.approveServiceEnrollment(context.Background(), requestID, RequestedOptions{}); err == nil {
			t.Error("approveServiceEnrollment() error = nil, want error")
		}
	})

	t.Run("approveForSigning", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, time.Hour)
		identity := &Identity{Username: "alice", Subject: "sub-alice"}
		requestID := mustCreateUserRequest(t, svc)
		var req model.CertificateRequest
		if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
			t.Fatalf("failed to reload request: %v", err)
		}
		closeUnderlyingDB(t, svc.db)

		if err := svc.approveForSigning(context.Background(), req, identity, RequestedOptions{}, time.Hour); err == nil {
			t.Error("approveForSigning() error = nil, want error")
		}
	})

	t.Run("Deny", func(t *testing.T) {
		t.Parallel()
		svc := newTestCertRequestService(t, 0)
		requestID := mustCreateUserRequest(t, svc)
		closeUnderlyingDB(t, svc.db)

		if err := svc.Deny(context.Background(), requestID); err == nil {
			t.Error("Deny() error = nil, want error")
		}
	})
}

// TestNewCertRequestService_ShouldRejectAMalformedKeyIDTemplate covers the
// newKeyIDTemplates error propagating out of the constructor: a bad
// template (unparseable syntax, or referencing a field
// keyIDTemplateData doesn't have) must fail startup rather than the first
// approval.
func TestNewCertRequestService_ShouldRejectAMalformedKeyIDTemplate(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	opts := config.CertificateOptions{}
	opts.User.KeyIDTemplate = "{{.NoSuchField}}"

	if _, err := NewCertRequestService(&config.Config{CertOptions: opts}, db, channel, channel); err == nil {
		t.Error("NewCertRequestService() error = nil, want error for a key ID template referencing an unknown field")
	}
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

// TestCertRequestService_Wait_ShouldReturnCtxErrOnGenuineCancellation covers
// the blocking select's ctx.Done() arm specifically — distinct from
// TestCertRequestService_Wait_ShouldResumeAfterContextCancellation, which
// cancels ctx *before* calling Wait and so never reaches the select at all
// (lookupRequest's own DB query fails first, on the already-canceled
// context). This cancels concurrently, after Wait is genuinely blocked.
func TestCertRequestService_Wait_ShouldReturnCtxErrOnGenuineCancellation(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, _, _, err = svc.Wait(ctx, requestID)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// failingSubscriber is a message.Subscriber whose Subscribe call always
// fails, standing in for a broker that's unreachable.
type failingSubscriber struct{}

func (failingSubscriber) Subscribe(_ context.Context, _ string) (<-chan *message.Message, error) {
	return nil, errors.New("subscription unavailable")
}
func (failingSubscriber) Close() error { return nil }

func TestCertRequestService_Wait_ShouldSurfaceASubscribeFailure(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.CertificateRequest{}, &model.User{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	svc, err := NewCertRequestService(&config.Config{}, db, channel, failingSubscriber{})
	if err != nil {
		t.Fatalf("failed to construct CertRequestService: %v", err)
	}

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	if _, _, _, err := svc.Wait(context.Background(), requestID); err == nil {
		t.Error("Wait() error = nil, want error when the subscriber is unreachable")
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
	seedUser(t, svc.db, identity.Subject)
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

	identity := &Identity{Username: "alice", Subject: "sub-1", Groups: []string{"engineers"}}
	seedUser(t, svc.db, identity.Subject)
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

// TestCertRequestService_Approve_ShouldDenyPAMWhenRequireGroupUnconfigured
// pins the one deliberate divergence from every other certificate type: an
// unset cert_options.pam.require_group denies rather than opens, because
// "who may sudo" has to fail closed (see CertOptionsPAM.RequireGroup). The
// identity here has no groups at all, so this also proves the rejection
// isn't just an ordinary group-membership failure in disguise.
func TestCertRequestService_Approve_ShouldDenyPAMWhenRequireGroupUnconfigured(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		PAM: config.CertOptionsPAM{ValidDuration: 30 * time.Second},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypePAM,
		PublicKey: "ssh-ed25519 AAAA...",
		Username:  "mnestor",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	identity := &Identity{Username: "mike.nestor", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity); err == nil {
		t.Fatal("expected an error approving a PAM request with no configured require_group, got nil")
	}
}

// TestCertRequestService_Approve_ShouldQueuePAMRequestWithLocalUsernameAsPrincipal
// is the assertion the phase 4 plan calls out by name: the issued
// certificate must name the local account the module authenticated
// (req.Username), not the approver's OIDC username, with the two set to
// different values. Also checks the PAM-only defaults: extensions dropped
// (nothing configured-permitted) and the PAM key ID template rather than
// the user one.
func TestCertRequestService_Approve_ShouldQueuePAMRequestWithLocalUsernameAsPrincipal(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, config.CertificateOptions{
		PAM: config.CertOptionsPAM{RequireGroup: "sudoers", ValidDuration: 30 * time.Second},
	})

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypePAM,
		PublicKey: "ssh-ed25519 AAAA...",
		Username:  "mnestor",
		RequestedOptions: RequestedOptions{
			Extensions: []string{"permit-pty"},
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

	identity := &Identity{Username: "mike.nestor", Subject: "sub-1", Groups: []string{"sudoers"}}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error approving request: %v", err)
	}

	select {
	case msg := <-messages:
		var job certmsg.SigningJob
		if err := json.Unmarshal(msg.Payload, &job); err != nil {
			t.Fatalf("failed to decode signing job: %v", err)
		}
		msg.Ack()

		if len(job.Principals) != 1 || job.Principals[0] != "mnestor" {
			t.Errorf("expected principals to be [\"mnestor\"] (the local username), got %v", job.Principals)
		}
		// KeyID is the audit-log label and stays keyed on the approver's
		// identity, same as every other type — it's the *principal* that
		// must diverge to the local username (checked above). This is what
		// makes "pam:mike.nestor" distinguishable from a login by the same
		// person in an sshd/sudo audit log (see CertOptionsPAM.KeyIDTemplate).
		if job.KeyID != "pam:mike.nestor" {
			t.Errorf("got KeyID %q, want %q (PAM's own default template, keyed on the approver)", job.KeyID, "pam:mike.nestor")
		}
		if len(job.RequestedOptions.Extensions) != 0 {
			t.Errorf("expected no extensions (none configured-permitted), got %v", job.RequestedOptions.Extensions)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for the signing job to be published")
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

	identity := &Identity{Username: "alice", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)
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

func TestCertRequestService_Approve_ShouldReturnNotFoundForAnUnknownID(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)

	err := svc.Approve(context.Background(), "does-not-exist", &Identity{Username: "alice", Subject: "sub-alice"})

	var notFound *errorresponses.NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected a *errorresponses.NotFoundError, got %T: %v", err, err)
	}
}

// TestCertRequestService_Approve_ShouldRejectHostCertificates covers
// Approve's default switch case: host certificates aren't issuable yet (the
// signer only handles user and PAM), so approving one must fail immediately
// rather than queuing a job the signer would refuse.
func TestCertRequestService_Approve_ShouldRejectHostCertificates(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeHost,
		PublicKey: "ssh-ed25519 AAAA... host",
		Hostname:  "web-01.example.com",
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	if err := svc.Approve(context.Background(), requestID, identity); err == nil {
		t.Fatal("expected approving a host certificate request to fail: host issuance isn't supported yet")
	}
}

// TestCertRequestService_Approve_ShouldSurfaceACorruptOptionsColumn is
// Approve's counterpart to the same Detail test: a request row whose
// requested_options is somehow unparseable must fail approval rather than
// silently treat the requester as having asked for nothing.
func TestCertRequestService_Approve_ShouldSurfaceACorruptOptionsColumn(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)
	requestID := mustCreateUserRequest(t, svc)

	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("requested_options", "not json").Error; err != nil {
		t.Fatalf("failed to corrupt requested_options: %v", err)
	}

	if err := svc.Approve(context.Background(), requestID, identity); err == nil {
		t.Error("Approve() error = nil, want error for an unparseable requested_options column")
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

	identity := &Identity{Username: "alice", Subject: "sub-1"}
	seedUser(t, svc.db, identity.Subject)
	if err := svc.Approve(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error on first approve: %v", err)
	}

	if err := svc.Approve(context.Background(), requestID, identity); err == nil {
		t.Fatal("expected an error approving an already-signing request, got nil")
	}
}

// TestResolveCertOptions_ShouldNarrowPAMExtensionsAndUsePAMDuration confirms
// PAM reads from its own config section — cert_options.pam.extensions being
// empty (the documented default) drops a requested extension entirely
// rather than granting it, and PAM's ValidDuration is used rather than
// User's.
func TestResolveCertOptions_ShouldNarrowPAMExtensionsAndUsePAMDuration(t *testing.T) {
	t.Parallel()

	narrowed, validDuration, requireGroup, err := resolveCertOptions(config.CertificateOptions{
		User: config.CertOptionsUser{Extensions: []string{"permit-pty"}, ValidDuration: time.Hour},
		PAM:  config.CertOptionsPAM{RequireGroup: "sudoers", ValidDuration: 30 * time.Second},
	}, model.CertificateTypePAM, RequestedOptions{Extensions: []string{"permit-pty"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if narrowed.Extensions != nil {
		t.Errorf("expected no PAM extensions to survive narrowing, got %v", narrowed.Extensions)
	}
	if validDuration != 30*time.Second {
		t.Errorf("got ValidDuration %v, want PAM's own 30s, not User's", validDuration)
	}
	if requireGroup != "sudoers" {
		t.Errorf("got RequireGroup %q, want %q", requireGroup, "sudoers")
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

func TestResolveCertOptions_ShouldRejectAnUnsupportedCertificateType(t *testing.T) {
	t.Parallel()

	_, _, _, err := resolveCertOptions(config.CertificateOptions{}, model.CertificateType("bogus"), RequestedOptions{})
	if err == nil {
		t.Error("resolveCertOptions() error = nil, want error for an unsupported certificate type")
	}
}

func TestResolvePrincipals_ShouldUseHostnameForHostCertificates(t *testing.T) {
	t.Parallel()

	got := resolvePrincipals(model.CertificateTypeHost, "db01.internal", "", &Identity{Username: "alice"})
	if len(got) != 1 || got[0] != "db01.internal" {
		t.Errorf("got %v, want [\"db01.internal\"]", got)
	}
}

func TestResolvePrincipals_ShouldUseUsernameForUserAndServiceCertificates(t *testing.T) {
	t.Parallel()

	for _, certType := range []model.CertificateType{model.CertificateTypeUser, model.CertificateTypeService} {
		got := resolvePrincipals(certType, "db01.internal", "", &Identity{Username: "alice"})
		if len(got) != 1 || got[0] != "alice" {
			t.Errorf("for %s: got %v, want [\"alice\"]", certType, got)
		}
	}
}

// TestResolvePrincipals_ShouldUsePAMUsernameNotIdentity is the assertion
// that catches the wrong reading of docs/release-phase4-pam-server.md's
// "Principal resolution" section: PAM certificates must name the local
// account the module authenticated, not the approver's OIDC identity, even
// when those two names differ.
func TestResolvePrincipals_ShouldUsePAMUsernameNotIdentity(t *testing.T) {
	t.Parallel()

	got := resolvePrincipals(model.CertificateTypePAM, "", "mnestor", &Identity{Username: "mike.nestor"})
	if len(got) != 1 || got[0] != "mnestor" {
		t.Errorf("got %v, want [\"mnestor\"]", got)
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

// TestCertRequestService_Approve_ShouldRefuseARequestPastTTL keeps the
// TTL-expiry assertion that used to run through ListPending, repointed at
// Approve now that requests are never listed. This is the half that matters:
// an expired request must not be approvable, whatever route reaches it.
func TestCertRequestService_Approve_ShouldRefuseARequestPastTTL(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Millisecond)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	if err := svc.Approve(context.Background(), requestID, &Identity{Username: "alice", Subject: "sub-alice"}); err == nil {
		t.Error("expected a TTL-expired request to be refused by Approve")
	}
}

// TestCertRequestService_Approve_ShouldBindTheRequestToTheApprovingUser pins
// the claim half of the binding: an unclaimed request becomes the approver's.
func TestCertRequestService_Approve_ShouldBindTheRequestToTheApprovingUser(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	wantUserID := seedUser(t, svc.db, identity.Subject)

	requestID := mustCreateUserRequest(t, svc)

	if err := svc.Approve(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error approving: %v", err)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading the request back: %v", err)
	}
	if req.UserID == nil {
		t.Fatal("expected the request to be bound to a user")
	}
	if *req.UserID != wantUserID {
		t.Errorf("got user_id %q, want %q", *req.UserID, wantUserID)
	}
}

// TestCertRequestService_Approve_ShouldRejectAnApproverWhoIsNotTheRequester
// is the reason the binding exists. The certificate carries the *approver's*
// principals over the *requester's* public key, so letting a second user
// approve someone else's pending request would hand that requester a
// certificate impersonating the approver.
func TestCertRequestService_Approve_ShouldRejectAnApproverWhoIsNotTheRequester(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	alice := &Identity{Username: "alice", Subject: "sub-alice"}
	bob := &Identity{Username: "bob", Subject: "sub-bob"}
	seedUser(t, svc.db, alice.Subject)
	seedUser(t, svc.db, bob.Subject)

	requestID := mustCreateUserRequest(t, svc)

	// Bind it to alice while leaving it pending — the state a request is in
	// once alice has opened the approval page but not yet decided.
	if err := svc.bindRequester(context.Background(), &model.CertificateRequest{ID: requestID}, alice); err != nil {
		t.Fatalf("unexpected error binding the request to alice: %v", err)
	}

	// Bob must not be able to act on it, even though he is authenticated.
	err := svc.Approve(context.Background(), requestID, bob)
	if err == nil {
		t.Fatal("expected bob's approve to fail on a request bound to alice")
	}

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Errorf("got error %v, want a ForbiddenError", err)
	}
}

// TestCertRequestService_Approve_ShouldRejectAnIdentityWithNoUserRecord
// fails closed: without a users row there is nothing to bind to, so the
// request must not be approved rather than silently left unbound.
func TestCertRequestService_Approve_ShouldRejectAnIdentityWithNoUserRecord(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	requestID := mustCreateUserRequest(t, svc)

	err := svc.Approve(context.Background(), requestID, &Identity{Username: "ghost", Subject: "sub-ghost"})
	if err == nil {
		t.Fatal("expected approve to fail for an identity with no users row")
	}

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Errorf("got error %v, want a ForbiddenError", err)
	}
}

// TestCertRequestService_Approve_ShouldEnforceRequireGroupOnUserCertificates
// covers both directions of the newly reachable gate, including the
// backward-compatible case where an empty value restricts nobody.
func TestCertRequestService_Approve_ShouldEnforceRequireGroupOnUserCertificates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		requireGroup string
		groups       []string
		wantErr      bool
	}{
		{
			name:         "should allow anyone when require_group is unset",
			requireGroup: "",
			groups:       nil,
			wantErr:      false,
		},
		{
			name:         "should allow a member of the required group",
			requireGroup: "ssh-users",
			groups:       []string{"other", "ssh-users"},
			wantErr:      false,
		},
		{
			name:         "should reject a non-member",
			requireGroup: "ssh-users",
			groups:       []string{"other"},
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := config.CertificateOptions{}
			opts.User.ValidDuration = time.Hour
			opts.User.RequireGroup = tt.requireGroup

			svc := newTestCertRequestServiceWithOptions(t, opts)
			identity := &Identity{Username: "alice", Subject: "sub-alice", Groups: tt.groups}
			seedUser(t, svc.db, identity.Subject)

			requestID := mustCreateUserRequest(t, svc)

			err := svc.Approve(context.Background(), requestID, identity)
			if tt.wantErr && err == nil {
				t.Error("expected approve to be rejected by require_group")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// mustCreateUserRequest creates a pending user certificate request and
// returns its ID.
func mustCreateUserRequest(t *testing.T, svc *CertRequestService) string {
	t.Helper()

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA test",
	})
	if err != nil {
		t.Fatalf("failed to create certificate request: %v", err)
	}
	return requestID
}

// TestCertRequestService_Detail_ShouldBindOnFirstView is the reason Detail
// binds at all: the approval page loads before anyone decides, so a request
// should be owned from the moment its owner opens it rather than only once
// they click approve.
func TestCertRequestService_Detail_ShouldBindOnFirstView(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	wantUserID := seedUser(t, svc.db, identity.Subject)

	requestID := mustCreateUserRequest(t, svc)

	if _, err := svc.Detail(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading the request back: %v", err)
	}
	if req.UserID == nil || *req.UserID != wantUserID {
		t.Errorf("expected viewing the request to bind it to alice, got %v", req.UserID)
	}
}

// TestCertRequestService_Detail_ShouldRejectAViewerWhoIsNotTheOwner means a
// second user finds out on page load rather than after clicking approve.
func TestCertRequestService_Detail_ShouldRejectAViewerWhoIsNotTheOwner(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	alice := &Identity{Username: "alice", Subject: "sub-alice"}
	bob := &Identity{Username: "bob", Subject: "sub-bob"}
	seedUser(t, svc.db, alice.Subject)
	seedUser(t, svc.db, bob.Subject)

	requestID := mustCreateUserRequest(t, svc)

	if _, err := svc.Detail(context.Background(), requestID, alice); err != nil {
		t.Fatalf("unexpected error on alice's view: %v", err)
	}

	_, err := svc.Detail(context.Background(), requestID, bob)
	if err == nil {
		t.Fatal("expected bob's view of alice's request to be refused")
	}

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Errorf("got error %v, want a ForbiddenError", err)
	}
}

// TestCertRequestService_Detail_ShouldReportRequestedAndNarrowedSeparately
// is what lets the approval page show a human that an option they asked for
// is being trimmed — the hard constraint is that this is visible *before*
// approval, not discovered afterwards.
func TestCertRequestService_Detail_ShouldReportRequestedAndNarrowedSeparately(t *testing.T) {
	t.Parallel()

	opts := config.CertificateOptions{}
	opts.User.ValidDuration = 10 * time.Hour
	opts.User.Extensions = []string{"permit-pty"}

	svc := newTestCertRequestServiceWithOptions(t, opts)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{
		Type:      model.CertificateTypeUser,
		PublicKey: "ssh-ed25519 AAAA test",
		RequestedOptions: RequestedOptions{
			Extensions: []string{"permit-pty", "permit-port-forwarding"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	detail, err := svc.Detail(context.Background(), requestID, identity)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(detail.Requested.Extensions) != 2 {
		t.Errorf("got requested extensions %v, want both as submitted", detail.Requested.Extensions)
	}
	if len(detail.Narrowed.Extensions) != 1 || detail.Narrowed.Extensions[0] != "permit-pty" {
		t.Errorf("got narrowed extensions %v, want only the configured-permitted one", detail.Narrowed.Extensions)
	}
	if detail.ValidDuration != 10*time.Hour {
		t.Errorf("got valid duration %v, want 10h", detail.ValidDuration)
	}
	if len(detail.Principals) != 1 || detail.Principals[0] != "alice" {
		t.Errorf("got principals %v, want [alice]", detail.Principals)
	}
}

func TestCertRequestService_Detail_ShouldReturnNotFoundForAnUnknownID(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	seedUser(t, svc.db, "sub-alice")

	_, err := svc.Detail(context.Background(), "does-not-exist", &Identity{Subject: "sub-alice"})

	var notFound *errorresponses.NotFoundError
	if !errors.As(err, &notFound) {
		t.Errorf("got error %v, want a NotFoundError", err)
	}
}

// TestCertRequestService_Detail_ShouldSurfaceACorruptOptionsColumn covers
// the requested_options decode failure: the column is free-form JSON with
// no DB-level schema behind it, so a row that somehow ended up with
// unparseable content must surface as an error rather than panic or
// silently zero out the requester's options.
func TestCertRequestService_Detail_ShouldSurfaceACorruptOptionsColumn(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)
	requestID := mustCreateUserRequest(t, svc)

	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("requested_options", "not json").Error; err != nil {
		t.Fatalf("failed to corrupt requested_options: %v", err)
	}

	if _, err := svc.Detail(context.Background(), requestID, identity); err == nil {
		t.Error("Detail() error = nil, want error for an unparseable requested_options column")
	}
}

// TestCertRequestService_Detail_ShouldSurfaceAnUnsupportedStoredType covers
// Detail's own resolveCertOptions error path: CreateRequest stores
// whatever Type string it's given with no enum validation, so a row that
// somehow ended up with an unrecognized type must surface as an error
// rather than a zero-value policy.
func TestCertRequestService_Detail_ShouldSurfaceAnUnsupportedStoredType(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateType("bogus"), PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	if _, err := svc.Detail(context.Background(), requestID, identity); err == nil {
		t.Error("Detail() error = nil, want error for an unsupported stored certificate type")
	}
}

// TestCertRequestService_Approve_ShouldSurfaceAnUnsupportedStoredType is
// Approve's counterpart to the Detail test above.
func TestCertRequestService_Approve_ShouldSurfaceAnUnsupportedStoredType(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)

	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateType("bogus"), PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	if err := svc.Approve(context.Background(), requestID, identity); err == nil {
		t.Error("Approve() error = nil, want error for an unsupported stored certificate type")
	}
}

// TestBindRequester_ShouldNoOpForARepeatViewByTheSameOwner covers the
// already-bound-to-this-user early return: Detail's binding is idempotent
// for the owner (a second page load isn't a second claim attempt).
func TestBindRequester_ShouldNoOpForARepeatViewByTheSameOwner(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)
	requestID := mustCreateUserRequest(t, svc)

	if _, err := svc.Detail(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error on first view: %v", err)
	}
	// Second view by the same owner must succeed too, not just the first.
	if _, err := svc.Detail(context.Background(), requestID, identity); err != nil {
		t.Fatalf("unexpected error on repeat view by the owner: %v", err)
	}
}

// TestBindRequester_ShouldDetectARacingClaim covers the guarded-UPDATE's
// RowsAffected==0 path deterministically: rather than racing two real
// goroutines (inherently timing-dependent), it seeds the DB row as already
// claimed by a different user before calling bindRequester with an in-memory
// req that still thinks the row is unclaimed — exactly the state a second
// caller would observe if it lost a real race between reading the row and
// issuing its own guarded UPDATE.
func TestBindRequester_ShouldDetectARacingClaim(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	winnerID := seedUser(t, svc.db, "sub-winner")
	loser := &Identity{Username: "loser", Subject: "sub-loser"}
	seedUser(t, svc.db, loser.Subject)
	requestID := mustCreateUserRequest(t, svc)

	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("user_id", winnerID).Error; err != nil {
		t.Fatalf("failed to seed the winning claim: %v", err)
	}

	req := &model.CertificateRequest{ID: requestID, UserID: nil}
	err := svc.bindRequester(context.Background(), req, loser)

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("got error %v (%T), want a ForbiddenError for a request claimed by someone else", err, err)
	}
}

// TestApproveServiceEnrollment_ShouldRefuseARequestThatIsNoLongerPending
// covers approveServiceEnrollment's own guarded-UPDATE RowsAffected==0
// branch directly. Approve's own top-level pending check (line ~291) always
// catches a non-pending row first when called through Approve — including
// with a genuine second Approve call — so this branch is only reachable via
// a real race between two concurrent Approve calls both passing that check
// before either commits. Calling approveServiceEnrollment directly (an
// unexported, in-package-testable method) after flipping the row's status
// out from under it reproduces exactly that race deterministically.
func TestApproveServiceEnrollment_ShouldRefuseARequestThatIsNoLongerPending(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeService, PublicKey: "ssh-ed25519 AAAA... svc"})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusDenied).Error; err != nil {
		t.Fatalf("failed to flip the request's status: %v", err)
	}

	if err := svc.approveServiceEnrollment(context.Background(), requestID, RequestedOptions{}); err == nil {
		t.Error("approveServiceEnrollment() error = nil, want error for a request that lost the pending race")
	}
}

// TestApproveForSigning_ShouldRefuseARequestThatIsNoLongerPending is
// approveForSigning's counterpart to
// TestApproveServiceEnrollment_ShouldRefuseARequestThatIsNoLongerPending —
// same reasoning, same technique: Approve's own top-level check makes this
// guard only reachable via a genuine race, reproduced deterministically by
// calling approveForSigning directly after flipping the row's status.
func TestApproveForSigning_ShouldRefuseARequestThatIsNoLongerPending(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	requestID := mustCreateUserRequest(t, svc)
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusDenied).Error; err != nil {
		t.Fatalf("failed to flip the request's status: %v", err)
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("failed to reload request: %v", err)
	}

	if err := svc.approveForSigning(context.Background(), req, identity, RequestedOptions{}, time.Hour); err == nil {
		t.Error("approveForSigning() error = nil, want error for a request that lost the pending race")
	}
}

// failingPublisher is a message.Publisher whose Publish call always fails,
// standing in for a broker that's unreachable at approval time.
type failingPublisher struct{}

func (failingPublisher) Publish(_ string, _ ...*message.Message) error {
	return errors.New("publish unavailable")
}
func (failingPublisher) Close() error { return nil }

// TestCertRequestService_Approve_ShouldSurfaceAPublishFailure covers
// approveForSigning's own publish error: the row is already marked Signing
// by this point (left for the invalidation sweep to catch, per the doc
// comment on the call site) — Approve itself must still report the failure
// to its caller rather than claim success.
func TestCertRequestService_Approve_ShouldSurfaceAPublishFailure(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.CertificateRequest{}, &model.User{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	svc, err := NewCertRequestService(&config.Config{CertOptions: config.CertificateOptions{
		User: config.CertOptionsUser{ValidDuration: time.Hour},
	}}, db, failingPublisher{}, channel)
	if err != nil {
		t.Fatalf("failed to construct CertRequestService: %v", err)
	}

	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)
	requestID := mustCreateUserRequest(t, svc)

	if err := svc.Approve(context.Background(), requestID, identity); err == nil {
		t.Error("Approve() error = nil, want error when publishing the signing job fails")
	}
}

// TestCertRequestService_Deny_ShouldApplyTheTTLFilterWhenConfigured covers
// Deny's ttlCutoff branch: with RequestTTL set, a pending-but-expired
// request must not be denied by this path — Wait's own lazy expiry owns
// that transition (see reconcileStatus) — so Deny reports it as already
// non-pending, same as any other terminal request.
func TestCertRequestService_Deny_ShouldApplyTheTTLFilterWhenConfigured(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Minute)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	// Back-date CreatedAt past the TTL directly in the DB — mirrors how
	// TestCertRequestService_Wait_ShouldExpireRequestPastTTL ages a row.
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("created_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("failed to back-date created_at: %v", err)
	}

	if err := svc.Deny(context.Background(), requestID); err == nil {
		t.Fatal("expected Deny to refuse a request past its TTL, got nil")
	}
}

// TestCertRequestService_Wait_ShouldReportATerminalStatusFoundColdInTheDB
// covers reconcileStatus's default case (denied/expired/failed) — reachable
// whenever a Wait call finds a request already resolved in the DB but not
// yet in the in-memory resolved cache, e.g. a reconnect after a process
// restart. Constructed directly here (bypassing Deny/expire, which already
// populate the cache themselves) so the cache is guaranteed cold.
func TestCertRequestService_Wait_ShouldReportATerminalStatusFoundColdInTheDB(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Updates(map[string]any{"status": model.CertificateRequestStatusFailed, "resolved_at": time.Now()}).Error; err != nil {
		t.Fatalf("failed to mark the request failed directly in the DB: %v", err)
	}

	status, _, _, err := svc.Wait(context.Background(), requestID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != model.CertificateRequestStatusFailed {
		t.Errorf("got status %q, want %q", status, model.CertificateRequestStatusFailed)
	}
}

// TestExpire_ShouldBeANoOpTheSecondTime covers expire's own
// RowsAffected==0 early return: called again on a request it (or a Deny/
// Approve racing it) already resolved, it must do nothing rather than
// double-notify — expire is unexported and called from multiple sites
// (Approve's TTL check, reconcileStatus), so this exercises it directly
// rather than threading the call through one specific caller.
func TestExpire_ShouldBeANoOpTheSecondTime(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, 0)
	requestID, err := svc.CreateRequest(context.Background(), NewCertRequestParams{Type: model.CertificateTypeUser, PublicKey: "ssh-ed25519 AAAA..."})
	if err != nil {
		t.Fatalf("unexpected error creating request: %v", err)
	}

	svc.expire(context.Background(), requestID)

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("failed to reload request: %v", err)
	}
	if req.Status != model.CertificateRequestStatusExpired {
		t.Fatalf("got status %q after first expire(), want %q", req.Status, model.CertificateRequestStatusExpired)
	}

	// Second call: RowsAffected must be 0 (already expired), and this must
	// not panic or otherwise misbehave.
	svc.expire(context.Background(), requestID)
}

// TestNotifyWaiter_ShouldNotPanicWhenPublishingFails covers notifyWaiter's
// own Publish error branch: a publish failure here is logged, not fatal to
// the caller (Deny/expire's own DB update already succeeded — see the
// function's doc comment), so this only asserts it doesn't panic.
func TestNotifyWaiter_ShouldNotPanicWhenPublishingFails(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open in-memory sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.CertificateRequest{}, &model.User{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	channel := gochannel.NewGoChannel(gochannel.Config{Persistent: false}, watermill.NewSlogLogger(slog.Default()))
	t.Cleanup(func() {
		if err := channel.Close(); err != nil {
			t.Errorf("unexpected error closing gochannel: %v", err)
		}
	})

	svc, err := NewCertRequestService(&config.Config{}, db, failingPublisher{}, channel)
	if err != nil {
		t.Fatalf("failed to construct CertRequestService: %v", err)
	}

	svc.notifyWaiter("req-1", requestOutcome{status: model.CertificateRequestStatusDenied})

	svc.mu.Lock()
	_, cached := svc.resolved["req-1"]
	svc.mu.Unlock()
	if !cached {
		t.Error("expected the outcome to be cached even though publishing failed")
	}
}
