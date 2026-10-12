package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/model"
	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// statusCoder is what the error handler middleware asserts on, directly
// and without errors.As, to pick the HTTP status. An error that does not
// satisfy it unwrapped renders as 500.
type statusCoder interface {
	HTTPStatusCode() int
}

// requireStatus fails t unless err itself (not something it wraps) carries
// want as its HTTP status, which is what the error handler will answer with.
func requireStatus(t *testing.T, err error, want int) {
	t.Helper()

	if err == nil {
		t.Fatalf("got no error, want one rendered as %d", want)
	}
	// Asserted directly, as the middleware does: a wrapped coder is a 500.
	coder, ok := err.(statusCoder)
	if !ok {
		t.Fatalf("got %v (%T), want an error carrying HTTP status %d; without one it renders as 500", err, err, want)
	}
	if got := coder.HTTPStatusCode(); got != want {
		t.Errorf("got HTTP status %d (%v), want %d", got, err, want)
	}
}

// TestNotPendingError covers the mapping from a request's actual state to
// the status a refused approve or deny answers with: 410 when it expired,
// whether or not the row has been flipped yet, and 409 when it was decided.
func TestNotPendingError(t *testing.T) {
	t.Parallel()

	now := time.Now()
	cutoff := now.Add(-time.Minute)
	tests := []struct {
		name       string
		status     model.CertificateRequestStatus
		createdAt  time.Time
		cutoff     time.Time
		wantStatus int
		wantReason string
	}{
		{
			name:       "should answer 410 when the request expired",
			status:     model.CertificateRequestStatusExpired,
			createdAt:  now.Add(-time.Hour),
			cutoff:     cutoff,
			wantStatus: http.StatusGone,
			wantReason: "this certificate request has expired",
		},
		{
			name:       "should answer 410 when an expired request is read with no TTL configured",
			status:     model.CertificateRequestStatusExpired,
			createdAt:  now,
			wantStatus: http.StatusGone,
			wantReason: "this certificate request has expired",
		},
		{
			name:       "should answer 410 when a pending request is past its TTL",
			status:     model.CertificateRequestStatusPending,
			createdAt:  now.Add(-time.Hour),
			cutoff:     cutoff,
			wantStatus: http.StatusGone,
			wantReason: "this certificate request has expired",
		},
		{
			name:       "should answer 409 when a pending request is still within its TTL",
			status:     model.CertificateRequestStatusPending,
			createdAt:  now,
			cutoff:     cutoff,
			wantStatus: http.StatusConflict,
			wantReason: "this certificate request is no longer pending",
		},
		{
			name:       "should answer 409 when a pending request has no TTL",
			status:     model.CertificateRequestStatusPending,
			createdAt:  now.Add(-time.Hour),
			wantStatus: http.StatusConflict,
			wantReason: "this certificate request is no longer pending",
		},
		{
			name:       "should answer 409 when the request was denied",
			status:     model.CertificateRequestStatusDenied,
			createdAt:  now,
			cutoff:     cutoff,
			wantStatus: http.StatusConflict,
			wantReason: "this certificate request was already denied",
		},
		{
			// A request decided within its TTL and read after it is still
			// decided, not expired.
			name:       "should answer 409 when a denied request is older than the TTL",
			status:     model.CertificateRequestStatusDenied,
			createdAt:  now.Add(-time.Hour),
			cutoff:     cutoff,
			wantStatus: http.StatusConflict,
			wantReason: "this certificate request was already denied",
		},
		{
			name:       "should answer 409 when the request was approved",
			status:     model.CertificateRequestStatusApproved,
			createdAt:  now,
			cutoff:     cutoff,
			wantStatus: http.StatusConflict,
			wantReason: "this certificate request was already approved",
		},
		{
			name:       "should answer 409 when the request is signing",
			status:     model.CertificateRequestStatusSigning,
			createdAt:  now,
			cutoff:     cutoff,
			wantStatus: http.StatusConflict,
			wantReason: "this certificate request was already approved",
		},
		{
			name:       "should answer 409 when the request was enrolled",
			status:     model.CertificateRequestStatusEnrolled,
			createdAt:  now,
			cutoff:     cutoff,
			wantStatus: http.StatusConflict,
			wantReason: "this certificate request was already approved",
		},
		{
			name:       "should answer 409 when signing the approved request failed",
			status:     model.CertificateRequestStatusFailed,
			createdAt:  now,
			cutoff:     cutoff,
			wantStatus: http.StatusConflict,
			wantReason: "this certificate request was already approved",
		},
		{
			name:       "should answer 409 when the status is one this code does not know",
			status:     model.CertificateRequestStatus("bogus"),
			createdAt:  now,
			cutoff:     cutoff,
			wantStatus: http.StatusConflict,
			wantReason: "this certificate request is no longer pending",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := notPendingError(model.CertificateRequest{ID: "req-1", Status: tt.status, CreatedAt: tt.createdAt}, tt.cutoff)

			requireStatus(t, err, tt.wantStatus)
			if err.Error() != tt.wantReason {
				t.Errorf("got reason %q, want %q", err.Error(), tt.wantReason)
			}
		})
	}
}

