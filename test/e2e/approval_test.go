//go:build e2e

package e2e

import (
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// Tier 2 ("browser"): the same server as tier 1, with the approval page
// driven in a real headless browser instead of a plain http.Client — the
// SPA against the real server: routing, CSP, cookies, the granted-vs-
// requested rendering. See docs/e2e-testing-plan.md.

func TestApproval_UnauthenticatedVisitorSeesSignInNotApprove(t *testing.T) {
	f := newFixture(t)
	browser := harness.StartBrowser(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	browser.Navigate(t, approvalURL, `[data-testid="load-failure-unauthenticated"]`)
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

func TestApproval_SecondIdentityOpeningSameLinkIsRefused(t *testing.T) {
	f := newFixture(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	// Two independent browsers stand in for two different people: each
	// chromedp instance gets its own profile, so their cookie jars (and
	// therefore OIDC sessions) don't share anything.
	first := harness.StartBrowser(t)
	first.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	first.Click(t, `[data-testid="sign-in-button"]`)
	first.CompleteIdPLogin(t, "alice")
	first.WaitVisible(t, `[data-testid="approval-view"]`)

	second := harness.StartBrowser(t)
	second.Navigate(t, approvalURL, `[data-testid="sign-in-button"]`)
	second.Click(t, `[data-testid="sign-in-button"]`)
	second.CompleteIdPLogin(t, "mallory")
	second.WaitVisible(t, `[data-testid="load-failure-forbidden"]`)
	second.AssertNotPresent(t, `[data-testid="approve-button"]`)
}
