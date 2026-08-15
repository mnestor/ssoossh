//go:build e2e

// Package e2e is the merge-gate suite: real ssoosshd and ssoossh binaries,
// a real (harness-provided) OIDC identity provider, a real ssh-agent, and
// (tier 3) a real sshd. See docs/e2e-testing-plan.md for the design.
package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mnestor/ssoossh/internal/apitypes"
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

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("harness: failed to create cookie jar: %v", err)
	}
	return &http.Client{Jar: jar}
}

// requestIDFromApprovalURL extracts the certificate request's UUID from an
// approval URL of the form "http://host/approve/<id>".
func requestIDFromApprovalURL(t *testing.T, approvalURL string) string {
	t.Helper()

	u, err := url.Parse(approvalURL)
	if err != nil {
		t.Fatalf("harness: failed to parse approval URL %q: %v", approvalURL, err)
	}
	id := strings.TrimPrefix(u.Path, "/approve/")
	if id == "" || id == u.Path {
		t.Fatalf("harness: approval URL %q does not look like /approve/<id>", approvalURL)
	}
	return id
}

// authenticate drives client through the full OIDC login against the
// harness IdP: GET .../auth/login?return_to=..., submit the IdP's real
// login form as username (with groups, if any), and follow the resulting
// redirect chain back into the server. client's cookie jar holds an
// authenticated session afterward.
func authenticate(t *testing.T, client *http.Client, serverBaseURL, returnTo, username string, groups []string) {
	t.Helper()

	loginURL := serverBaseURL + "/auth/login?return_to=" + url.QueryEscape(returnTo)
	resp, err := client.Get(loginURL)
	if err != nil {
		t.Fatalf("harness: failed to reach the OIDC login redirect: %v", err)
	}
	resp.Body.Close()

	idpAuthorizeURL := resp.Request.URL.String()

	form := url.Values{"username": {username}}
	for _, g := range groups {
		form.Add("groups", g)
	}

	resp2, err := client.PostForm(idpAuthorizeURL, form)
	if err != nil {
		t.Fatalf("harness: failed to submit the IdP login form: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("harness: expected 200 after completing OIDC login, got %d (final URL %s)",
			resp2.StatusCode, resp2.Request.URL)
	}
}

// approve authenticates client as username (with groups) and POSTs approve
// for requestID, asserting the call itself succeeds. The certificate (or
// denial) still arrives on the waiting client's own SSE connection
// separately.
func approve(t *testing.T, client *http.Client, serverBaseURL, requestID, username string, groups []string) {
	t.Helper()

	authenticate(t, client, serverBaseURL, "/approve/"+requestID, username, groups)

	resp, err := client.Post(serverBaseURL+"/api/certs/requests/"+requestID+"/approve", "application/json", nil)
	if err != nil {
		t.Fatalf("harness: failed to POST approve: %v", err)
	}
	defer resp.Body.Close()

	var envelope apitypes.Envelope[apitypes.ApproveResponse]
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("harness: failed to decode approve response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("harness: approve failed: status %d, error %q", resp.StatusCode, envelope.Error)
	}
}

// deny authenticates client as username and POSTs deny for requestID.
func deny(t *testing.T, client *http.Client, serverBaseURL, requestID, username string) {
	t.Helper()

	authenticate(t, client, serverBaseURL, "/approve/"+requestID, username, nil)

	resp, err := client.Post(serverBaseURL+"/api/certs/requests/"+requestID+"/deny", "application/json", nil)
	if err != nil {
		t.Fatalf("harness: failed to POST deny: %v", err)
	}
	defer resp.Body.Close()

	var envelope apitypes.Envelope[apitypes.DenyResponse]
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("harness: failed to decode deny response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("harness: deny failed: status %d, error %q", resp.StatusCode, envelope.Error)
	}
}
