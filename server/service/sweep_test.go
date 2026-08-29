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

// sweepOptions returns cert options for a whole-budget clientTimeout. The
// sweep's cutoff is created_at < now - (ApprovalTTL + SigningGrace), both
// derived from it, so tests that care about the boundary compute it from
// the options rather than restating a number that the split owns.
func sweepOptions(clientTimeout time.Duration) config.CertificateOptions {
	return config.CertificateOptions{ClientTimeout: clientTimeout}
}

// strandedBound is the age at which sweepOptions' requests become stranded.
func strandedBound(opts config.CertificateOptions) time.Duration {
	return opts.ApprovalTTL() + opts.SigningGrace()
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

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(time.Hour))
	closeUnderlyingDB(t, svc.db)

	if err := svc.SweepStrandedRequests(context.Background()); err == nil {
		t.Error("SweepStrandedRequests() error = nil, want error")
	}
}

func TestFailStranded_ShouldLogAndReturnOnADBError(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(time.Hour))
	requestID := signingRequestAged(t, svc, 3*time.Hour)
	closeUnderlyingDB(t, svc.db)

	svc.failStranded(context.Background(), []string{requestID})
}

func TestSweepStrandedRequests_ShouldFailRequestsPastTheBound(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(10*time.Minute))
	// 30m is comfortably past the bound.
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

	opts := sweepOptions(10 * time.Minute)
	svc := newTestCertRequestServiceWithOptions(t, opts)
	// Inside the bound — this request could still have been
	// approved recently and be signing right now.
	requestID := signingRequestAged(t, svc, strandedBound(opts)-time.Minute)

	if err := svc.SweepStrandedRequests(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := requestByID(t, svc, requestID).Status; got != model.CertificateRequestStatusSigning {
		t.Errorf("got status %q, want the request left in %q", got, model.CertificateRequestStatusSigning)
	}
}

func TestSweepStrandedRequests_ShouldIgnoreRequestsInOtherStatuses(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(10*time.Minute))

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

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(10*time.Minute))
	requestID := signingRequestAged(t, svc, 30*time.Minute)

	type waitResult struct {
		status model.CertificateRequestStatus
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		outcome, err := svc.Wait(context.Background(), requestID)
		status := outcome.Status
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

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(10*time.Minute))
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

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(10*time.Minute))
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

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(10*time.Minute))
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
// the degenerate configuration: with the approval TTL off there's no bound to
// derive, so every signing request is reported stranded. That's only safe at
// startup, which is why bootstrap runs this inline and skips the recurring
// pass in that configuration.
func TestSweepStrandedRequests_ShouldSweepEverythingWhenTTLDisabled(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(0))
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

	svc := newTestCertRequestServiceWithOptions(t, sweepOptions(time.Hour))

	if got := svc.strandedCutoff().Location(); got != time.UTC {
		t.Errorf("strandedCutoff() location = %v, want %v", got, time.UTC)
	}
}

