//go:build e2e

package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// TestServiceCodes_AdminListSearch verifies the admin list page loads correctly
// and the search and pager controls are present.
func TestServiceCodes_AdminListSearch(t *testing.T) {
	f := newFixture(t, func(opts *harness.ServerOptions) {
		opts.AdminRequireGroup = e2eAdminGroup
		opts.AuditorGroup = e2eAuditorGroup
		opts.ServiceAccountsField = "service_accounts"
	})

	browser := harness.StartBrowser(t)
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)

	// Admin logs in with admin group
	approvalURL := login.ApprovalURL(t, waitFor)
	browser.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLoginWithGroups(t, "alice", []string{e2eAdminGroup})
	browser.WaitVisible(t, `[data-testid="approval-view"]`)
	browser.Click(t, `[data-testid="approve-button"]`)

	// Navigate to admin service codes page
	browser.Navigate(t, f.Server.BaseURL+"/admin/service-codes", `[data-testid="search-enrollments"]`)

	// A fresh database has no enrollments, so this is the empty state.
	browser.WaitVisible(t, `[data-testid="enrollments-empty"]`)
	browser.WaitVisible(t, `[data-testid="search-enrollments"]`)

	// Pager renders nothing when a single page holds everything, which is
	// its documented contract -- asserting it is present on an empty list
	// would assert the opposite of what the component does.
	browser.AssertNotPresent(t, `[data-testid="enrollments-pager"]`)
}

// TestServiceCodes_AdminPageRequiresAdminGroup verifies that accessing the admin
// page requires admin group membership.
func TestServiceCodes_AdminPageRequiresAdminGroup(t *testing.T) {
	f := newFixture(t, func(opts *harness.ServerOptions) {
		opts.AdminRequireGroup = e2eAdminGroup
		opts.AuditorGroup = e2eAuditorGroup
		opts.ServiceAccountsField = "service_accounts"
	})

	browser := harness.StartBrowser(t)
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)

	// Non-admin user logs in (no admin group)
	approvalURL := login.ApprovalURL(t, waitFor)
	browser.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLogin(t, "alice")
	browser.WaitVisible(t, `[data-testid="approval-view"]`)

	// An authenticated user without auditor access stays on the page and is
	// told why. Bouncing them to login would be a loop: they are already
	// signed in, and signing in again cannot grant admin.
	browser.Navigate(t, f.Server.BaseURL+"/admin/service-codes", `[data-testid="admin-access-denied"]`)
	browser.AssertNotPresent(t, `[data-testid="search-enrollments"]`)
}

// TestServiceCodes_OwnerPageLoads verifies that the owner's service codes page
// loads after authentication.
func TestServiceCodes_OwnerPageLoads(t *testing.T) {
	f := newFixture(t, func(opts *harness.ServerOptions) {
		opts.ServiceAccountsField = "service_accounts"
	})

	browser := harness.StartBrowser(t)
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)

	// User goes through approval flow to log in
	approvalURL := login.ApprovalURL(t, waitFor)
	browser.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLogin(t, "alice")
	browser.WaitVisible(t, `[data-testid="approval-view"]`)

	// Navigate to owner's service codes page
	browser.Navigate(t, f.Server.BaseURL+"/service-codes", `[data-testid="service-codes-heading"]`)

	// Verify the page loaded
	browser.WaitVisible(t, `[data-testid="service-codes-heading"]`)
}

// TestServiceCodes_OwnerDrillsIntoAnAccount walks the page's whole shape in a
// real browser: the accounts the identity holds, one account's codes, then
// one code's detail.
//
// The account level is the part worth driving end to end. Ownership is
// membership in the service account rather than a stored owner (see
// docs/proposals/enrollment-group-ownership.md), so what this proves is that
// a browser session holding an account reaches a code nothing in that session
// approved.
func TestServiceCodes_OwnerDrillsIntoAnAccount(t *testing.T) {
	// A code approved through the API by alice, who holds the account.
	svc := newServiceFixture(t)
	keyPath := filepath.Join(t.TempDir(), "svckey")
	svc.enroll(t, keyPath)

	// A second identity, holding the same account but having approved
	// nothing, opens the page.
	browser := harness.StartBrowser(t)
	browser.Navigate(t, svc.Server.BaseURL+"/login", `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLoginWithExtraClaims(t, "bob",
		map[string]any{serviceAccountClaim: []string{svc.Account}})

	// Level one: the accounts held, not the codes approved.
	browser.Navigate(t, svc.Server.BaseURL+"/service-codes", `[data-testid="service-account-row"]`)
	if got := browser.Text(t, `[data-testid="service-account-row"]`); !strings.Contains(got, svc.Account) {
		t.Fatalf("the account row does not name %q: %q", svc.Account, got)
	}

	// Level two: that account's codes, reached by clicking it. The code was
	// approved by alice and is being read by bob, which is the whole point.
	browser.Click(t, `[data-testid="service-account-row"]`)
	browser.WaitVisible(t, `[data-testid="service-codes-back"]`)
	browser.WaitVisible(t, `[data-testid="service-code-row"]`)

	// Level three: one code's detail panel.
	browser.Click(t, `[data-testid="service-code-row"]`)
	browser.WaitVisible(t, `[data-testid="service-code-account"]`)
	if got := browser.Text(t, `[data-testid="service-code-account"]`); got != svc.Account {
		t.Errorf("the detail panel names %q, want %q", got, svc.Account)
	}
}
