//go:build e2e

package e2e

import (
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// Tier 2 ("browser"): the same server as tier 1, with the approval page
// driven in a real headless browser instead of a plain http.Client — the
// SPA against the real server: routing, CSP, cookies, the granted-vs-
// requested rendering. See docs/dev/e2e-testing-plan.md.

// An approval URL is how most people reach this app at all, and they reach
// it signed out. That has to land on the real /login screen rather than a
// sign-in prompt improvised by the approval page: /login is where a
// deployment's consent notice is shown, and a notice can only gate a
// sign-in it stands in front of.
func TestApproval_UnauthenticatedVisitorIsSentToTheLoginScreen(t *testing.T) {
	f := newFixture(t)
	browser := harness.StartBrowser(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	browser.Navigate(t, approvalURL, `[data-testid="login-view"]`)
	browser.WaitVisible(t, `[data-testid="sign-in-button"]`)
	browser.AssertNotPresent(t, `[data-testid="approve-button"]`)
}

func TestApproval_TrimmedOptionsShownStruckThroughBeforeApproval(t *testing.T) {
	f := newFixture(t)
	browser := harness.StartBrowser(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	browser.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLogin(t, "alice")

	// The client asks for more extensions than the harness server config
	// permits (see login_test.go's extensions assertion) — some must show
	// as trimmed (struck through — see OptionDiffList.svelte) before
	// anyone approves.
	browser.WaitVisible(t, `[data-testid="approval-view"]`)
	browser.WaitVisible(t, `[data-testid="option-trimmed"]`)
	browser.WaitVisible(t, `[data-testid="narrowed-warning"]`)
	browser.WaitVisible(t, `[data-testid="approve-button"]`)
}

// TestApproval_SecondClientOpeningSameLinkIsRefused proves the approval URL
// is spent by its first open (middleware.ApprovalClaimMiddleware): a
// different client following the same link never reaches sign-in at all.
//
// This supersedes the pre-claim journey where a second person could sign in
// and was refused by identity binding — that control still exists behind
// the claim and keeps its unit coverage (bindRequester, the claim service,
// and the middleware), but a second browser can no longer reach it.
func TestApproval_SecondClientOpeningSameLinkIsRefused(t *testing.T) {
	f := newFixture(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	// The first browser claims the link and proceeds normally.
	first := harness.StartBrowser(t)
	first.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	first.Click(t, `[data-testid="sign-in-button"]`)
	first.CompleteIdPLogin(t, "alice")
	first.WaitVisible(t, `[data-testid="approval-view"]`)

	// A different client — its own profile AND its own user agent, since
	// every chromedp instance otherwise shares one UA string and would read
	// as the claiming browser with cookies blocked — lands on the
	// spent-link page without ever being offered a sign-in.
	second := harness.StartBrowserWithUserAgent(t, "ssoossh-e2e-second-client/1.0")
	second.Navigate(t, approvalURL, `[data-testid="claim-already-opened"]`)
	second.AssertNotPresent(t, `[data-testid="sign-in-button"]`)
	second.AssertNotPresent(t, `[data-testid="approve-button"]`)

	// The same user agent without the claim cookie is read as the claiming
	// browser refusing cookies, and told so — the other refusal flavor.
	third := harness.StartBrowser(t)
	third.Navigate(t, approvalURL, `[data-testid="claim-cookies-blocked"]`)
	third.AssertNotPresent(t, `[data-testid="approve-button"]`)
}
