package service

// Test methodology: unit tests against the same in-memory sqlite *gorm.DB
// the other certificate tests use. What matters here is what /api/certs
// cannot answer -- a denial issues no certificate, so none of the scoping
// the certificate list relies on applies. These pin the three rules that
// replace it: rows are scoped by the decision's own subject snapshot,
// approvals never appear, and a row survives its request going away.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mnestor/ssoossh/server/model"
)

// seedDecision inserts one decision, optionally with the request behind it,
// and returns the decision's id.
func seedDecision(
	t *testing.T,
	svc *CertRequestService,
	subject string,
	outcome model.CertificateRequestDecisionOutcome,
	decidedAt time.Time,
	withRequest bool,
	requestType model.CertificateType,
) string {
	t.Helper()

	requestID := uuid.NewString()
	if withRequest {
		req := model.CertificateRequest{
			ID:        requestID,
			Type:      requestType,
			Status:    model.CertificateRequestStatusDenied,
			CreatedAt: decidedAt,
		}
		if err := svc.db.Create(&req).Error; err != nil {
			t.Fatalf("failed to seed request: %v", err)
		}
	}

	decision := model.CertificateRequestDecision{
		ID:                   uuid.NewString(),
		CertificateRequestID: requestID,
		Outcome:              outcome,
		Subject:              subject,
		Username:             "alice",
		Email:                "alice@example.com",
		SourceIP:             "198.51.100.7",
		ReportedUsername:     "deploy",
		ReportedHostname:     "rack07",
		PAMService:           "sudo",
		TTY:                  "pts/3",
		RemoteHost:           "10.1.2.9",
		Client:               "ssoossh/1.2.0",
		DecidedAt:            decidedAt,
	}
	if err := svc.db.Create(&decision).Error; err != nil {
		t.Fatalf("failed to seed decision: %v", err)
	}
	return decision.ID
}

// The scoping rule, and the only one that matters for disclosure: the
// decisions table has no user foreign key, so the subject snapshot is what
// stands between one decider's history and another's.
func TestListDeniedForIdentity_ShouldReturnOnlyTheCallersOwnDenials(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	now := time.Now()
	mine := seedDecision(t, reqSvc, "sub-alice", model.CertificateRequestDecisionDenied, now, true, model.CertificateTypePAM)
	seedDecision(t, reqSvc, "sub-bob", model.CertificateRequestDecisionDenied, now, true, model.CertificateTypePAM)

	got, _, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 100)
	if err != nil {
		t.Fatalf("ListDeniedForIdentity() error = %v", err)
	}

	if len(got) != 1 || got[0].Decision.ID != mine {
		t.Errorf("ListDeniedForIdentity() = %d rows, want only alice's denial %s", len(got), mine)
	}
}

// An approval already has a row in the certificate list. Returning it here
// too would double every approved request on a page that interleaves the
// two.
func TestListDeniedForIdentity_ShouldLeaveOutApprovals(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	now := time.Now()
	seedDecision(t, reqSvc, "sub-alice", model.CertificateRequestDecisionApproved, now, true, model.CertificateTypeUser)

	got, _, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 100)
	if err != nil {
		t.Fatalf("ListDeniedForIdentity() error = %v", err)
	}

	if len(got) != 0 {
		t.Errorf("ListDeniedForIdentity() = %d rows, want 0 -- approvals belong to the certificate list", len(got))
	}
}

func TestListDeniedForIdentity_ShouldReportTheTypeThatWasAskedFor(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	seedDecision(t, reqSvc, "sub-alice", model.CertificateRequestDecisionDenied, time.Now(), true, model.CertificateTypeConsole)

	got, _, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 100)
	if err != nil {
		t.Fatalf("ListDeniedForIdentity() error = %v", err)
	}

	if len(got) != 1 || got[0].Type != model.CertificateTypeConsole {
		t.Errorf("ListDeniedForIdentity() type = %q, want %q", got[0].Type, model.CertificateTypeConsole)
	}
}

// The decisions table is the permanent one by design, so the join to
// certificate_requests is allowed to miss. A denial with no request row
// still has to be listed -- dropping it would quietly shorten somebody's
// audit trail.
func TestListDeniedForIdentity_ShouldListADenialWhoseRequestIsGone(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	seedDecision(t, reqSvc, "sub-alice", model.CertificateRequestDecisionDenied, time.Now(), false, "")

	got, _, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 100)
	if err != nil {
		t.Fatalf("ListDeniedForIdentity() error = %v", err)
	}

	if len(got) != 1 {
		t.Fatalf("ListDeniedForIdentity() = %d rows, want 1", len(got))
	}
	if got[0].Type != "" {
		t.Errorf("ListDeniedForIdentity() type = %q, want empty for a request that is gone", got[0].Type)
	}
}