// TestCertRequestService_ShouldRefuseADecisionOnARequestNoLongerPending
// drives Approve and Deny against a row in each non-pending state and
// checks the status the refusal carries: a race that lost is the caller's
// conflict or an expiry, never a 500.
func TestCertRequestService_ShouldRefuseADecisionOnARequestNoLongerPending(t *testing.T) {
	t.Parallel()

	states := []struct {
		name       string
		status     model.CertificateRequestStatus
		pastTTL    bool
		wantStatus int
	}{
		{name: "denied", status: model.CertificateRequestStatusDenied, wantStatus: http.StatusConflict},
		{name: "approved", status: model.CertificateRequestStatusApproved, wantStatus: http.StatusConflict},
		{name: "signing", status: model.CertificateRequestStatusSigning, wantStatus: http.StatusConflict},
		{name: "enrolled", status: model.CertificateRequestStatusEnrolled, wantStatus: http.StatusConflict},
		{name: "failed", status: model.CertificateRequestStatusFailed, wantStatus: http.StatusConflict},
		{name: "expired", status: model.CertificateRequestStatusExpired, wantStatus: http.StatusGone},
		{name: "pending past its TTL", status: model.CertificateRequestStatusPending, pastTTL: true, wantStatus: http.StatusGone},
	}
	actions := []struct {
		name   string
		decide func(svc *CertRequestService, requestID string, identity *Identity) error
	}{
		{
			name: "approve",
			decide: func(svc *CertRequestService, requestID string, identity *Identity) error {
				return svc.Approve(context.Background(), requestID, identity, DecisionContext{}, ApprovalSelection{})
			},
		},
		{
			name: "deny",
			decide: func(svc *CertRequestService, requestID string, identity *Identity) error {
				return svc.Deny(context.Background(), requestID, identity, DecisionContext{})
			},
		},
	}

	for _, action := range actions {
		for _, state := range states {
			t.Run("should answer "+http.StatusText(state.wantStatus)+" when "+action.name+" finds the request "+state.name, func(t *testing.T) {
				t.Parallel()

				svc := newTestCertRequestService(t, time.Hour)
				identity := &Identity{Username: "alice", Subject: "sub-alice"}
				// Seeded so the not-pending refusal is the only thing
				// that can stop the call, whatever order Approve and
				// Deny resolve the user in.
				seedUser(t, svc.db, identity.Subject)
				requestID := mustCreateUserRequest(t, svc)

				updates := map[string]any{"status": state.status}
				if state.pastTTL {
					updates["created_at"] = time.Now().Add(-24 * time.Hour)
				}
				if err := svc.db.Model(&model.CertificateRequest{}).Where("id = ?", requestID).Updates(updates).Error; err != nil {
					t.Fatalf("failed to put the request in state %q: %v", state.name, err)
				}

				requireStatus(t, action.decide(svc, requestID, identity), state.wantStatus)
			})
		}
	}
}

// TestCertRequestService_ShouldLeaveARefusedRequestAsItWas checks that
// refusing a decision on an already-decided request changes nothing: the
// row keeps the state that beat the caller.
func TestCertRequestService_ShouldLeaveARefusedRequestAsItWas(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		decide func(svc *CertRequestService, requestID string, identity *Identity) error
	}{
		{
			name: "should keep the request denied when a late approve is refused",
			decide: func(svc *CertRequestService, requestID string, identity *Identity) error {
				return svc.Approve(context.Background(), requestID, identity, DecisionContext{}, ApprovalSelection{})
			},
		},
		{
			name: "should keep the request denied when a second deny is refused",
			decide: func(svc *CertRequestService, requestID string, identity *Identity) error {
				return svc.Deny(context.Background(), requestID, identity, DecisionContext{})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestCertRequestService(t, time.Hour)
			identity := &Identity{Username: "alice", Subject: "sub-alice"}
			seedUser(t, svc.db, identity.Subject)
			requestID := mustCreateUserRequest(t, svc)
			if err := svc.db.Model(&model.CertificateRequest{}).Where("id = ?", requestID).
				Update("status", model.CertificateRequestStatusDenied).Error; err != nil {
				t.Fatalf("failed to deny the request directly: %v", err)
			}

			if err := tt.decide(svc, requestID, identity); err == nil {
				t.Fatal("got no error, want the decision refused")
			}

			var req model.CertificateRequest
			if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
				t.Fatalf("failed to read the request back: %v", err)
			}
			if req.Status != model.CertificateRequestStatusDenied {
				t.Errorf("got status %q, want it still %q", req.Status, model.CertificateRequestStatusDenied)
			}
		})
	}
}

