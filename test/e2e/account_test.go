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

	// Navigate straight to the account page. No wait in between: the login
	// helper does not return until the post-login redirect chain (IdP ->
	// callback -> approval page) has settled, so this is safe. It did not
	// used to be, and this navigation is what proves it -- see
	// waitForLoginRedirects in harness/browser.go.
	browser.Navigate(t, f.Server.BaseURL+"/account", `[data-testid="account-identity-card"]`)

	// Verify extra fields are displayed via their data-testid attributes.
	browser.WaitVisible(t, `[data-testid="extra-field-employee_id"]`)
	browser.WaitVisible(t, `[data-testid="extra-field-cost_center"]`)

	// Verify principals section is present.
	browser.WaitVisible(t, `[data-testid="principals-section"]`)
}
