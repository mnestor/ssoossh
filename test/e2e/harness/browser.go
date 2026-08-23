//go:build e2e || resilience || load

package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// browserWaitFor bounds every chromedp action — long enough for a real page
// load and OIDC redirect chain over loopback, short enough that a genuine
// hang fails the test rather than the CI job's own timeout.
const browserWaitFor = 15 * time.Second

// Browser is a headless Chrome/Chromium instance driving the approval page
// for tier 2. Two independent Browser values have independent cookie jars
// (chromedp gives each ExecAllocator its own profile directory) — that's
// what stands in for "two different people" in the requester-binding test.
//
// chromedp is the harness's first choice per docs/dev/e2e-testing-plan.md
// ("Driving the browser"): pure Go, no second toolchain, and hosted Ubuntu
// runners already ship Chrome. If the SPA's async loading ever fights its
// lack of auto-waiting badly enough to matter, this file is the whole seam
// — swap to Playwright here without touching the test files.
type Browser struct {
	ctx         context.Context
	cancelAlloc context.CancelFunc
	cancelCtx   context.CancelFunc
}

// StartBrowser launches a fresh headless browser instance, torn down via
// t.Cleanup. Set SSOOSSH_E2E_HEADED=1 to watch it run while iterating
// locally (see test/e2e/README.md).
func StartBrowser(t *testing.T) *Browser {
	t.Helper()

	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		// Headless Chrome under a container UID with no user namespace
		// commonly can't use its setuid sandbox helper; disabling it is
		// standard practice for CI/containerized runs, same as the sample
		// scripts Google itself publishes for headless CI.
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	if os.Getenv("SSOOSSH_E2E_HEADED") != "" {
		opts = append(opts, chromedp.Flag("headless", false))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)

	// The first Run on a fresh chromedp context allocates the browser and
	// binds the target's session lifetime to whatever context object it's
	// given — chromedp.Run's own doc comment warns against wrapping that
	// specific call in a timeout, since the target tears down the moment
	// that context is later canceled, timeout or not. ctx itself is
	// unbounded and torn down by t.Cleanup below; a hang here is instead
	// caught by the test's own -timeout.
	if err := chromedp.Run(ctx); err != nil {
		cancelCtx()
		cancelAlloc()
		t.Fatalf("harness: failed to start the browser (is chromium/chrome installed?): %v", err)
	}

	b := &Browser{ctx: ctx, cancelAlloc: cancelAlloc, cancelCtx: cancelCtx}
	t.Cleanup(func() {
		b.cancelCtx()
		b.cancelAlloc()
	})
	return b
}

// Navigate opens url and waits for selector to be visible.
func (b *Browser) Navigate(t *testing.T, url, waitVisibleSelector string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Run(ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(waitVisibleSelector, chromedp.ByQuery),
	); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: browser navigation to %s (waiting for %s) failed: %v", url, waitVisibleSelector, err)
	}
}

// WaitVisible blocks until selector is visible in the current page.
func (b *Browser) WaitVisible(t *testing.T, selector string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.WaitVisible(selector, chromedp.ByQuery)); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: waiting for %s failed: %v", selector, err)
	}
}

// AssertNotPresent fails the test if selector matches any node.
func (b *Browser) AssertNotPresent(t *testing.T, selector string) {
	t.Helper()

	var nodeCount int
	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.EvaluateAsDevTools(
		fmt.Sprintf("document.querySelectorAll(%q).length", selector), &nodeCount,
	)); err != nil {
		t.Fatalf("harness: checking for absence of %s failed: %v", selector, err)
	}
	if nodeCount != 0 {
		b.screenshotOnFailure(t)
		t.Errorf("expected no elements matching %s, found %d", selector, nodeCount)
	}
}

// Click clicks the (visible) element matching selector.
func (b *Browser) Click(t *testing.T, selector string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
	); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: clicking %s failed: %v", selector, err)
	}
}

// ClickIfPresent clicks selector if the page has it right now, and reports
// whether it did. Unlike Click it neither waits for the element nor fails
// when it is missing — for a deliberate double-click, where the first click
// is entitled to have replaced the button before the second one lands.
func (b *Browser) ClickIfPresent(t *testing.T, selector string) bool {
	t.Helper()

	var clicked bool
	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.EvaluateAsDevTools(
		fmt.Sprintf("(() => { const el = document.querySelector(%q); if (!el) { return false; } el.click(); return true; })()", selector),
		&clicked,
	)); err != nil {
		t.Fatalf("harness: clicking %s if present failed: %v", selector, err)
	}
	return clicked
}

// CompleteIdPLogin fills and submits the harness IdP's real login form
// (data-testid="idp-username"/"idp-submit" — see idp.go's renderLoginForm)
// with username, then follows the resulting redirect chain back into the
// server. Call after navigating to a URL that reaches /auth/login.
func (b *Browser) CompleteIdPLogin(t *testing.T, username string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Run(ctx,
		chromedp.WaitVisible(`[data-testid="idp-login-form"]`, chromedp.ByQuery),
		chromedp.SendKeys(`[data-testid="idp-username"]`, username, chromedp.ByQuery),
		chromedp.Click(`[data-testid="idp-submit"]`, chromedp.ByQuery),
	); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: completing the IdP login form failed: %v", err)
	}
}

// screenshotOnFailure best-effort captures a screenshot and the current URL
// to the test's artifact directory — debugging a browser failure from an
// assertion message alone isn't realistic.
func (b *Browser) screenshotOnFailure(t *testing.T) {
	t.Helper()

	var buf []byte
	var currentURL string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := chromedp.Run(ctx,
		chromedp.Location(&currentURL),
		chromedp.CaptureScreenshot(&buf),
	); err != nil {
		t.Logf("harness: failed to capture failure screenshot: %v", err)
		return
	}

	dir := artifactDir(t)
	if dir == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "browser-failure.png"), buf, 0o600); err != nil {
		t.Logf("harness: failed to write failure screenshot: %v", err)
	}
	writeArtifact(t, "browser-failure-url.txt", []byte(currentURL))
}