// The host context is what makes a refusal identifiable a month later. It
// is copied onto the decision at decision time, so it is read straight off
// that row rather than through the join that may miss.
func TestListDeniedForIdentity_ShouldCarryTheHostContextTheRequestClaimed(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	seedDecision(t, reqSvc, "sub-alice", model.CertificateRequestDecisionDenied, time.Now(), true, model.CertificateTypePAM)

	got, _, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 100)
	if err != nil {
		t.Fatalf("ListDeniedForIdentity() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListDeniedForIdentity() = %d rows, want 1", len(got))
	}

	d := got[0].Decision
	if d.PAMService != "sudo" || d.TTY != "pts/3" || d.RemoteHost != "10.1.2.9" || d.Client != "ssoossh/1.2.0" {
		t.Errorf("host context = %q/%q/%q/%q, want sudo/pts~3/10.1.2.9/ssoossh~1.2.0",
			d.PAMService, d.TTY, d.RemoteHost, d.Client)
	}
}

// It survives the request row going away, which is the reason it is copied
// rather than joined for.
func TestListDeniedForIdentity_ShouldKeepTheHostContextWhenTheRequestIsGone(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	seedDecision(t, reqSvc, "sub-alice", model.CertificateRequestDecisionDenied, time.Now(), false, "")

	got, _, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 100)
	if err != nil {
		t.Fatalf("ListDeniedForIdentity() error = %v", err)
	}
	if len(got) != 1 || got[0].Decision.RemoteHost != "10.1.2.9" {
		t.Errorf("remote host = %q, want it kept even with no request row", got[0].Decision.RemoteHost)
	}
}

func TestListDeniedForIdentity_ShouldOrderNewestFirst(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	now := time.Now()
	older := seedDecision(t, reqSvc, "sub-alice", model.CertificateRequestDecisionDenied, now.Add(-time.Hour), true, model.CertificateTypeUser)
	newer := seedDecision(t, reqSvc, "sub-alice", model.CertificateRequestDecisionDenied, now, true, model.CertificateTypeUser)

	got, _, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 100)
	if err != nil {
		t.Fatalf("ListDeniedForIdentity() error = %v", err)
	}

	if len(got) != 2 || got[0].Decision.ID != newer || got[1].Decision.ID != older {
		t.Errorf("ListDeniedForIdentity() order = %v, want newest (%s) first", got, newer)
	}
}

func TestListDeniedForIdentity_ShouldPageWithTheCursorItReturns(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	now := time.Now()
	older := seedDecision(t, reqSvc, "sub-alice", model.CertificateRequestDecisionDenied, now.Add(-time.Hour), true, model.CertificateTypeUser)
	newer := seedDecision(t, reqSvc, "sub-alice", model.CertificateRequestDecisionDenied, now, true, model.CertificateTypeUser)

	first, cursor, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 1)
	if err != nil {
		t.Fatalf("ListDeniedForIdentity() error = %v", err)
	}
	if len(first) != 1 || first[0].Decision.ID != newer {
		t.Fatalf("first page = %v, want just %s", first, newer)
	}
	if cursor == nil || *cursor != newer {
		t.Fatalf("cursor = %v, want %s", cursor, newer)
	}

	second, next, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, cursor, 1)
	if err != nil {
		t.Fatalf("ListDeniedForIdentity(after) error = %v", err)
	}
	if len(second) != 1 || second[0].Decision.ID != older {
		t.Fatalf("second page = %v, want just %s", second, older)
	}
	if next != nil {
		t.Errorf("cursor after the last row = %v, want nil", next)
	}
}

// A cursor naming somebody else's decision must not page through it. The
// lookup is scoped by subject for the same reason the list is.
func TestListDeniedForIdentity_ShouldRefuseACursorFromAnotherDecider(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	theirs := seedDecision(t, reqSvc, "sub-bob", model.CertificateRequestDecisionDenied, time.Now(), true, model.CertificateTypeUser)

	if _, _, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, &theirs, 25); err == nil {
		t.Error("ListDeniedForIdentity() with another decider's cursor = nil error, want a refusal")
	}
}

func TestListDeniedForIdentity_ShouldReturnNothingForADeciderWithNoDenials(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)

	got, cursor, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-nobody"}, nil, 25)
	if err != nil {
		t.Fatalf("ListDeniedForIdentity() error = %v", err)
	}
	if len(got) != 0 || cursor != nil {
		t.Errorf("ListDeniedForIdentity() = %d rows / cursor %v, want 0 and nil", len(got), cursor)
	}
}

func TestListDeniedForIdentity_ShouldSurfaceADatabaseError(t *testing.T) {
	t.Parallel()

	reqSvc := newTestCertRequestService(t, time.Hour)
	svc := newTestCertificateService(t, reqSvc)
	closeUnderlyingDB(t, reqSvc.db)

	if _, _, err := svc.ListDeniedForIdentity(context.Background(), &Identity{Subject: "sub-alice"}, nil, 25); err == nil {
		t.Error("ListDeniedForIdentity() on a closed database = nil error, want an error")
	}
}
