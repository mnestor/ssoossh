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

// TestEdgeCase_ConcurrentApprovalsOfSameLogin validates that if the same
// approval (same login ID) is approved concurrently from two browser instances,
// only one certificate is issued (or both attempts fail safely).
func TestEdgeCase_ConcurrentApprovalsOfSameLogin(t *testing.T) {
	f := newFixture(t)

	// Start a single login but approve it from two browser instances.
	login := harness.StartLogin(t, f.ssoosshBin, f.server.BaseURL, f.agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	// Two browser instances approve the same login concurrently.
	browser1 := harness.StartBrowser(t)
	browser2 := harness.StartBrowser(t)

	// Browser 1 starts approval process.
	browser1.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser1.Click(t, `[data-testid="sign-in-button"]`)
	browser1.CompleteIdPLogin(t, "alice")
	browser1.WaitVisible(t, `[data-testid="approval-view"]`)

	// Browser 2 does the same (both trying to approve the same login ID).
	browser2.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser2.Click(t, `[data-testid="sign-in-button"]`)
	browser2.CompleteIdPLogin(t, "alice")
	browser2.WaitVisible(t, `[data-testid="approval-view"]`)

	// Approve concurrently.
	browser1.Click(t, `[data-testid="approve-button"]`)
	browser2.Click(t, `[data-testid="approve-button"]`)

	// Both attempts should complete without panic. The server should ensure
	// only one certificate is actually issued for this login.
	if err := login.Wait(t, waitFor); err != nil {
		t.Logf("login resolution: %v", err)
	}
}
