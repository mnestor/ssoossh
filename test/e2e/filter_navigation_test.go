//go:build e2e

package e2e

import (
	"net/url"
	"testing"

	"github.com/mnestor/ssoossh/server/service"
	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// desktopWidth and desktopHeight put the browser above the `lg` breakpoint,
// where the nav rail is a column beside the page rather than a drawer.
const (
	desktopWidth  = 1280
	desktopHeight = 900
)

// railDashboardLink is the rail's Dashboard destination, and
// railDashboardCurrent the same link once the router has landed on it.
const (
	railDashboardLink    = `[data-testid="app-rail"] a[href="/dashboard"]`
	railDashboardCurrent = railDashboardLink + `[aria-current="page"]`
)

// filterNavigationCase is one list page that keeps its filters in the
// address bar: where it lives, what shows once it has loaded, how one filter
// is changed, and the address that change must leave behind.
type filterNavigationCase struct {
	name    string
	path    string
	ready   string
	change  func(t *testing.T, b *harness.Browser)
	wantURL string
}

// TestFilterNavigation_RailStillNavigatesAfterAFilterChange is issue #178:
// changing a filter on a page that writes its filters to the address bar
// left the nav rail dead until a reload. Each case changes one filter,
// checks the address carries it (a filtered view must survive a reload and
// be shareable), then clicks the rail and checks the router really moved.
func TestFilterNavigation_RailStillNavigatesAfterAFilterChange(t *testing.T) {
	f := newFixture(t, func(o *harness.ServerOptions) {
		o.AuditorGroup = e2eAuditorGroup
	})

	loginAction := string(service.AuditAuthLogin)
	cases := []filterNavigationCase{
		{
			name:    "should navigate from the rail when the history status filter changed",
			path:    "/logs/me",
			ready:   `[data-testid="status-filter-live"]`,
			change:  func(t *testing.T, b *harness.Browser) { b.Click(t, `[data-testid="status-filter-live"]`) },
			wantURL: "/logs/me?status=live",
		},
		{
			name:    "should navigate from the rail when the history type filter changed",
			path:    "/logs/me",
			ready:   `[data-testid="type-filter-user"]`,
			change:  func(t *testing.T, b *harness.Browser) { b.Click(t, `[data-testid="type-filter-user"]`) },
			wantURL: "/logs/me?type=user",
		},
		{
			name:    "should navigate from the rail when the history outcome filter changed",
			path:    "/logs/me",
			ready:   `[data-testid="outcome-filter-denied"]`,
			change:  func(t *testing.T, b *harness.Browser) { b.Click(t, `[data-testid="outcome-filter-denied"]`) },
			wantURL: "/logs/me?outcome=denied",
		},
		{
			name:    "should navigate from the rail when the admin certificate status filter changed",
			path:    "/admin/certificates",
			ready:   `[data-testid="status-filter-expired"]`,
			change:  func(t *testing.T, b *harness.Browser) { b.Click(t, `[data-testid="status-filter-expired"]`) },
			wantURL: "/admin/certificates?status=expired",
		},
		{
			name:  "should navigate from the rail when the audit action filter changed",
			path:  "/admin/audit",
			ready: `[data-testid="audit-action-filter"]`,
			change: func(t *testing.T, b *harness.Browser) {
				b.SelectValue(t, `[data-testid="audit-action-filter"]`, loginAction)
			},
			wantURL: "/admin/audit?action=" + url.QueryEscape(loginAction),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A browser per case: a page that hangs takes its browser with
			// it, and the next case must still say what it found.
			browser := signInAsAuditorAt(t, f, tc.path, tc.ready)

			tc.change(t, browser)
			browser.WaitURL(t, tc.wantURL)

			browser.Click(t, railDashboardLink)
			browser.WaitURL(t, "/dashboard")
			browser.WaitVisible(t, railDashboardCurrent)
		})
	}
}

// signInAsAuditorAt opens path in a fresh desktop-sized browser, signs in as
// an auditor (who reaches both the caller's own history and the admin
// lists, which is every page that syncs its filters), and waits for ready.
func signInAsAuditorAt(t *testing.T, f *fixture, path, ready string) *harness.Browser {
	t.Helper()

	browser := harness.StartBrowser(t)
	browser.SetViewport(t, desktopWidth, desktopHeight)
	browser.Navigate(t, f.Server.BaseURL+path, `[data-testid="login-view"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLoginWithGroups(t, "auditor", []string{e2eAuditorGroup})
	browser.WaitVisible(t, ready)
	return browser
}
