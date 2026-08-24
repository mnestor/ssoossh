//go:build e2e

package e2e

import (
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

const (
	adminGroup   = "admins"
	auditorGroup = "auditors"
)

// TestServiceCodes_AdminListSearch verifies the admin list page loads correctly
// and the search and pager controls are present.
func TestServiceCodes_AdminListSearch(t *testing.T) {
	f := newFixture(t, func(opts *harness.ServerOptions) {
		opts.AdminRequireGroup = adminGroup
		opts.AuditorGroup = auditorGroup
		opts.ServiceAccountsField = "service_accounts"
	})

	browser := harness.StartBrowser(t)
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)

	// Admin logs in with admin group
	approvalURL := login.ApprovalURL(t, waitFor)
	browser.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLoginWithGroups(t, "alice", []string{adminGroup})
	browser.WaitVisible(t, `[data-testid="approval-view"]`)
	browser.Click(t, `[data-testid="approve-button"]`)

	// Navigate to admin service codes page
	browser.Navigate(t, f.Server.BaseURL+"/admin/service-codes", `[data-testid="search-enrollments"]`)

	// Verify page loaded with list empty state (no enrollments in fresh database)
	browser.WaitVisible(t, `text="No service enrollment codes found"`)

	// Verify search input exists
	browser.WaitVisible(t, `[data-testid="search-enrollments"]`)

	// Verify pager exists
	browser.WaitVisible(t, `[data-testid="enrollments-pager"]`)
}

// TestServiceCodes_AdminPageRequiresAdminGroup verifies that accessing the admin
// page requires admin group membership.
func TestServiceCodes_AdminPageRequiresAdminGroup(t *testing.T) {
	f := newFixture(t, func(opts *harness.ServerOptions) {
		opts.AdminRequireGroup = adminGroup
		opts.AuditorGroup = auditorGroup
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

	// Try to navigate to admin service codes page
	// Without admin group, should redirect to login or show 403
	browser.Navigate(t, f.Server.BaseURL+"/admin/service-codes", `[data-testid="sign-in-button"]`)
	browser.WaitVisible(t, `[data-testid="sign-in-button"]`)
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
	browser.Navigate(t, f.Server.BaseURL+"/service-codes", `text="Service enrollment codes"`)

	// Verify the page loaded
	browser.WaitVisible(t, `text="Service enrollment codes"`)
}