// TestCertRequestService_Approve_ShouldExpireAPendingRequestPastItsTTL
// pins that Approve, unlike Deny, flips a pending row it finds past the TTL
// to expired as it refuses it, so the approval page's re-read shows the
// expiry rather than a request that still looks open.
func TestCertRequestService_Approve_ShouldExpireAPendingRequestPastItsTTL(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	identity := &Identity{Username: "alice", Subject: "sub-alice"}
	seedUser(t, svc.db, identity.Subject)
	requestID := mustCreateUserRequest(t, svc)
	if err := svc.db.Model(&model.CertificateRequest{}).Where("id = ?", requestID).
		Update("created_at", time.Now().Add(-24*time.Hour)).Error; err != nil {
		t.Fatalf("failed to back-date the request: %v", err)
	}

	if err := svc.Approve(context.Background(), requestID, identity, DecisionContext{}, ApprovalSelection{}); err == nil {
		t.Fatal("got no error, want the expired request refused")
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("failed to read the request back: %v", err)
	}
	if req.Status != model.CertificateRequestStatusExpired {
		t.Errorf("got status %q, want %q", req.Status, model.CertificateRequestStatusExpired)
	}
}

// TestApprove_ShouldExplainAGuardedUpdateThatLostTheRace covers the
// guarded "WHERE status = pending" updates in approveForSigning and
// approveServiceEnrollment. Approve's own pending check catches a row that
// was already decided when it was read, so these are reached only when the
// row changes between that read and the update. Calling the branch methods
// directly with a stale copy of the row reproduces that race
// deterministically; the refusal must describe the row as it is now.
func TestApprove_ShouldExplainAGuardedUpdateThatLostTheRace(t *testing.T) {
	t.Parallel()

	branches := []struct {
		name     string
		certType model.CertificateType
		approve  func(svc *CertRequestService, req model.CertificateRequest) error
	}{
		{
			name:     "signing",
			certType: model.CertificateTypeUser,
			approve: func(svc *CertRequestService, req model.CertificateRequest) error {
				identity := &Identity{Username: "alice", Subject: "sub-alice"}
				return svc.approveForSigning(context.Background(), req, identity, svc.policies[model.CertificateTypeUser], RequestedOptions{}, DecisionContext{}, []string{})
			},
		},
		{
			name:     "service enrollment",
			certType: model.CertificateTypeService,
			approve: func(svc *CertRequestService, req model.CertificateRequest) error {
				identity := &Identity{Username: "approver", Subject: "sub-approver"}
				policy, err := svc.policyFor(model.CertificateTypeService)
				if err != nil {
					return err
				}
				return svc.approveServiceEnrollment(context.Background(), req, RequestedOptions{}, nil, identity, policy, DecisionContext{}, ApprovalSelection{ServiceAccount: "svc-account"})
			},
		},
	}
	races := []struct {
		name       string
		lose       func(svc *CertRequestService, requestID string) error
		wantStatus int
	}{
		{
			name: "was denied",
			lose: func(svc *CertRequestService, requestID string) error {
				return svc.db.Model(&model.CertificateRequest{}).Where("id = ?", requestID).
					Update("status", model.CertificateRequestStatusDenied).Error
			},
			wantStatus: http.StatusConflict,
		},
		{
			name: "was approved",
			lose: func(svc *CertRequestService, requestID string) error {
				return svc.db.Model(&model.CertificateRequest{}).Where("id = ?", requestID).
					Update("status", model.CertificateRequestStatusSigning).Error
			},
			wantStatus: http.StatusConflict,
		},
		{
			name: "expired",
			lose: func(svc *CertRequestService, requestID string) error {
				return svc.db.Model(&model.CertificateRequest{}).Where("id = ?", requestID).
					Update("status", model.CertificateRequestStatusExpired).Error
			},
			wantStatus: http.StatusGone,
		},
		{
			name: "was swept away",
			lose: func(svc *CertRequestService, requestID string) error {
				return svc.db.Delete(&model.CertificateRequest{}, "id = ?", requestID).Error
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, branch := range branches {
		for _, race := range races {
			t.Run("should answer "+http.StatusText(race.wantStatus)+" when the "+branch.name+" update finds the request "+race.name, func(t *testing.T) {
				t.Parallel()

				svc := newTestCertRequestService(t, time.Hour)
				requestID, err := svc.createRequestID(context.Background(), NewCertRequestParams{Type: branch.certType, PublicKey: "ssh-ed25519 AAAA test"})
				if err != nil {
					t.Fatalf("failed to create the request: %v", err)
				}
				var stale model.CertificateRequest
				if err := svc.db.First(&stale, "id = ?", requestID).Error; err != nil {
					t.Fatalf("failed to read the request: %v", err)
				}
				if err := race.lose(svc, requestID); err != nil {
					t.Fatalf("failed to change the request under the approval: %v", err)
				}

				requireStatus(t, branch.approve(svc, stale), race.wantStatus)
			})
		}
	}
}

// TestCheckServiceAccountLinkage_ShouldRefuseWithForbidden pins that an
// approver naming a service account they are not associated with is a
// policy refusal (403), not a server fault.
func TestCheckServiceAccountLinkage_ShouldRefuseWithForbidden(t *testing.T) {
	t.Parallel()

	identity := &Identity{Username: "alice", ServiceAccounts: []string{"svc-deploy"}}

	err := checkServiceAccountLinkage(identity, "svc-payroll", true)

	var forbidden *errorresponses.ForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("got %v (%T), want a ForbiddenError", err, err)
	}
	requireStatus(t, err, http.StatusForbidden)
}