// TestSweepDisabledUserEnrollments_ShouldNotExpireBeforeGracePeriod verifies
// that enrollments for disabled users are not expired until the grace period
// has elapsed.
func TestSweepDisabledUserEnrollments_ShouldNotExpireBeforeGracePeriod(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.Enrollment{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	ctx := context.Background()
	gracePeriod := 7 * 24 * time.Hour

	// Create a disabled user (just now)
	now := time.Now()
	user := model.User{
		ID:         "user-1",
		Subject:    "subject-1",
		Username:   "testuser",
		DisabledAt: &now,
	}
	db.Create(&user)

	// Create an active enrollment for this user
	enrollment := model.Enrollment{
		ID:        "enrollment-1",
		Code:      "code-1",
		PublicKey: "key-1",
		UserID:    user.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	db.Create(&enrollment)

	// Run the sweep
	if err := SweepDisabledUserEnrollments(ctx, db, gracePeriod, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify enrollment was NOT expired (grace period hasn't elapsed)
	var stillActive model.Enrollment
	db.Where("id = ?", enrollment.ID).First(&stillActive)
	if stillActive.ExpiresAt.Before(now.Add(1 * time.Hour)) {
		t.Error("enrollment should not be expired before grace period elapses")
	}
}

// TestSweepDisabledUserEnrollments_ShouldExpireAfterGracePeriod verifies
// that enrollments for disabled users are expired once the grace period
// has elapsed.
func TestSweepDisabledUserEnrollments_ShouldExpireAfterGracePeriod(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.Enrollment{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	ctx := context.Background()
	gracePeriod := 7 * 24 * time.Hour

	// Create a user disabled well in the past
	now := time.Now()
	disabledAt := now.Add(-gracePeriod).Add(-1 * time.Hour)
	user := model.User{
		ID:         "user-1",
		Subject:    "subject-1",
		Username:   "testuser",
		DisabledAt: &disabledAt,
	}
	db.Create(&user)

	// Create an active enrollment for this user
	enrollment := model.Enrollment{
		ID:        "enrollment-1",
		Code:      "code-1",
		PublicKey: "key-1",
		UserID:    user.ID,
		CreatedAt: disabledAt,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	db.Create(&enrollment)

	// Run the sweep
	if err := SweepDisabledUserEnrollments(ctx, db, gracePeriod, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify enrollment WAS expired (expires_at should be set to approximately now,
	// so it's expired). The sweep sets it to time.Now(), so it should be very close
	// to the current time, definitely not 30 days in the future.
	var expiredEnrollment model.Enrollment
	db.Where("id = ?", enrollment.ID).First(&expiredEnrollment)
	timeSinceExpiry := time.Since(expiredEnrollment.ExpiresAt)
	if timeSinceExpiry < -1*time.Minute {
		// More than 1 minute in the future means sweep didn't expire it
		t.Errorf("enrollment should be expired, but ExpiresAt is %v (now is %v, diff = %v)", expiredEnrollment.ExpiresAt, now, timeSinceExpiry)
	}
}

// TestSweepDisabledUserEnrollments_ShouldIgnoreAlreadyExpiredEnrollments
// verifies that already-expired enrollments are left alone.
func TestSweepDisabledUserEnrollments_ShouldIgnoreAlreadyExpiredEnrollments(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.Enrollment{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	ctx := context.Background()
	gracePeriod := 7 * 24 * time.Hour

	// Create a user disabled well in the past
	now := time.Now()
	disabledAt := now.Add(-gracePeriod).Add(-1 * time.Hour)
	user := model.User{
		ID:         "user-1",
		Subject:    "subject-1",
		Username:   "testuser",
		DisabledAt: &disabledAt,
	}
	db.Create(&user)

	// Create an already-expired enrollment
	pastTime := time.Now().Add(-1 * time.Hour)
	enrollment := model.Enrollment{
		ID:        "enrollment-1",
		Code:      "code-1",
		PublicKey: "key-1",
		UserID:    user.ID,
		CreatedAt: disabledAt,
		ExpiresAt: pastTime, // Already expired
	}
	db.Create(&enrollment)

	// Capture the enrollment's ExpiresAt before the sweep
	var beforeSweep model.Enrollment
	db.Where("id = ?", enrollment.ID).First(&beforeSweep)

	// Run the sweep
	if err := SweepDisabledUserEnrollments(ctx, db, gracePeriod, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify enrollment was left unchanged (already expired enrollments should be
	// excluded by the WHERE expires_at > now() clause)
	var afterSweep model.Enrollment
	db.Where("id = ?", enrollment.ID).First(&afterSweep)
	if afterSweep.ExpiresAt != beforeSweep.ExpiresAt {
		t.Errorf("already-expired enrollment should not be modified by sweep, but ExpiresAt changed from %v to %v", beforeSweep.ExpiresAt, afterSweep.ExpiresAt)
	}
}

// TestSweepDisabledUserEnrollments_ShouldIgnoreEnabledUsers verifies that
// enrollments for enabled (non-disabled) users are not expired.
func TestSweepDisabledUserEnrollments_ShouldIgnoreEnabledUsers(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.Enrollment{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	ctx := context.Background()
	gracePeriod := 7 * 24 * time.Hour

	// Create an enabled user (DisabledAt is nil)
	user := model.User{
		ID:       "user-1",
		Subject:  "subject-1",
		Username: "testuser",
	}
	db.Create(&user)

	// Create an active enrollment
	now := time.Now()
	enrollment := model.Enrollment{
		ID:        "enrollment-1",
		Code:      "code-1",
		PublicKey: "key-1",
		UserID:    user.ID,
		CreatedAt: now,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	db.Create(&enrollment)

	// Run the sweep
	if err := SweepDisabledUserEnrollments(ctx, db, gracePeriod, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify enrollment was NOT expired (user is not disabled)
	var stillActive model.Enrollment
	db.Where("id = ?", enrollment.ID).First(&stillActive)
	if stillActive.ExpiresAt.Before(now.Add(1 * time.Hour)) {
		t.Error("enrollment for enabled user should not be expired")
	}
}

// TestSweepDisabledUserEnrollments_IsIdempotent verifies that running the
// sweep multiple times produces the same result (idempotent).
func TestSweepDisabledUserEnrollments_IsIdempotent(t *testing.T) {
	t.Parallel()

	db := newTestDB(t)
	if err := db.AutoMigrate(&model.User{}, &model.Enrollment{}); err != nil {
		t.Fatalf("failed to migrate test tables: %v", err)
	}
	ctx := context.Background()
	gracePeriod := 7 * 24 * time.Hour

	// Create a user disabled well in the past
	now := time.Now()
	disabledAt := now.Add(-gracePeriod).Add(-1 * time.Hour)
	user := model.User{
		ID:         "user-1",
		Subject:    "subject-1",
		Username:   "testuser",
		DisabledAt: &disabledAt,
	}
	db.Create(&user)

	// Create an active enrollment
	enrollment := model.Enrollment{
		ID:        "enrollment-1",
		Code:      "code-1",
		PublicKey: "key-1",
		UserID:    user.ID,
		CreatedAt: disabledAt,
		ExpiresAt: now.Add(30 * 24 * time.Hour),
	}
	db.Create(&enrollment)

	// Run the sweep twice
	if err := SweepDisabledUserEnrollments(ctx, db, gracePeriod, nil); err != nil {
		t.Fatalf("first sweep failed: %v", err)
	}

	var firstPass model.Enrollment
	db.Where("id = ?", enrollment.ID).First(&firstPass)
	firstExpiresAt := firstPass.ExpiresAt

	if err := SweepDisabledUserEnrollments(ctx, db, gracePeriod, nil); err != nil {
		t.Fatalf("second sweep failed: %v", err)
	}

	var secondPass model.Enrollment
	db.Where("id = ?", enrollment.ID).First(&secondPass)

	if firstExpiresAt != secondPass.ExpiresAt {
		t.Errorf("sweep should be idempotent: first run expired at %v, second run at %v", firstExpiresAt, secondPass.ExpiresAt)
	}
}
