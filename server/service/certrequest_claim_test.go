package service

// Test methodology: same harness as certrequest_test.go — a real in-memory
// sqlite *gorm.DB behind newTestCertRequestService. The claim race's loser
// branch is exercised through the unexported claimApprovalPage with a
// deliberately stale row, the same technique bindRequester's tests use,
// because the single-connection sqlite pool serializes real concurrency.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/server/model"
)

// claimPage claims requestID's page as a fresh browser and returns the
// minted cookie token, failing the test on any other outcome.
func claimPage(t *testing.T, svc *CertRequestService, requestID, userAgent string) string {
	t.Helper()
	res, err := svc.ClaimApprovalPage(context.Background(), requestID, "", userAgent)
	if err != nil {
		t.Fatalf("unexpected error claiming: %v", err)
	}
	if res.Outcome != ClaimPageClaimed {
		t.Fatalf("got outcome %v, want ClaimPageClaimed", res.Outcome)
	}
	return res.Token
}

// backdateClaim rewrites requestID's claimed_at, to age a claim past the
// cookie-blocked window.
func backdateClaim(t *testing.T, svc *CertRequestService, requestID string, age time.Duration) {
	t.Helper()
	result := svc.db.Model(&model.CertificateRequest{}).
		Where("id = ?", requestID).
		Update("claimed_at", time.Now().Add(-age).UTC())
	if result.Error != nil {
		t.Fatalf("unexpected error backdating the claim: %v", result.Error)
	}
	if result.RowsAffected != 1 {
		t.Fatalf("backdating affected %d rows, want 1", result.RowsAffected)
	}
}

// TestClaimApprovalPage_ShouldClaimWhenTheFirstClientGets pins the claim
// half: the first GET wins, gets a token to set as the cookie, and the row
// records the hash — never the token itself — plus when and who.
func TestClaimApprovalPage_ShouldClaimWhenTheFirstClientGets(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	requestID := mustCreateUserRequest(t, svc)

	token := claimPage(t, svc, requestID, "Mozilla/5.0 (claimer)")
	if token == "" {
		t.Fatal("expected a non-empty claim token")
	}

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading the request back: %v", err)
	}
	if req.ClaimTokenHash == nil {
		t.Fatal("expected the claim token hash to be recorded")
	}
	if *req.ClaimTokenHash != hashClaimToken(token) {
		t.Errorf("got stored hash %q, want the hash of the returned token %q", *req.ClaimTokenHash, hashClaimToken(token))
	}
	if *req.ClaimTokenHash == token {
		t.Error("the raw token must never be what the row stores")
	}
	if req.ClaimedAt == nil {
		t.Error("expected claimed_at to be recorded")
	}
	if req.ClaimUserAgent != "Mozilla/5.0 (claimer)" {
		t.Errorf("got claim_user_agent %q, want the claiming user agent", req.ClaimUserAgent)
	}
}

// TestClaimApprovalPage_ShouldReportUnknownWhenTheRequestDoesNotExist keeps
// nonexistent IDs out of the claim machinery: nothing to bind, no cookie to
// set, and the SPA renders its own not-found state.
func TestClaimApprovalPage_ShouldReportUnknownWhenTheRequestDoesNotExist(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)

	res, err := svc.ClaimApprovalPage(context.Background(), "no-such-request", "", "Mozilla/5.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Outcome != ClaimPageUnknownRequest {
		t.Errorf("got outcome %v, want ClaimPageUnknownRequest", res.Outcome)
	}
}

