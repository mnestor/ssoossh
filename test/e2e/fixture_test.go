//go:build e2e

// Package e2e is the merge-gate suite: real ssoosshd and ssoossh binaries,
// a real (harness-provided) OIDC identity provider, a real ssh-agent, and
// (tier 3) a real sshd. See docs/dev/e2e-testing-plan.md for the design.
package e2e

import (
	"net/http"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// waitFor bounds every blocking wait in this suite — long enough for a real
// process to start and a real (loopback) HTTP round trip, short enough that
// a genuine hang fails the test instead of the CI job's own timeout.
const waitFor = 15 * time.Second

// fixture is the common apparatus tier 1 and tier 3 tests build on: a
// running IdP and ssoosshd, a private agent, and the built client binary.
type fixture struct {
	IdP        *harness.IdentityProvider
	Server     *harness.Server
	Agent      *harness.Agent
	SsoosshBin string
}

// newFixture starts an IdP and a ssoosshd instance pointed at it, and a
// private ssh-agent, all torn down via t.Cleanup.
func newFixture(t *testing.T) *fixture {
	t.Helper()

	idp := harness.NewIdentityProvider(t)
	srv := harness.StartServer(t, idp, harness.ServerOptions{})
	agent := harness.StartAgent(t)
	_, ssoosshBin := harness.Binaries(t)

	return &fixture{IdP: idp, Server: srv, Agent: agent, SsoosshBin: ssoosshBin}
}

// newBrowserClient returns an http.Client with a cookie jar, standing in
// for tier 1's "browser": it walks OIDC redirects and posts to the
// approval endpoints, but never renders HTML or runs JavaScript.
func newBrowserClient(t *testing.T) *http.Client {
	t.Helper()

	client, err := harness.NewCookieClient()
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	return client
}

// requestIDFromApprovalURL extracts the certificate request's UUID from an
// approval URL of the form "http://host/approve/<id>".
func requestIDFromApprovalURL(t *testing.T, approvalURL string) string {
	t.Helper()

	id, err := harness.RequestIDFromApprovalURL(approvalURL)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	return id
}

// authenticate drives client through the full OIDC login against the
// harness IdP, leaving an authenticated session in its cookie jar.
func authenticate(t *testing.T, client *http.Client, serverBaseURL, returnTo, username string, groups []string) {
	t.Helper()

	if err := harness.Authenticate(client, serverBaseURL, returnTo, username, groups); err != nil {
		t.Fatalf("harness: %v", err)
	}
}

// approve authenticates client as username (with groups) and POSTs approve
// for requestID, asserting the call itself succeeds. The certificate (or
// denial) still arrives on the waiting client's own SSE connection
// separately.
func approve(t *testing.T, client *http.Client, serverBaseURL, requestID, username string, groups []string) {
	t.Helper()

	authenticate(t, client, serverBaseURL, "/approve/"+requestID, username, groups)
	if err := harness.Approve(client, serverBaseURL, requestID); err != nil {
		t.Fatalf("harness: %v", err)
	}
}

// deny authenticates client as username and POSTs deny for requestID.
func deny(t *testing.T, client *http.Client, serverBaseURL, requestID, username string) {
	t.Helper()

	authenticate(t, client, serverBaseURL, "/approve/"+requestID, username, nil)
	if err := harness.Deny(client, serverBaseURL, requestID); err != nil {
		t.Fatalf("harness: %v", err)
	}
}
