//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/internal/apitypes"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// Tier 1 ("wire") for console login, plus one tier 2 pass through the real
// code box. There is no client binary to drive here: the module that talks
// to these endpoints is written separately, so the harness plays its part
// directly — which is also the honest shape of the test, since what is
// being checked is the contract that module depends on.
//
// The property everything else rests on is the first test below: a code is
// a lookup key for an authenticated approver, never a capability. If an
// unauthenticated caller could resolve one, 40 bits would be a shortcut to
// the request ID, and the request ID is what the certificate is delivered
// against.

// newConsolePublicKey returns a freshly generated public key in
// authorized_keys form, standing in for the per-attempt ephemeral key the
// console module generates.
func newConsolePublicKey(t *testing.T) string {
	t.Helper()

	pair, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("failed to generate a keypair: %v", err)
	}
	authorized, err := pair.MarshalAuthorizedKey()
	if err != nil {
		t.Fatalf("failed to marshal the public key: %v", err)
	}
	return authorized
}

// startConsoleLogin creates one console request against f, the way a
// console module would, and returns what the server answered with.
func startConsoleLogin(t *testing.T, f *fixture) apitypes.CreateRequestResponse {
	t.Helper()

	created, err := harness.CreateConsoleRequest(f.Server.BaseURL, apitypes.ConsoleRequestBody{
		PublicKey:  newConsolePublicKey(t),
		Username:   "alice",
		Hostname:   "web01",
		PAMService: "login",
		TTY:        "tty1",
	})
	if err != nil {
		t.Fatalf("failed to create a console request: %v", err)
	}
	return created
}

// The create response is the console module's whole contract: a code to
// display, somewhere to send the human, and a deadline to bound its own
// wait by.
func TestConsole_CreateReturnsACodeAndADeadline(t *testing.T) {
	f := newFixture(t)

	created := startConsoleLogin(t, f)

	if created.RequestID == "" {
		t.Error("no request_id returned")
	}
	// Grouped for display: this is what goes on a console screen.
	if !strings.Contains(created.UserCode, "-") || len(created.UserCode) != 9 {
		t.Errorf("user_code %q is not the grouped 8-symbol form", created.UserCode)
	}
	if created.VerificationURL != "/console" {
		t.Errorf("verification_url = %q, want /console", created.VerificationURL)
	}
	if !strings.HasPrefix(created.VerificationURLComplete, "/c/") {
		t.Errorf("verification_url_complete = %q, want a /c/<code> shortcut", created.VerificationURLComplete)
	}
	if created.ExpiresAt.IsZero() {
		t.Error("no expires_at returned; the module has nothing to bound its wait by")
	}
}

// The security property the whole design rests on. A signed-out caller
// must learn nothing: not the request ID, and not whether the code is even
// live.
func TestConsole_CodeCannotBeResolvedWithoutASession(t *testing.T) {
	f := newFixture(t)

	created := startConsoleLogin(t, f)

	anonymous := newBrowserClient(t)
	resolved, status, err := harness.ResolveConsoleCode(anonymous, f.Server.BaseURL, created.UserCode)
	if err == nil {
		t.Fatalf("an unauthenticated caller resolved the code to %q", resolved.RequestID)
	}
	if status != http.StatusUnauthorized {
		t.Errorf("got status %d, want %d", status, http.StatusUnauthorized)
	}
}

func TestConsole_AuthenticatedApproverResolvesTheCode(t *testing.T) {
	f := newFixture(t)

	created := startConsoleLogin(t, f)

	client := newBrowserClient(t)
	authenticate(t, client, f.Server.BaseURL, "/console", "alice", nil)

	resolved, _, err := harness.ResolveConsoleCode(client, f.Server.BaseURL, created.UserCode)
	if err != nil {
		t.Fatalf("failed to resolve the code: %v", err)
	}
	if resolved.RequestID != created.RequestID {
		t.Errorf("resolved to %q, want %q", resolved.RequestID, created.RequestID)
	}
	if resolved.ApprovalURL != "/approve/"+created.RequestID {
		t.Errorf("approval_url = %q, want /approve/%s", resolved.ApprovalURL, created.RequestID)
	}
}

