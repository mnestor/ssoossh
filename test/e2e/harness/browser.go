//go:build e2e || resilience || load

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

	// console records what the page wrote to its console, uncaught
	// exceptions, and the browser's own log (failed loads included), so a
	// failure says what the page was doing and not only what it looked
	// like. Written by recordConsole, read by screenshotOnFailure.
	console *lockedBuffer
}

// StartBrowser launches a fresh headless browser instance, torn down via
// t.Cleanup. Set SSOOSSH_E2E_HEADED=1 to watch it run while iterating
// locally (see test/e2e/README.md).
func StartBrowser(t *testing.T) *Browser {
	t.Helper()

	return StartBrowserWithUserAgent(t, "")
}

// StartBrowserWithUserAgent is StartBrowser with a User-Agent override, or
// the browser's own with "". The approval-claim control compares user
// agents to tell "the claiming browser without its cookie" from "a
// different client", and every chromedp instance in one suite otherwise
// sends the same UA string — which reads as the former. A browser standing
// in for a genuinely different client must therefore set its own.
func StartBrowserWithUserAgent(t *testing.T, userAgent string) *Browser {
	t.Helper()

	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	if userAgent != "" {
		opts = append(opts, chromedp.UserAgent(userAgent))
	}
	opts = append(opts,
		// Headless Chrome under a container UID with no user namespace
		// commonly can't use its setuid sandbox helper; disabling it is
		// standard practice for CI/containerized runs, same as the sample
		// scripts Google itself publishes for headless CI.
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
	)
	if os.Getenv("SSOOSSH_E2E_HEADED") != "" {
		opts = append(opts, chromedp.VisibleWindow)
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)

	// The first Do on a fresh chromedp context allocates the browser and
	// binds the target's session lifetime to whatever context object it's
	// given — chromedp.Run's own doc comment warns against wrapping that
	// specific call in a timeout, since the target tears down the moment
	// that context is later canceled, timeout or not. ctx itself is
	// unbounded and torn down by t.Cleanup below; a hang here is instead
	// caught by the test's own -timeout.
	if err := chromedp.Do(ctx); err != nil {
		cancelCtx()
		cancelAlloc()
		t.Fatalf("harness: failed to start the browser (is chromium/chrome installed?): %v", err)
	}

	b := &Browser{ctx: ctx, cancelAlloc: cancelAlloc, cancelCtx: cancelCtx, console: &lockedBuffer{}}
	done := b.recordConsole()
	t.Cleanup(func() {
		b.cancelCtx()
		b.cancelAlloc()
		<-done
	})
	return b
}

// recordConsole copies the page's console into b.console until the
// browser's context ends, and returns a channel closed once the copier has
// stopped. chromedp.Console subscribes when it is called rather than when
// the iterator is ranged over, so nothing logged after this returns is lost.
func (b *Browser) recordConsole() <-chan struct{} {
	messages := chromedp.Console(b.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for m, err := range messages {
			if err != nil {
				return
			}
			line := m.Time.UTC().Format("15:04:05.000") + " " + m.String()
			if m.URL != "" {
				line += " (" + m.URL + ")"
			}
			b.console.WriteLine(line)
		}
	}()
	return done
}

// Navigate opens url and waits for selector to be visible.
func (b *Browser) Navigate(t *testing.T, url, waitVisibleSelector string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Do(ctx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(chromedp.CSS(waitVisibleSelector)),
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
	if err := chromedp.Do(ctx, chromedp.WaitVisible(chromedp.CSS(selector))); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: waiting for %s failed: %v", selector, err)
	}
}

// Text returns the visible text of the first node matching selector. Useful
// where the assertion is about a value the page rendered rather than about
// an element existing -- "the contact address is the configured one", not
// "there is a contact address".
func (b *Browser) Text(t *testing.T, selector string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()

	text, err := chromedp.Run(ctx, chromedp.Text(chromedp.CSS(selector), chromedp.NodeVisible))
	if err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: reading the text of %s failed: %v", selector, err)
	}
	return text
}

// Attribute returns one attribute of the first node matching selector, and
// whether it was present.
func (b *Browser) Attribute(t *testing.T, selector, name string) (string, bool) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()

	attr, err := chromedp.Run(ctx, chromedp.AttributeValue(chromedp.CSS(selector), name, chromedp.NodeVisible))
	if err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: reading %s of %s failed: %v", name, selector, err)
	}
	return attr.Value, attr.Exists
}

