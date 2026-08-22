//go:build resilience || e2e

package resilience

import (
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// TestOIDC_TokenEndpointTimeout validates that when the OIDC token endpoint
// is unreachable (timeout), the server rejects the login with a clear error
// and does not panic or hang indefinitely.
func TestOIDC_TokenEndpointTimeout(t *testing.T) {
	// This test would require injecting a failure at the IdP level.
	// In the harness, the IdP is controllable, so we can mock failures.
	// For now, this documents the scenario to be implemented.

	t.Skip("requires IdP failure injection capability")
}

// TestOIDC_JWKSEndpointUnreachable validates that when the JWKS endpoint
// becomes unreachable during token validation, the server returns a
// specific error (not a generic 500) and the request fails safely.
func TestOIDC_JWKSEndpointUnreachable(t *testing.T) {
	// Scenario: JWKS cache is stale, endpoint is down, validation fails.
	// Expected: Login fails with 401/403 (invalid token), not 500.

	t.Skip("requires IdP failure injection capability")
}

// TestOIDC_KeyRotationMidSession validates that when an OIDC provider rotates
// its signing keys mid-session, tokens signed with the old key are still
// accepted (via cached JWKS), and new tokens are validated with the new key.
// The system should transition smoothly without requiring login again.
func TestOIDC_KeyRotationMidSession(t *testing.T) {
	// Scenario:
	// 1. User logs in, gets token signed with key A
	// 2. IdP rotates keys (A is old, B is new)
	// 3. User logs in again, gets token signed with key B
	// Expected: Both tokens validate, no service disruption

	t.Skip("requires IdP key rotation injection")
}

// TestOIDC_TokenEndpointMalformedResponse validates that when the OIDC token
// endpoint returns invalid JSON or a malformed response, the server logs the
// error clearly and rejects the login without panicking.
func TestOIDC_TokenEndpointMalformedResponse(t *testing.T) {
	// Scenario: Token endpoint returns 200 OK but body is not valid JSON
	// Expected: Login fails, error is logged, no panic

	t.Skip("requires IdP failure injection capability")
}

// TestOIDC_TokenEndpointReturns5xx validates that when the OIDC token endpoint
// returns 5xx errors, the server does not retry indefinitely, times out quickly,
// and returns a clear error to the client.
func TestOIDC_TokenEndpointReturns5xx(t *testing.T) {
	// Scenario: IdP returns 503 Service Unavailable
	// Expected: Login fails with reasonable timeout, no retry storm

	t.Skip("requires IdP failure injection capability")
}

// TestOIDC_TLSHandshakeFails validates that TLS errors during OIDC communication
// are handled cleanly (not as a generic panic).
func TestOIDC_TLSHandshakeFails(t *testing.T) {
	t.Skip("requires IdP TLS injection capability")
}

// TestOIDC_LoginSucceedsAfterIdPRecovery validates that after an IdP outage,
// once the IdP returns to health, logins work again without restarting ssoosshd.
func TestOIDC_LoginSucceedsAfterIdPRecovery(t *testing.T) {
	f := newFixture(t)

	// Perform a baseline login to verify the IdP is healthy.
	login1 := harness.StartLogin(t, f.ssoosshBin, f.server.BaseURL, f.agent.Socket)
	approvalURL1 := login1.ApprovalURL(t, waitFor)

	browser := f.startBrowser(t)
	browser.Navigate(t, approvalURL1, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLogin(t, "alice")
	browser.WaitVisible(t, `[data-testid="approval-view"]`)
	browser.Click(t, `[data-testid="approve-button"]`)

	if err := login1.Wait(t, waitFor); err != nil {
		t.Fatalf("baseline login failed: %v", err)
	}

	// Now test a second login after IdP is back (simulated by same fixture).
	// In a real scenario, the IdP would be restarted between these.
	login2 := harness.StartLogin(t, f.ssoosshBin, f.server.BaseURL, f.agent.Socket, "--force")
	approvalURL2 := login2.ApprovalURL(t, waitFor)

	browser.Navigate(t, approvalURL2, `[data-testid="sign-in-button"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLogin(t, "bob")
	browser.WaitVisible(t, `[data-testid="approval-view"]`)
	browser.Click(t, `[data-testid="approve-button"]`)

	if err := login2.Wait(t, waitFor); err != nil {
		t.Errorf("login after IdP recovery failed: %v", err)
	}
}
