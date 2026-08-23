//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// Tier 1 ("wire"): the harness plus the real ssoossh and ssoosshd binaries,
// approval driven over HTTP with a cookie jar walking the OIDC redirects —
// no browser. See docs/dev/e2e-testing-plan.md's assertion table.

func TestLogin_PrintsApprovalURLBeforeCompletion(t *testing.T) {
	f := newFixture(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	if !strings.HasPrefix(approvalURL, f.Server.BaseURL+"/approve/") {
		t.Fatalf("got approval URL %q, want a %s/approve/<id> URL", approvalURL, f.Server.BaseURL)
	}

	// The process must still be running: the URL is printed before the
	// SSE wait begins, not after.
	select {
	case <-login.Done():
		t.Fatal("ssh login already exited by the time its approval URL was observed")
	default:
	}

	requestID := requestIDFromApprovalURL(t, approvalURL)
	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
	}
}

func TestLogin_ApprovingDeliversCertificateOverSSE(t *testing.T) {
	f := newFixture(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)
	requestID := requestIDFromApprovalURL(t, approvalURL)

	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
	}

	certs := f.Agent.Certificates(t)
	if len(certs) != 1 {
		t.Fatalf("got %d certificates loaded in the agent, want 1", len(certs))
	}
	if got, want := certs[0].ValidPrincipals, []string{"alice"}; len(got) != 1 || got[0] != want[0] {
		t.Errorf("got principals %v, want %v", got, want)
	}

	caKey, err := harness.ParseAuthorizedKey(f.Server.CAPublicKey)
	if err != nil {
		t.Fatalf("harness: failed to parse the test CA public key: %v", err)
	}
	if !harness.SameSSHKey(certs[0].SignatureKey, caKey) {
		t.Error("issued certificate is not signed by the configured test CA")
	}
}

func TestLogin_CertificateCarriesOnlyPermittedExtensionsAndNoCriticalOptions(t *testing.T) {
	f := newFixture(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
	}

	certs := f.Agent.Certificates(t)
	if len(certs) != 1 {
		t.Fatalf("got %d certificates loaded in the agent, want 1", len(certs))
	}
	cert := certs[0]

	// The client asks for a wider extension set (loginExtensions in
	// ssh_login.go) than the harness server config permits — only these two
	// should survive the server-side narrowing.
	wantExtensions := map[string]string{"permit-pty": "", "permit-agent-forwarding": ""}
	if len(cert.Extensions) != len(wantExtensions) {
		t.Errorf("got extensions %v, want exactly %v", cert.Extensions, wantExtensions)
	}
	for k := range wantExtensions {
		if _, ok := cert.Extensions[k]; !ok {
			t.Errorf("missing expected extension %q, got %v", k, cert.Extensions)
		}
	}

	if len(cert.CriticalOptions) != 0 {
		t.Errorf("got critical options %v, want none — ForceCommand/SourceAddresses are always dropped", cert.CriticalOptions)
	}
}

// TestLogin_KeyIDTemplateRendersExtraClaimFields proves the extra-claims
// pipeline against real binaries: authentication.fields.extra maps ID token
// claims at the approver's login, the values persist on the users row, and
// the configured key ID template renders them — including join for a
// list-valued claim and MISSING for a claim the token never carried — in
// the certificate the client actually receives.
func TestLogin_KeyIDTemplateRendersExtraClaimFields(t *testing.T) {
	idp := harness.NewIdentityProvider(t)
	srv := harness.StartServer(t, idp, harness.ServerOptions{
		UserKeyIDTemplate: `{{.Username}}|{{.Extra.dept}}|{{join .Extra.accounts ";"}}|{{.Extra.absent}}`,
		ExtraClaimFields: map[string]string{
			"dept":     "department",
			"accounts": "altAccounts",
			"absent":   "notInToken",
		},
	})
	agent := harness.StartAgent(t)
	_, ssoosshBin := harness.Binaries(t)

	login := harness.StartLogin(t, ssoosshBin, srv.BaseURL, agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	if err := harness.AuthenticateWithExtraClaims(client, srv.BaseURL, "/approve/"+requestID, "alice", nil, map[string]any{
		"department":  "eng",
		"altAccounts": []string{"a-alice", "b-alice"},
	}); err != nil {
		t.Fatalf("harness: %v", err)
	}
	if err := harness.Approve(client, srv.BaseURL, requestID); err != nil {
		t.Fatalf("harness: %v", err)
	}

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
	}

	certs := agent.Certificates(t)
	if len(certs) != 1 {
		t.Fatalf("got %d certificates loaded in the agent, want 1", len(certs))
	}
	want := "alice|eng|a-alice;b-alice|MISSING"
	if certs[0].KeyId != want {
		t.Errorf("got key ID %q, want %q", certs[0].KeyId, want)
	}
}

func TestLogin_DenyingResolvesWithNoCertificate(t *testing.T) {
	f := newFixture(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	deny(t, client, f.Server.BaseURL, requestID, "alice")

	if err := login.Wait(t, waitFor); err == nil {
		t.Fatal("expected ssh login to fail after the request was denied, got a clean exit")
	}
	if !strings.Contains(login.Stderr(), "denied") {
		t.Errorf("expected stderr to mention the denial, got:\n%s", login.Stderr())
	}

	if certs := f.Agent.Certificates(t); len(certs) != 0 {
		t.Errorf("got %d certificates loaded after a denied request, want 0", len(certs))
	}
}

func TestLogin_SecondLoginReusesValidCertificateWithoutNewApproval(t *testing.T) {
	f := newFixture(t)

	first := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, first.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

	if err := first.Wait(t, waitFor); err != nil {
		t.Fatalf("first ssh login failed: %v\nstderr:\n%s", err, first.Stderr())
	}

	second := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	if err := second.Wait(t, waitFor); err != nil {
		t.Fatalf("second ssh login failed: %v\nstderr:\n%s", err, second.Stderr())
	}

	if !strings.Contains(second.Stderr(), "Already have a valid certificate") {
		t.Errorf("expected the second login to reuse the existing certificate without a new approval, got stderr:\n%s", second.Stderr())
	}

	if certs := f.Agent.Certificates(t); len(certs) != 1 {
		t.Errorf("got %d certificates loaded after a reused login, want 1", len(certs))
	}
}

func TestLogout_RemovesOnlySsoosshCertificateLeavingUnrelatedKeyUntouched(t *testing.T) {
	f := newFixture(t)

	unrelated := f.Agent.AddUnrelatedKey(t, "unrelated-test-key")

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	requestID := requestIDFromApprovalURL(t, login.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
	}
	if certs := f.Agent.Certificates(t); len(certs) != 1 {
		t.Fatalf("got %d certificates loaded before logout, want 1", len(certs))
	}

	if output, err := harness.RunLogout(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket); err != nil {
		t.Fatalf("ssh logout failed: %v\noutput:\n%s", err, output)
	}

	if certs := f.Agent.Certificates(t); len(certs) != 0 {
		t.Errorf("got %d certificates loaded after logout, want 0", len(certs))
	}

	stillPresent := false
	for _, k := range f.Agent.AllKeys(t) {
		if harness.SameSSHKey(k, unrelated) {
			stillPresent = true
		}
	}
	if !stillPresent {
		t.Error("logout removed the unrelated key, which it does not own")
	}
}
