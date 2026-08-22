package service

import (
	"context"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/config"
	"github.com/mnestor/ssoossh/server/model"
)

// Test methodology: the sweep is exercised against the same in-memory
// sqlite service fixture the rest of this package uses, with rows aged by
// writing created_at directly — the alternative (sleeping past a real
// timeout) would make these tests slow for no extra confidence.

// sweepOptions returns cert options with both bounds set, so the sweep's
// cutoff is created_at < now - (ttl + signingTimeout).
func sweepOptions(ttl, signingTimeout time.Duration) config.CertificateOptions {
	return config.CertificateOptions{RequestTTL: ttl, SigningTimeout: signingTimeout}
}

// signingRequestAged creates a request, moves it to Signing, and backdates
// its created_at by age.
func signingRequestAged(t *testing.T, svc *CertRequestService, age time.Duration) string {
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
		Updates(map[string]any{
			"status":     model.CertificateRequestStatusSigning,
			"created_at": time.Now().Add(-age),
		}).Error; err != nil {
		t.Fatalf("failed to age request into signing: %v", err)
	}
	return requestID
}

// requestByID reads a request back.
func requestByID(t *testing.T, svc *CertRequestService, requestID string) model.CertificateRequest {
	t.Helper()

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading back the request: %v", err)
	}
	return req
}

// TestSweepStrandedRequests_ShouldSurfaceAGenericDBError covers the Find
// error branch, and TestFailStranded_ShouldLogAndReturnOnADBError covers
// failStranded's own — both via a closed connection.
func TestSweepStrandedRequests_ShouldSurfaceAGenericDBError(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(time.Hour, time.Minute))
	closeUnderlyingDB(t, svc.db)

	if err := svc.SweepStrandedRequests(context.Background()); err == nil {
		t.Error("SweepStrandedRequests() error = nil, want error")
	}
}

func TestFailStranded_ShouldLogAndReturnOnADBError(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(time.Hour, time.Minute))
	requestID := signingRequestAged(t, svc, 3*time.Hour)
	closeUnderlyingDB(t, svc.db)

	svc.failStranded(context.Background(), []string{requestID})
}

func TestSweepStrandedRequests_ShouldFailRequestsPastTheBound(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(5*time.Minute, 5*time.Minute))
	// Bound is ttl+timeout = 10m; 30m is comfortably past it.
	requestID := signingRequestAged(t, svc, 30*time.Minute)

	if err := svc.SweepStrandedRequests(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	req := requestByID(t, svc, requestID)
	if req.Status != model.CertificateRequestStatusFailed {
		t.Errorf("got status %q, want %q", req.Status, model.CertificateRequestStatusFailed)
	}
	if req.FailureReason != FailureReasonStranded {
		t.Errorf("got failure reason %q, want %q", req.FailureReason, FailureReasonStranded)
	}
	if req.ResolvedAt == nil {
		t.Error("expected resolved_at to be set")
	}
}

// TestSweepStrandedRequests_ShouldLeaveInFlightRequestsAlone is the
// regression that matters most: sweeping a request that's still legitimately
// being signed would cancel a certificate that was about to be issued.
func TestSweepStrandedRequests_ShouldLeaveInFlightRequestsAlone(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(5*time.Minute, 5*time.Minute))
	// Bound is 10m. 9m is inside it — this request could still have been
	// approved recently and be signing right now.
	requestID := signingRequestAged(t, svc, 9*time.Minute)

	if err := svc.SweepStrandedRequests(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := requestByID(t, svc, requestID).Status; got != model.CertificateRequestStatusSigning {
		t.Errorf("got status %q, want the request left in %q", got, model.CertificateRequestStatusSigning)
	}
}

func TestSweepStrandedRequests_ShouldIgnoreRequestsInOtherStatuses(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(5*time.Minute, 5*time.Minute))

	// Every non-signing status, all aged well past the bound.
	statuses := []model.CertificateRequestStatus{
		model.CertificateRequestStatusPending,
		model.CertificateRequestStatusApproved,
		model.CertificateRequestStatusEnrolled,
		model.CertificateRequestStatusDenied,
		model.CertificateRequestStatusExpired,
	}
	ids := make(map[model.CertificateRequestStatus]string, len(statuses))
	for _, status := range statuses {
		requestID := signingRequestAged(t, svc, time.Hour)
		if err := svc.db.Model(&model.CertificateRequest{}).
			Where("id = ?", requestID).
			Update("status", status).Error; err != nil {
			t.Fatalf("failed to set status %q: %v", status, err)
		}
		ids[status] = requestID
	}

	if err := svc.SweepStrandedRequests(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for status, requestID := range ids {
		if got := requestByID(t, svc, requestID).Status; got != status {
			t.Errorf("request in %q was changed to %q", status, got)
		}
	}
}