// TestClaimApprovalPage_ShouldJudgeRevisitsOfAClaimedPage covers every way
// a claimed page can be revisited: the claiming browser returning with its
// cookie (across the IdP redirect), a second client without one, a wrong
// cookie, and the cookie-blocked heuristic's boundaries.
func TestClaimApprovalPage_ShouldJudgeRevisitsOfAClaimedPage(t *testing.T) {
	t.Parallel()

	const claimerUA = "Mozilla/5.0 (claimer)"

	tests := []struct {
		name string
		// claimAge backdates the claim before the revisit; zero leaves it
		// fresh.
		claimAge time.Duration
		// presentToken substitutes for the real cookie value when set;
		// presentReal presents the token the claim minted.
		presentReal  bool
		presentToken string
		userAgent    string
		want         ClaimPageOutcome
	}{
		{
			name:        "should match when the claiming cookie returns",
			presentReal: true,
			userAgent:   claimerUA,
			want:        ClaimPageMatched,
		},
		{
			name:        "should match on the cookie even when the user agent changed",
			presentReal: true,
			userAgent:   "Mozilla/5.0 (upgraded mid-flow)",
			want:        ClaimPageMatched,
		},
		{
			name:      "should reject a cookieless client with a different user agent",
			userAgent: "Slackbot-LinkExpanding 1.0",
			want:      ClaimPageRejected,
		},
		{
			name:         "should reject a wrong cookie even from the claiming user agent",
			presentToken: "not-the-minted-token",
			userAgent:    claimerUA,
			want:         ClaimPageRejected,
		},
		{
			name:      "should report cookie-blocked when the claiming user agent returns cookieless within the window",
			userAgent: claimerUA,
			want:      ClaimPageCookieBlocked,
		},
		{
			name:      "should reject the claiming user agent returning cookieless after the window",
			claimAge:  claimCookieBlockedWindow + time.Minute,
			userAgent: claimerUA,
			want:      ClaimPageRejected,
		},
		{
			name:      "should reject a cookieless revisit when both user agents are empty",
			userAgent: "",
			want:      ClaimPageRejected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newTestCertRequestService(t, time.Hour)
			requestID := mustCreateUserRequest(t, svc)

			// The empty-user-agent case needs the claim itself made with an
			// empty user agent, or the strings would differ trivially.
			claimUA := claimerUA
			if tt.userAgent == "" {
				claimUA = ""
			}
			token := claimPage(t, svc, requestID, claimUA)
			if tt.claimAge > 0 {
				backdateClaim(t, svc, requestID, tt.claimAge)
			}

			presented := tt.presentToken
			if tt.presentReal {
				presented = token
			}

			res, err := svc.ClaimApprovalPage(context.Background(), requestID, presented, tt.userAgent)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Outcome != tt.want {
				t.Errorf("got outcome %v, want %v", res.Outcome, tt.want)
			}
		})
	}
}

// TestClaimApprovalPage_ShouldTruncateAnOversizedUserAgent bounds what the
// attacker-controlled header can make the row store.
func TestClaimApprovalPage_ShouldTruncateAnOversizedUserAgent(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	requestID := mustCreateUserRequest(t, svc)

	claimPage(t, svc, requestID, strings.Repeat("a", claimUserAgentMaxLen+100))

	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading the request back: %v", err)
	}
	if len(req.ClaimUserAgent) != claimUserAgentMaxLen {
		t.Errorf("got stored user agent length %d, want %d", len(req.ClaimUserAgent), claimUserAgentMaxLen)
	}
}

// TestClaimApprovalPage_ShouldNotLetARacingLoserOverwriteTheClaim covers
// the WHERE claim_token_hash IS NULL guard. The single-connection sqlite
// harness serializes real concurrency, so the loser's state is staged
// directly: a row already claimed in the database, judged through
// claimApprovalPage with an in-memory req that still thinks it is
// unclaimed — exactly where a racing loser lands when its guarded UPDATE
// affects zero rows.
func TestClaimApprovalPage_ShouldNotLetARacingLoserOverwriteTheClaim(t *testing.T) {
	t.Parallel()

	svc := newTestCertRequestService(t, time.Hour)
	requestID := mustCreateUserRequest(t, svc)

	winnerToken := claimPage(t, svc, requestID, "Mozilla/5.0 (winner)")

	stale := &model.CertificateRequest{ID: requestID}
	res, err := svc.claimApprovalPage(context.Background(), stale, "", "Mozilla/5.0 (loser)")
	if err != nil {
		t.Fatalf("unexpected error for the racing loser: %v", err)
	}
	if res.Outcome != ClaimPageRejected {
		t.Errorf("got outcome %v, want ClaimPageRejected for the racing loser", res.Outcome)
	}

	// The winner's claim must have survived the loser's attempt.
	var req model.CertificateRequest
	if err := svc.db.First(&req, "id = ?", requestID).Error; err != nil {
		t.Fatalf("unexpected error reading the request back: %v", err)
	}
	if req.ClaimTokenHash == nil || *req.ClaimTokenHash != hashClaimToken(winnerToken) {
		t.Error("expected the winner's claim to survive the racing loser")
	}
	if req.ClaimUserAgent != "Mozilla/5.0 (winner)" {
		t.Errorf("got claim_user_agent %q, want the winner's", req.ClaimUserAgent)
	}
}