// SetViewport resizes the page's layout viewport. Headless Chrome opens at
// 800x600, which is below the `lg` breakpoint where the nav rail stops being
// a drawer, so a test that clicks the rail sets a desktop size first.
func (b *Browser) SetViewport(t *testing.T, width, height int64) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Do(ctx, chromedp.EmulateViewport(width, height)); err != nil {
		t.Fatalf("harness: setting the viewport to %dx%d failed: %v", width, height, err)
	}
}

// urlPollInterval is how often WaitURL samples the address.
const urlPollInterval = 50 * time.Millisecond

// WaitURL blocks until the page's path and query string (location.pathname
// plus location.search) equal want. A client-side navigation or an address
// bar update leaves no element of its own to wait for; this is how a test
// sees one happen.
func (b *Browser) WaitURL(t *testing.T, want string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Do(ctx, chromedp.Poll[chromedp.Void](
		fmt.Sprintf("location.pathname + location.search === %q", want),
		chromedp.WithPollingInterval(urlPollInterval),
		chromedp.WithPollingTimeout(browserWaitFor),
	)); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: waiting for the address %s failed: %v", want, err)
	}
}

// SelectValue picks value in the native <select> matching selector and
// fires the change event a framework binding listens for. Setting .value
// alone changes what the control shows but tells no listener.
func (b *Browser) SelectValue(t *testing.T, selector, value string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Do(ctx,
		chromedp.WaitVisible(chromedp.CSS(selector)),
		chromedp.Evaluate[chromedp.Void](fmt.Sprintf(
			`(() => { const el = document.querySelector(%q); el.value = %q; el.dispatchEvent(new Event('change', { bubbles: true })); })()`,
			selector, value,
		)),
	); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: selecting %q in %s failed: %v", value, selector, err)
	}
}

// Ctx exposes the browser context so a diagnostic can drive chromedp
// directly. Tests should prefer the helpers above.
func (b *Browser) Ctx() context.Context { return b.ctx }

// AssertNotPresent fails the test if selector matches any node.
func (b *Browser) AssertNotPresent(t *testing.T, selector string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	nodeCount, err := chromedp.Run(ctx, chromedp.EvaluateAsDevTools[int](
		fmt.Sprintf("document.querySelectorAll(%q).length", selector),
	))
	if err != nil {
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
	if err := chromedp.Do(ctx,
		chromedp.WaitVisible(chromedp.CSS(selector)),
		chromedp.Click(chromedp.CSS(selector)),
	); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: clicking %s failed: %v", selector, err)
	}
}

// ClickTestIDWithText clicks the visible element whose data-testid is testID
// and whose text, with surrounding whitespace trimmed, is exactly text. CSS
// cannot match on text, so this is an XPath query. It is for lists whose
// order a test does not control, where "the first match" is whichever row
// happened to sort first.
func (b *Browser) ClickTestIDWithText(t *testing.T, testID, text string) {
	t.Helper()

	sel := chromedp.Search(fmt.Sprintf("//*[@data-testid=%s][normalize-space(.)=%s]",
		xpathLiteral(t, testID), xpathLiteral(t, text)))

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Do(ctx,
		chromedp.WaitVisible(sel),
		chromedp.Click(sel),
	); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: clicking [data-testid=%q] with text %q failed: %v", testID, text, err)
	}
}

// xpathLiteral quotes s as an XPath 1.0 string literal. The language has no
// escape sequences, so s is wrapped in whichever quote it does not contain.
func xpathLiteral(t *testing.T, s string) string {
	t.Helper()

	switch {
	case !strings.Contains(s, `"`):
		return `"` + s + `"`
	case !strings.Contains(s, "'"):
		return "'" + s + "'"
	}
	t.Fatalf("harness: %q holds both quote characters, which an XPath 1.0 literal cannot", s)
	return ""
}

// Type waits for the element matching selector and types text into it.
// SendKeys dispatches real key events, which is what a framework-bound
// field needs to notice the change — setting .value directly would not.
func (b *Browser) Type(t *testing.T, selector, text string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Do(ctx,
		chromedp.WaitVisible(chromedp.CSS(selector)),
		chromedp.SendKeys(chromedp.CSS(selector), text),
	); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: typing into %s failed: %v", selector, err)
	}
}

// ClickIfPresent clicks selector if the page has it right now, and reports
// whether it did. Unlike Click it neither waits for the element nor fails
// when it is missing — for a deliberate double-click, where the first click
// is entitled to have replaced the button before the second one lands.
func (b *Browser) ClickIfPresent(t *testing.T, selector string) bool {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	clicked, err := chromedp.Run(ctx, chromedp.EvaluateAsDevTools[bool](
		fmt.Sprintf("(() => { const el = document.querySelector(%q); if (!el) { return false; } el.click(); return true; })()", selector),
	))
	if err != nil {
		t.Fatalf("harness: clicking %s if present failed: %v", selector, err)
	}
	return clicked
}

