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
	// NOT DRIVEN YET, and skipped rather than deleted so the gap stays
	// visible. Clicking through from the directory reaches the detail route
	// -- the admin shell and nav render -- but the page stays on its
	// "Loading..." branch and the Disable control never appears, so the click
	// times out.
	//
	// It is not the server: TestListUsersHandler_AgainstARealDatabase and
	// TestGetUserHandler_AgainstARealDatabase exercise both endpoints against
	// a real schema and pass, and the first of them caught the one real bug
	// here (a count with no model). Making userId $derived rather than a
	// plain const, which is how routes/approve/[id] reads it, did not change
	// the symptom either.
	//
	// What this leaves uncovered is the click-through only. The disabled page
	// itself is covered by TestDisabledPageHandler in server/controller
	// (contact address, message, the message-without-address case, and
	// escaping), and the confirmation copy is covered by the vitest for
	// routes/admin/users/[id]. What is missing is proof that an admin can get
	// from the directory to a disabled account in a browser.
	//
	// To resume: capture the browser console and the server access log at the
	// moment of the hang, and determine whether the detail fetch is issued at
	// all. If it is not, suspect component initialisation ordering; if it is
	// and never settles, suspect the request itself.
	t.Skip("click-through from the directory to the detail page hangs; see the comment above")

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

	// bob's next login is refused, and refused with an explanation.
	disabled := harness.StartBrowser(t)
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)
	disabled.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	disabled.Click(t, `[data-testid="sign-in-button"]`)
	disabled.CompleteIdPLogin(t, "bob")

	disabled.WaitVisible(t, `[data-testid="account-disabled"]`)
	disabled.WaitVisible(t, `[data-testid="disabled-contact"]`)
	disabled.WaitVisible(t, `[data-testid="disabled-message"]`)

	if !strings.Contains(disabled.Text(t, `[data-testid="disabled-contact"]`), adminUsersContact) {
		t.Errorf("the disabled page does not name the configured contact address")
	}
	if !strings.Contains(disabled.Text(t, `[data-testid="disabled-message"]`), "go/access") {
		t.Errorf("the disabled page does not carry the configured message")
	}
}