// TestSweepStrandedRequests_ShouldWakeAWaitingClient covers why failStranded
// notifies per row instead of relying on its bulk UPDATE alone: Wait only
// re-reads the database when a message lands on the request's wake topic, so
// a connected client would otherwise block forever against a row that
// already reads failed.
func TestSweepStrandedRequests_ShouldWakeAWaitingClient(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(5*time.Minute, 5*time.Minute))
	requestID := signingRequestAged(t, svc, 30*time.Minute)

	type waitResult struct {
		status model.CertificateRequestStatus
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		status, _, _, err := svc.Wait(context.Background(), requestID)
		done <- waitResult{status, err}
	}()
	// Let Wait reach its blocking select before the sweep runs, so the
	// unblock has to come from the sweep's notification.
	time.Sleep(50 * time.Millisecond)

	if err := svc.SweepStrandedRequests(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("unexpected error from Wait: %v", res.err)
		}
		if res.status != model.CertificateRequestStatusFailed {
			t.Errorf("got status %q, want %q", res.status, model.CertificateRequestStatusFailed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the swept request never woke the waiting client")
	}
}

// TestSweepStrandedRequests_ShouldNotOverwriteAConcurrentResolution covers
// the row resolving between the sweep's select and its update — the guarded
// WHERE means the sweep loses that race rather than clobbering the result.
func TestSweepStrandedRequests_ShouldNotOverwriteAConcurrentResolution(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(5*time.Minute, 5*time.Minute))
	requestID := signingRequestAged(t, svc, 30*time.Minute)

	// Stand in for the listener winning the race: the row leaves Signing
	// before the sweep's guarded update runs.
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("status", model.CertificateRequestStatusApproved).Error; err != nil {
		t.Fatalf("failed to resolve request: %v", err)
	}

	svc.failStranded(context.Background(), []string{requestID})

	if got := requestByID(t, svc, requestID).Status; got != model.CertificateRequestStatusApproved {
		t.Errorf("sweep overwrote a concurrently resolved request: got %q", got)
	}
}

// TestSweepStrandedRequests_ShouldFailMultipleRequestsInOneSweep covers
// failStranded's batch UPDATE across more than one row: every stranded
// request in the sweep, not just the first, must come back Failed.
func TestSweepStrandedRequests_ShouldFailMultipleRequestsInOneSweep(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(5*time.Minute, 5*time.Minute))
	first := signingRequestAged(t, svc, 30*time.Minute)
	second := signingRequestAged(t, svc, 45*time.Minute)

	if err := svc.SweepStrandedRequests(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, requestID := range []string{first, second} {
		if got := requestByID(t, svc, requestID).Status; got != model.CertificateRequestStatusFailed {
			t.Errorf("request %s: got status %q, want %q", requestID, got, model.CertificateRequestStatusFailed)
		}
	}
}

// TestSweepStrandedRequests_ShouldOnlyOverwriteTheRequestsItActuallyUpdated
// covers the RETURNING-based filtering in failStranded: when a batch mixes
// a request that's still legitimately stranded with one that resolved
// concurrently, only the still-stranded one should end up Failed — the
// concurrently resolved one must be left alone, exactly as it would be if
// it were the only row in the batch.
func TestSweepStrandedRequests_ShouldOnlyOverwriteTheRequestsItActuallyUpdated(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(5*time.Minute, 5*time.Minute))
	stillStranded := signingRequestAged(t, svc, 30*time.Minute)
	racedAway := signingRequestAged(t, svc, 45*time.Minute)

	// Stand in for the listener winning the race on one of the two rows the
	// sweep is about to act on.
	if err := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", racedAway).
		Update("status", model.CertificateRequestStatusApproved).Error; err != nil {
		t.Fatalf("failed to resolve request: %v", err)
	}

	svc.failStranded(context.Background(), []string{stillStranded, racedAway})

	if got := requestByID(t, svc, stillStranded).Status; got != model.CertificateRequestStatusFailed {
		t.Errorf("still-stranded request: got status %q, want %q", got, model.CertificateRequestStatusFailed)
	}
	if got := requestByID(t, svc, racedAway).Status; got != model.CertificateRequestStatusApproved {
		t.Errorf("concurrently resolved request was overwritten: got %q", got)
	}
}

// TestSweepStrandedRequests_ShouldSweepEverythingWhenTTLDisabled documents
// the degenerate configuration: with RequestTTL off there's no bound to
// derive, so every signing request is reported stranded. That's only safe at
// startup, which is why bootstrap runs this inline and skips the recurring
// pass in that configuration.
func TestSweepStrandedRequests_ShouldSweepEverythingWhenTTLDisabled(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(0, 5*time.Minute))
	// Brand new — would be safely ignored if a bound could be derived.
	requestID := signingRequestAged(t, svc, 0)

	if err := svc.SweepStrandedRequests(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := requestByID(t, svc, requestID).Status; got != model.CertificateRequestStatusFailed {
		t.Errorf("got status %q, want %q", got, model.CertificateRequestStatusFailed)
	}
}

// TestStrandedCutoff_ShouldBeExpressedInUTC guards the query-parameter half
// of the invariant in package dbtime. This value is compared against
// created_at, which SQLite compares as text, so a local-offset cutoff
// against UTC-stored rows compares by literal digits rather than by
// instant — the sweep would then skip stranded requests, or fail live ones,
// whenever the two offsets differ. The plugin cannot cover this: GORM
// builds bound parameters inside the query callback, with no hook before it.
func TestStrandedCutoff_ShouldBeExpressedInUTC(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(time.Hour, 5*time.Minute))

	if got := svc.strandedCutoff().Location(); got != time.UTC {
		t.Errorf("strandedCutoff() location = %v, want %v", got, time.UTC)
	}
}
