//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// Tier 1 ("wire"): denying a request honours its claim. The first
// authenticated action on a request (opening the approval page, approving
// or denying) claims it, and every later action must come from the same
// user, so a second user who learns the request ID cannot cancel the
// owner's login.

// TestDeny_ShouldRefuseAnotherUserOnceTheOwnerHasClaimed has alice open
// the approval page (claiming the request), mallory post deny for it and
// get 403, and then alice approve it: the waiting client must still get
// its certificate, proving mallory's denial changed nothing.
func TestDeny_ShouldRefuseAnotherUserOnceTheOwnerHasClaimed(t *testing.T) {
	f := newFixture(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))
	requestURL := f.Server.BaseURL + "/api/certs/requests/" + requestID

	// The approval page's data source is the claiming call, so this is
	// what "alice opens the page" means on the wire.
	owner := newBrowserClient(t)
	authenticate(t, owner, f.Server.BaseURL, "/approve/"+requestID, "alice", nil)
	if got := statusOf(t, owner, http.MethodGet, requestURL); got != http.StatusOK {
		t.Fatalf("owner opening the approval page: got status %d, want %d", got, http.StatusOK)
	}

	intruder := newBrowserClient(t)
	authenticate(t, intruder, f.Server.BaseURL, "/approve/"+requestID, "mallory", nil)
	if got := statusOf(t, intruder, http.MethodPost, requestURL+"/deny"); got != http.StatusForbidden {
		t.Fatalf("another user denying a claimed request: got status %d, want %d", got, http.StatusForbidden)
	}

	// The refused denial must not have resolved the request: the client
	// is still waiting on its SSE stream.
	select {
	case <-login.Done():
		t.Fatalf("ssh login exited after a refused denial; stderr:\n%s", login.Stderr())
	default:
	}

	if err := harness.Approve(owner, f.Server.BaseURL, requestID); err != nil {
		t.Fatalf("owner approving after the refused denial: %v", err)
	}
	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after the owner approved: %v\nstderr:\n%s", err, login.Stderr())
	}
	certs := f.Agent.Certificates(t)
	if len(certs) != 1 {
		t.Fatalf("got %d certificates loaded in the agent, want 1", len(certs))
	}
	if got := certs[0].ValidPrincipals; len(got) != 1 || got[0] != "alice" {
		t.Errorf("got principals %v, want [alice]", got)
	}
}

// statusOf sends an empty-bodied method request to url as whoever client
// is authenticated as and returns only the response status, for asserting
// a refusal without the harness's success-only decision helpers.
func statusOf(t *testing.T, client *http.Client, method, url string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, http.NoBody)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, url, err)
	}
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}