// Normalization is the server's job, so what a human plausibly types has to
// reach the same request as what the screen showed.
func TestConsole_CodeResolvesHoweverItIsTyped(t *testing.T) {
	tests := []struct {
		name    string
		rewrite func(displayed string) string
	}{
		{name: "as displayed", rewrite: func(c string) string { return c }},
		{name: "without the separator", rewrite: func(c string) string { return strings.ReplaceAll(c, "-", "") }},
		{name: "in lower case", rewrite: strings.ToLower},
		{name: "with surrounding space", rewrite: func(c string) string { return "  " + c + " " }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			created := startConsoleLogin(t, f)

			client := newBrowserClient(t)
			authenticate(t, client, f.Server.BaseURL, "/console", "alice", nil)

			resolved, _, err := harness.ResolveConsoleCode(client, f.Server.BaseURL, tt.rewrite(created.UserCode))
			if err != nil {
				t.Fatalf("failed to resolve %q: %v", tt.rewrite(created.UserCode), err)
			}
			if resolved.RequestID != created.RequestID {
				t.Errorf("resolved to %q, want %q", resolved.RequestID, created.RequestID)
			}
		})
	}
}

// One code, one request, one shot. Resolving claims the request, so a
// second session typing the same code is turned away — which is what
// settles a race between two people before either sees any detail.
func TestConsole_SecondSessionCannotResolveAClaimedCode(t *testing.T) {
	f := newFixture(t)

	created := startConsoleLogin(t, f)

	first := newBrowserClient(t)
	authenticate(t, first, f.Server.BaseURL, "/console", "alice", nil)
	if _, _, err := harness.ResolveConsoleCode(first, f.Server.BaseURL, created.UserCode); err != nil {
		t.Fatalf("the first resolve failed: %v", err)
	}

	second := newBrowserClient(t)
	authenticate(t, second, f.Server.BaseURL, "/console", "bob", nil)
	_, status, err := harness.ResolveConsoleCode(second, f.Server.BaseURL, created.UserCode)
	if err == nil {
		t.Fatal("a second identity resolved a code already claimed by the first")
	}
	if status != http.StatusForbidden {
		t.Errorf("got status %d, want %d", status, http.StatusForbidden)
	}
}

// A code nothing carries has to come back as not-found rather than as a
// server error or an empty success.
func TestConsole_UnknownCodeIsNotFound(t *testing.T) {
	f := newFixture(t)

	client := newBrowserClient(t)
	authenticate(t, client, f.Server.BaseURL, "/console", "alice", nil)

	_, status, err := harness.ResolveConsoleCode(client, f.Server.BaseURL, "K7M4-QP2X")
	if err == nil {
		t.Fatal("an unused code resolved")
	}
	if status != http.StatusNotFound {
		t.Errorf("got status %d, want %d", status, http.StatusNotFound)
	}
}

// Resolving is the whole of the approach path: once a code has been
// resolved, approval is the same call every other type makes.
func TestConsole_ResolvedRequestCanBeApproved(t *testing.T) {
	f := newFixture(t)

	created := startConsoleLogin(t, f)

	client := newBrowserClient(t)
	authenticate(t, client, f.Server.BaseURL, "/console", "alice", nil)
	if _, _, err := harness.ResolveConsoleCode(client, f.Server.BaseURL, created.UserCode); err != nil {
		t.Fatalf("failed to resolve the code: %v", err)
	}
	if err := harness.Approve(client, f.Server.BaseURL, created.RequestID); err != nil {
		t.Fatalf("failed to approve the console login: %v", err)
	}
}

// Tier 2: the real code box in a real browser, against the real server.
// The console module prints the code and the URL; this is what the human
// does with them.
func TestConsole_TypingTheCodeReachesTheApprovalPage(t *testing.T) {
	f := newFixture(t)
	browser := harness.StartBrowser(t)

	created := startConsoleLogin(t, f)

	browser.Navigate(t, f.Server.BaseURL+"/console", `[data-testid="login-view"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLogin(t, "alice")

	browser.WaitVisible(t, `[data-testid="console-code-input"]`)
	browser.Type(t, `[data-testid="console-code-input"]`, created.UserCode)
	browser.Click(t, `[data-testid="console-code-submit"]`)

	browser.WaitVisible(t, `[data-testid="approval-view"]`)
	browser.WaitVisible(t, `[data-testid="approve-button"]`)
}

// A wrong code has to say so in the box rather than producing anything a
// person could act on.
func TestConsole_AWrongCodeNeverReachesAnApprovalPage(t *testing.T) {
	f := newFixture(t)
	browser := harness.StartBrowser(t)

	// A live request exists, so this is a wrong code rather than an empty
	// deployment.
	startConsoleLogin(t, f)

	browser.Navigate(t, f.Server.BaseURL+"/console", `[data-testid="login-view"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLogin(t, "alice")

	browser.WaitVisible(t, `[data-testid="console-code-input"]`)
	browser.Type(t, `[data-testid="console-code-input"]`, "K7M4QP2X")
	browser.Click(t, `[data-testid="console-code-submit"]`)

	browser.WaitVisible(t, `[data-testid="console-code-failure-not-found"]`)
	browser.AssertNotPresent(t, `[data-testid="approve-button"]`)
}