// loginSettleInterval is how often waitForLoginRedirects samples the page
// while the post-login chain is in flight.
const loginSettleInterval = 50 * time.Millisecond

// waitForLoginRedirects blocks until the post-login redirect chain (IdP ->
// /auth/callback -> application) has stopped moving.
//
// chromedp's Click returns as soon as the form is submitted, not when the
// navigation it triggers has finished. A test that logs in and then
// navigates somewhere else — the natural shape for "log in, then go to
// /account" — races the in-flight chain, and Chrome aborts the newer
// navigation with net::ERR_ABORTED naming the destination rather than the
// race. Tests that follow a login with WaitVisible on the post-login page
// hide this by accident; this makes the helper mean what its name says.
//
// Settled means the document has finished loading, the IdP's login form is
// gone, and the location has held still across consecutive samples. A
// transient error means an execution context was torn down mid-navigation,
// which is evidence the chain is still moving: reset and keep waiting. The
// caller's context bounds the whole thing.
func waitForLoginRedirects(ctx context.Context) error {
	const stableSamples = 2

	var last string
	stable := 0
	for {
		current, err := chromedp.Run(ctx, chromedp.Location())
		var done bool
		if err == nil {
			done, err = chromedp.Run(ctx, chromedp.Evaluate[bool](`document.readyState === 'complete' && document.querySelector('[data-testid="idp-login-form"]') === null`))
		}
		switch {
		case err != nil:
			stable = 0
		case done && current == last:
			stable++
			if stable >= stableSamples {
				return nil
			}
		default:
			stable = 0
		}
		last = current

		select {
		case <-ctx.Done():
			return fmt.Errorf("redirect chain still in flight at %s: %w", last, ctx.Err())
		case <-time.After(loginSettleInterval):
		}
	}
}

// CompleteIdPLogin fills and submits the harness IdP's real login form
// (data-testid="idp-username"/"idp-submit" — see idp.go's renderLoginForm)
// with username, then follows the resulting redirect chain back into the
// server. Call after navigating to a URL that reaches /auth/login.
func (b *Browser) CompleteIdPLogin(t *testing.T, username string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Do(ctx,
		chromedp.WaitVisible(chromedp.CSS(`[data-testid="idp-login-form"]`)),
		chromedp.SendKeys(chromedp.CSS(`[data-testid="idp-username"]`), username),
		chromedp.Click(chromedp.CSS(`[data-testid="idp-submit"]`)),
	); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: completing the IdP login form failed: %v", err)
	}

	if err := waitForLoginRedirects(ctx); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: the post-login redirect chain did not settle: %v", err)
	}
}

// CompleteIdPLoginWithExtraClaims is like CompleteIdPLogin but also sets extra
// claims by injecting them into a hidden form field before submission.
// extraClaims is a map of claim names to values; it will be JSON-encoded and
// sent to the IdP's extra_claims form field.
func (b *Browser) CompleteIdPLoginWithExtraClaims(t *testing.T, username string, extraClaims map[string]any) {
	t.Helper()

	// Marshal extraClaims to JSON and escape for use in JavaScript.
	extraJSON, err := json.Marshal(extraClaims)
	if err != nil {
		t.Fatalf("harness: failed to marshal extra claims: %v", err)
	}

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()
	if err := chromedp.Do(ctx,
		chromedp.WaitVisible(chromedp.CSS(`[data-testid="idp-login-form"]`)),
		chromedp.SendKeys(chromedp.CSS(`[data-testid="idp-username"]`), username),
		// Inject a hidden extra_claims field into the form before submission.
		chromedp.Evaluate[chromedp.Void](fmt.Sprintf(`
			var input = document.createElement('input');
			input.type = 'hidden';
			input.name = 'extra_claims';
			input.value = '%s';
			document.querySelector('[data-testid="idp-login-form"]').appendChild(input);
		`, string(extraJSON))),
		chromedp.Click(chromedp.CSS(`[data-testid="idp-submit"]`)),
	); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: completing the IdP login form with extra claims failed: %v", err)
	}

	if err := waitForLoginRedirects(ctx); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: the post-login redirect chain did not settle: %v", err)
	}
}

