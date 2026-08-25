//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// The groups and copy the server is configured with, named once so the
// server config and the browser logins cannot drift apart. Two spellings of
// a group is invisible: GrantsAuditor simply denies, the page never renders,
// and the failure looks like a slow selector rather than a mismatch.
const (
	adminUsersGroup      = "ssoossh-admins"
	adminUsersAuditors   = "ssoossh-auditors"
	adminUsersContact    = "it-help@corp.example"
	adminUsersMessage    = "Open a ticket at go/access to appeal."
	adminUsersGracePerio = "2h"
)

// newAdminUsersFixture starts a server that recognises the admin and auditor
// groups and carries the disabled-account copy.
func newAdminUsersFixture(t *testing.T) *fixture {
	t.Helper()

	return newFixture(t, func(o *harness.ServerOptions) {
		o.AdminRequireGroup = adminUsersGroup
		o.AuditorGroup = adminUsersAuditors
		o.AdminDisableGracePeriod = adminUsersGracePerio
		o.AdminContactEmail = adminUsersContact
		o.AdminDisabledMessage = adminUsersMessage
	})
}

// signIn drives one browser through a login as username with groups, leaving
// it on the approval page. A users row is created by the login itself, which
// is how a test gets a user for the admin directory to list.
func signIn(t *testing.T, f *fixture, browser *harness.Browser, username string, groups []string) {
	t.Helper()

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	browser.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	if len(groups) == 0 {
		browser.CompleteIdPLogin(t, username)
	} else {
		browser.CompleteIdPLoginWithGroups(t, username, groups)
	}
	// Settle the post-login redirect chain before navigating anywhere: it is
	// still in flight when the click returns, and navigating into the middle
	// of it aborts with ERR_ABORTED.
	browser.WaitVisible(t, `[data-testid="approval-view"]`)
}

// TestAdminUsers_DirectoryListsAUserWhoHasLoggedIn covers the read path: an
// admin reaches the directory and finds someone in it.
func TestAdminUsers_DirectoryListsAUserWhoHasLoggedIn(t *testing.T) {
	f := newAdminUsersFixture(t)

	// bob logs in first, which is what creates his users row.
	signIn(t, f, harness.StartBrowser(t), "bob", nil)

	admin := harness.StartBrowser(t)
	signIn(t, f, admin, "alice", []string{adminUsersGroup})

	admin.Navigate(t, f.Server.BaseURL+"/admin/users", `[data-testid="search-users"]`)
	admin.WaitVisible(t, `[data-testid="search-users"]`)
}

// TestAdminUsers_NonAdminIsRefusedTheDirectory covers the negative: someone
// signed in without auditor access is told so, rather than being bounced to a
// login screen they have already satisfied.
func TestAdminUsers_NonAdminIsRefusedTheDirectory(t *testing.T) {
	f := newAdminUsersFixture(t)

	plain := harness.StartBrowser(t)
	signIn(t, f, plain, "bob", nil)

	plain.Navigate(t, f.Server.BaseURL+"/admin/users", `[data-testid="admin-access-denied"]`)
	plain.AssertNotPresent(t, `[data-testid="search-users"]`)
}

// TestAdminUsers_DisabledUserSeesTheDisabledPage is the whole point of the
// feature: an admin disables someone, and that person's next login lands on a
// page naming the configured contact and message rather than a bare error.
func TestAdminUsers_DisabledUserSeesTheDisabledPage(t *testing.T) {
	f := newAdminUsersFixture(t)

	// bob logs in once so a users row exists to disable.
	signIn(t, f, harness.StartBrowser(t), "bob", nil)

	admin := harness.StartBrowser(t)
	signIn(t, f, admin, "alice", []string{adminUsersGroup})

	// Find bob through the directory rather than by constructing his id: the
	// point is that an admin can get from the list to the action.
	admin.Navigate(t, f.Server.BaseURL+"/admin/users", `[data-testid="search-users"]`)
	admin.Click(t, `a[href^="/admin/users/"]`)

	// The confirmation has to name the consequence before it is accepted, so
	// opening it is a distinct step from confirming it.
	admin.Click(t, `[data-testid="disable-user"]`)
	admin.WaitVisible(t, `[data-testid="disable-consequences"]`)
	admin.Click(t, `[data-testid="confirm-disable"]`)
	admin.WaitVisible(t, `[data-testid="user-disabled-badge"]`)

	// The disabled page itself, loaded in a browser that is not mid-redirect.
	//
	// The remaining hop -- a disabled user's login being bounced here -- is
	// not driven in the browser. Following the IdP submit through the
	// callback's 302 into a 403 does not settle in this harness, and it is
	// covered on the server instead: the callback's redirect is asserted by
	// TestCallbackHandler_DisabledUserIsRedirected, and the page's contents
	// by TestDisabledPageHandler, both in server/controller.
	fresh := harness.StartBrowser(t)
	fresh.Navigate(t, f.Server.BaseURL+"/auth/disabled", `[data-testid="account-disabled"]`)

	if !strings.Contains(fresh.Text(t, `[data-testid="disabled-contact"]`), adminUsersContact) {
		t.Errorf("the disabled page does not name the configured contact address")
	}
	if !strings.Contains(fresh.Text(t, `[data-testid="disabled-message"]`), "go/access") {
		t.Errorf("the disabled page does not carry the configured message")
	}
}
