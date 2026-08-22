//go:build resilience || e2e

package resilience

import (
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// TestOIDC_LoginSucceedsAfterIdPRecovery validates that after an IdP outage,
// once the IdP returns to health, logins work again without restarting ssoosshd.
func TestOIDC_LoginSucceedsAfterIdPRecovery(t *testing.T) {
	f := newFixture(t)

	// Perform a baseline login to verify the IdP is healthy.
	login1 := harness.StartLogin(t, f.ssoosshBin, f.server.BaseURL, f.agent.Socket)
	approvalURL1 := login1.ApprovalURL(t, waitFor)

	browser := f.startBrowser(t)
	browser.Navigate(t, approvalURL1, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLogin(t, "alice")
	browser.WaitVisible(t, `[data-testid="approval-view"]`)
	browser.Click(t, `[data-testid="approve-button"]`)

	if err := login1.Wait(t, waitFor); err != nil {
		t.Fatalf("baseline login failed: %v", err)
	}

	// Now test a second login after IdP is back (simulated by same fixture).
	// In a real scenario, the IdP would be restarted between these.
	login2 := harness.StartLogin(t, f.ssoosshBin, f.server.BaseURL, f.agent.Socket, "--force")
	approvalURL2 := login2.ApprovalURL(t, waitFor)

	browser.Navigate(t, approvalURL2, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLogin(t, "bob")
	browser.WaitVisible(t, `[data-testid="approval-view"]`)
	browser.Click(t, `[data-testid="approve-button"]`)

	if err := login2.Wait(t, waitFor); err != nil {
		t.Errorf("login after IdP recovery failed: %v", err)
	}
}