// CompleteIdPLoginWithGroups fills and submits the harness IdP's real login form
// with username and group claims, then follows the resulting redirect chain back
// into the server. Call after navigating to a URL that reaches /auth/login.
//
// groups are the group memberships to include in the ID token claims. They are
// submitted via the groups form field (see idp.go's renderLoginForm and
// handleAuthorize). The current test suite only uses single-group scenarios, so
// this implementation sends the first group only; expanding to multiple groups
// would require modifying the form to have multiple repeatable inputs.
func (b *Browser) CompleteIdPLoginWithGroups(t *testing.T, username string, groups []string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(b.ctx, browserWaitFor)
	defer cancel()

	// Build the actions: fill username and groups, then submit
	actions := []chromedp.Action[chromedp.Void]{
		chromedp.WaitVisible(chromedp.CSS(`[data-testid="idp-login-form"]`)),
		chromedp.SendKeys(chromedp.CSS(`[data-testid="idp-username"]`), username),
	}

	// Send groups: if any are provided, fill the groups field with the first one.
	// HTML forms with a single input can only send one value, so we send the first
	// group from the list. The IdP handler (idp.go:143-149) reads r.PostForm["groups"]
	// which returns a []string, so it's designed for multiple groups, but the
	// form currently only supports one input field. If multiple groups are needed,
	// the form would need to create additional input fields.
	if len(groups) > 0 {
		actions = append(actions, chromedp.SendKeys(chromedp.CSS(`[data-testid="idp-groups"]`), groups[0]))
	}

	// Click submit
	actions = append(actions, chromedp.Click(chromedp.CSS(`[data-testid="idp-submit"]`)))

	if err := chromedp.Do(ctx, actions...); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: completing the IdP login form with groups failed: %v", err)
	}

	if err := waitForLoginRedirects(ctx); err != nil {
		b.screenshotOnFailure(t)
		t.Fatalf("harness: the post-login redirect chain did not settle: %v", err)
	}
}

// visibleTestIDsJS lists the data-testid of every element the page is
// showing, once each, in document order. It names the state a page is in
// (a loading block, a load failure, the login view) in a line a CI log can
// carry.
const visibleTestIDsJS = `[...new Set([...document.querySelectorAll('[data-testid]')]
	.filter((el) => el.checkVisibility())
	.map((el) => el.dataset.testid))]`

// failureConsoleTailLines is how much of the browser console a failure
// quotes in the test log. The whole console is in browser-console.log.
const failureConsoleTailLines = 20

// screenshotOnFailure best-effort records what the browser was showing when
// a step failed: a screenshot, the URL, the page's HTML and its console go
// to the test's artifact directory, and the URL, the visible test IDs and
// the end of the console go to the test log, so a CI failure can be read
// without downloading anything. Debugging a browser failure from an
// assertion message alone isn't realistic.
//
// Each capture stands alone: a page that no longer answers one query may
// still answer the next, and the console needs no round trip at all.
//
// Derived from the browser's own context: a chromedp action needs a chromedp
// context, and the context.Background() this used before made every capture
// fail with "invalid context".
func (b *Browser) screenshotOnFailure(t *testing.T) {
	t.Helper()

	console := b.console.Bytes()
	writeArtifact(t, "browser-console.log", console)

	ctx, cancel := context.WithTimeout(b.ctx, 5*time.Second)
	defer cancel()

	currentURL, err := chromedp.Run(ctx, chromedp.Location())
	if err != nil {
		currentURL = fmt.Sprintf("(unknown: %v)", err)
	}
	writeArtifact(t, "browser-failure-url.txt", []byte(currentURL))

	testIDs, err := chromedp.Run(ctx, chromedp.Evaluate[[]string](visibleTestIDsJS))
	if err != nil {
		testIDs = []string{fmt.Sprintf("(unknown: %v)", err)}
	}

	if html, err := chromedp.Run(ctx, chromedp.Evaluate[string](`document.documentElement.outerHTML`)); err != nil {
		t.Logf("harness: failed to capture the failure page's HTML: %v", err)
	} else {
		writeArtifact(t, "browser-failure.html", []byte(html))
	}

	if png, err := chromedp.Run(ctx, chromedp.CaptureScreenshot()); err != nil {
		t.Logf("harness: failed to capture failure screenshot: %v", err)
	} else {
		writeArtifact(t, "browser-failure.png", png)
	}

	t.Logf("harness: the browser was at %s showing test IDs %v; last browser console lines:\n%s",
		currentURL, testIDs, tailLines(string(console), failureConsoleTailLines))
}

// tailLines returns the last n lines of s, without a trailing newline, or
// "(empty)" when s has none.
func tailLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return "(empty)"
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
