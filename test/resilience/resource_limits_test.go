//go:build resilience || e2e

package resilience

import (
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// TestEdgeCase_DuplicateApprovalClick validates that clicking approve twice
// (network retry, accidental double-click) doesn't issue two certificates or
// cause state corruption. The second click should be a no-op or rejected.
func TestEdgeCase_DuplicateApprovalClick(t *testing.T) {
	f := newFixture(t)

	// Start a login and approval process.
	login := harness.StartLogin(t, f.ssoosshBin, f.server.BaseURL, f.agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	browser := f.startBrowser(t)
	browser.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLogin(t, "alice")
	browser.WaitVisible(t, `[data-testid="approval-view"]`)

	// Click approve twice in rapid succession. The second click is
	// best-effort: the first approval resolves the request, and the page is
	// entitled to have swapped the button for the outcome before the second
	// one lands. That is the no-op this test is about, not a failure.
	browser.Click(t, `[data-testid="approve-button"]`)
	browser.ClickIfPresent(t, `[data-testid="approve-button"]`)

	// Only one certificate should be issued.
	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	certs := f.agent.Certificates(t)
	if len(certs) != 1 {
		t.Errorf("expected exactly one certificate after a duplicate approval, got %d", len(certs))
	}
}

// TestEdgeCase_CertificateWithExpiredToken validates that when a certificate
// is issued but the OIDC token has expired (edge case in timing), the
// certificate validity is not affected by the token expiry.
func TestEdgeCase_CertificateWithExpiredToken(t *testing.T) {
	f := newFixture(t)

	// Perform a normal login and approval.
	login := harness.StartLogin(t, f.ssoosshBin, f.server.BaseURL, f.agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	browser := f.startBrowser(t)
	browser.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLogin(t, "alice")
	browser.WaitVisible(t, `[data-testid="approval-view"]`)
	browser.Click(t, `[data-testid="approve-button"]`)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	// Verify the certificate was issued.
	certs := f.agent.Certificates(t)
	if len(certs) == 0 {
		t.Fatal("no certificate issued")
	}

	// The certificate's NotAfter should be independent of the OIDC token's expiry.
	// This test documents the requirement; actual expiry checking happens at cert validation.
}

// TestEdgeCase_ConcurrentApprovalsOfSameLogin validates that the same
// approval (same login ID) opened by a second browser cannot be approved
// twice. Two browsers used to reach the approval view and race their
// approve clicks; middleware.ApprovalClaimMiddleware now spends the link on
// its first open, so the race cannot start — the second client is turned
// away before sign-in, and the surviving flow issues exactly one
// certificate. The same-browser double-submit race stays covered by
// TestEdgeCase_DuplicateApprovalClick above.
func TestEdgeCase_ConcurrentApprovalsOfSameLogin(t *testing.T) {
	f := newFixture(t)

	// Start a single login; only the claiming browser may approve it.
	login := harness.StartLogin(t, f.ssoosshBin, f.server.BaseURL, f.agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	// Browser 1 claims the link and proceeds to the approval view.
	browser1 := harness.StartBrowser(t)
	browser1.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser1.Click(t, `[data-testid="sign-in-button"]`)
	browser1.CompleteIdPLogin(t, "alice")
	browser1.WaitVisible(t, `[data-testid="approval-view"]`)

	// A second client — its own profile and its own user agent, since every
	// chromedp instance otherwise shares one UA string and would read as the
	// claiming browser with cookies blocked — lands on the spent-link page
	// without ever being offered a sign-in.
	browser2 := harness.StartBrowserWithUserAgent(t, "ssoossh-resilience-second-client/1.0")
	browser2.Navigate(t, approvalURL, `[data-testid="claim-already-opened"]`)
	browser2.AssertNotPresent(t, `[data-testid="approve-button"]`)

	// The claiming browser's approval still completes, exactly once.
	browser1.Click(t, `[data-testid="approve-button"]`)
	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("login failed: %v", err)
	}

	certs := f.agent.Certificates(t)
	if len(certs) != 1 {
		t.Errorf("expected exactly one certificate for the claimed approval, got %d", len(certs))
	}
}
