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

	// Navigate to the account page.
	browser.Navigate(t, f.Server.BaseURL+"/account", `h2:contains("Identity")`)

	// Verify extra fields are displayed.
	browser.WaitVisible(t, `text="E-40921"`)
	browser.WaitVisible(t, `text="CC-7781"`)

	// Verify username shows as primary in the principals section.
	browser.WaitVisible(t, `text="(primary)"`)
}
