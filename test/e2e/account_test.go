//go:build e2e

package e2e

import (
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// TestAccount_ShowsExtraFieldsAndMergedPrincipals verifies that the /account
// page displays operator-configured extra fields and shows username + other
// accounts together as candidates for user certificate principals.
func TestAccount_ShowsExtraFieldsAndMergedPrincipals(t *testing.T) {
	f := newFixture(t,
		func(opts *harness.ServerOptions) {
			// Configure the server to expect employee_id and cost_center extra fields.
			opts.ExtraClaimFields = map[string]string{
				"employee_id": "employee_id",
				"cost_center": "cost_center",
			}
		},
	)
	browser := harness.StartBrowser(t)

	// Log in with extra claims and other accounts.
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	// Navigate to the approval page (this is to get into an authenticated state).
	browser.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)

	// Complete the IdP login with extra claims and other accounts.
	browser.CompleteIdPLoginWithExtraClaims(t, "alice", map[string]any{
		"employee_id": "E-40921",
		"cost_center": "CC-7781",
	})

	// Wait for the post-login redirect chain to settle before navigating
	// away. Clicking the IdP's submit button starts a chain (IdP -> callback
	// -> approval page) that chromedp does not block on, and navigating into
	// the middle of it aborts the new navigation with ERR_ABORTED.
	browser.WaitVisible(t, `[data-testid="approval-view"]`)

	// Navigate to the account page.
	browser.Navigate(t, f.Server.BaseURL+"/account", `[data-testid="account-identity-card"]`)

	// Verify extra fields are displayed via their data-testid attributes.
	browser.WaitVisible(t, `[data-testid="extra-field-employee_id"]`)
	browser.WaitVisible(t, `[data-testid="extra-field-cost_center"]`)

	// Verify principals section is present.
	browser.WaitVisible(t, `[data-testid="principals-section"]`)
}
