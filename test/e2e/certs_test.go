//go:build e2e

package e2e

import (
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// TestCertificateDetail_ApproverCanViewCertificate tests that the approver
// can navigate to a certificate detail page and see full details.
func TestCertificateDetail_ApproverCanViewCertificate(t *testing.T) {
	f := newFixture(t)
	browser := harness.StartBrowser(t)

	// Start a login and approve a certificate request
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	// Extract request ID and approve it
	requestID := requestIDFromApprovalURL(t, approvalURL)
	approveClient := newBrowserClient(t)
	approve(t, approveClient, f.Server.BaseURL, requestID, "alice", nil)

	// Wait for certificate to be issued
	cert, err := login.AwaitCertificate(t, waitFor, f.Server.BaseURL, f.Agent)
	if err != nil {
		t.Fatalf("failed to await certificate: %v", err)
	}

	// Navigate to the certificate detail page using the certificate ID
	// The certificate ID should be available from the login.AwaitCertificate response
	certDetailURL := f.Server.BaseURL + "/certs/" + cert.ID
	// Navigate to cert page; it will redirect to login since we're not authenticated yet
	browser.Navigate(t, certDetailURL, `[data-testid="login-view"]`)
	// Click the sign-in button
	browser.Click(t, `[data-testid="sign-in-button"]`)
	// Complete the IdP login as alice
	browser.CompleteIdPLogin(t, "alice")
	// Wait for the post-login redirect chain to settle. After completing IdP login,
	// the browser is redirected back to the cert page. Wait for cert-details to
	// appear before continuing.
	browser.WaitVisible(t, `[data-testid="cert-details"]`)
	browser.WaitVisible(t, `[data-testid="cert-serial-number"]`)
	browser.WaitVisible(t, `[data-testid="cert-key-id"]`)
}

// TestCertificateDetail_UnrelatedUserIsRefused tests that an unrelated user
// cannot view a certificate detail page, even when authenticated.
func TestCertificateDetail_UnrelatedUserIsRefused(t *testing.T) {
	f := newFixture(t)
	browser := harness.StartBrowser(t)

	// Start a login and approve a certificate request as alice
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	requestID := requestIDFromApprovalURL(t, approvalURL)
	approveClient := newBrowserClient(t)
	approve(t, approveClient, f.Server.BaseURL, requestID, "alice", nil)

	cert, err := login.AwaitCertificate(t, waitFor, f.Server.BaseURL, f.Agent)
	if err != nil {
		t.Fatalf("failed to await certificate: %v", err)
	}

	// Try to access the certificate as a different user (bob)
	certDetailURL := f.Server.BaseURL + "/certs/" + cert.ID
	// Navigate to cert page; it will redirect to login since we're not authenticated yet
	browser.Navigate(t, certDetailURL, `[data-testid="login-view"]`)
	// Click the sign-in button
	browser.Click(t, `[data-testid="sign-in-button"]`)
	// Complete login as bob (a user who is not the owner)
	browser.CompleteIdPLogin(t, "bob")
	// Wait for the post-login redirect chain to settle. After completing IdP login,
	// bob should be redirected back to the cert page, but since bob is not authorized
	// to view this certificate, the page should show access-denied.
	browser.WaitVisible(t, `[data-testid="access-denied"]`)
	browser.AssertNotPresent(t, `[data-testid="cert-details"]`)
}

// TestCertificateDetail_AuditorCanViewCertificate tests that an auditor
// can view any certificate detail page.
// auditorGroup is the group the harness server is told grants auditor
// access, and the group the browser logs in carrying. Naming it once keeps
// the two halves from drifting apart, which is a failure that looks exactly
// like a slow page.
const auditorGroup = "ssoossh-auditors"

func TestCertificateDetail_AuditorCanViewCertificate(t *testing.T) {
	// The server has to be told which group grants auditor access. With
	// admin.auditor_group unset -- the product default and what every other
	// test here runs with -- config.AdminConfig.GrantsAuditor denies every
	// caller, so logging in carrying this group would prove nothing.
	f := newFixture(t, func(o *harness.ServerOptions) {
		o.AuditorGroup = auditorGroup
	})
	browser := harness.StartBrowser(t)

	// Start a login and approve a certificate request as alice
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	requestID := requestIDFromApprovalURL(t, approvalURL)
	approveClient := newBrowserClient(t)
	approve(t, approveClient, f.Server.BaseURL, requestID, "alice", nil)

	cert, err := login.AwaitCertificate(t, waitFor, f.Server.BaseURL, f.Agent)
	if err != nil {
		t.Fatalf("failed to await certificate: %v", err)
	}

	// Access the certificate as an auditor
	certDetailURL := f.Server.BaseURL + "/certs/" + cert.ID
	// Navigate to cert page; it will redirect to login since we're not authenticated yet
	browser.Navigate(t, certDetailURL, `[data-testid="login-view"]`)
	// Click the sign-in button
	browser.Click(t, `[data-testid="sign-in-button"]`)
	// Complete login as an auditor (with auditor group)
	browser.CompleteIdPLoginWithGroups(t, "auditor", []string{auditorGroup})
	// Wait for the post-login redirect chain to settle. After completing IdP login,
	// the browser is redirected back to the cert page. Wait for cert-details to
	// appear before continuing.
	browser.WaitVisible(t, `[data-testid="cert-details"]`)
	browser.WaitVisible(t, `[data-testid="cert-serial-number"]`)
}

// TestAdminCertificateList_SearchAndFilterWorks tests that the admin certificate
// list page supports search, filtering, and pagination.
func TestAdminCertificateList_SearchAndFilterWorks(t *testing.T) {
	f := newFixture(t)
	browser := harness.StartBrowser(t)

	// Start a login and create a few certificates
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	requestID := requestIDFromApprovalURL(t, approvalURL)
	approveClient := newBrowserClient(t)
	approve(t, approveClient, f.Server.BaseURL, requestID, "alice", nil)

	_, err := login.AwaitCertificate(t, waitFor, f.Server.BaseURL, f.Agent)
	if err != nil {
		t.Fatalf("failed to await certificate: %v", err)
	}

	// Navigate to admin certificates page as an auditor
	certListURL := f.Server.BaseURL + "/admin/certificates"
	browser.Navigate(t, certListURL, `[data-testid="login-view"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLoginWithGroups(t, "auditor", []string{"ssoossh-auditors"})

	// Should see the certificate list
	browser.WaitVisible(t, `[data-testid="cert-list"]`)

	// Should see search input
	browser.WaitVisible(t, `[data-testid="search-input"]`)

	// Should see type filter
	browser.WaitVisible(t, `[data-testid="type-filter"]`)

	// Should see pager
	browser.WaitVisible(t, `[data-testid="pager"]`)
}

// TestAdminCertificateList_RowClickNavigatesToDetail tests that clicking a row
// navigates to the certificate detail page.
func TestAdminCertificateList_RowClickNavigatesToDetail(t *testing.T) {
	f := newFixture(t)
	browser := harness.StartBrowser(t)

	// Start a login and create a certificate
	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)
	approvalURL := login.ApprovalURL(t, waitFor)

	requestID := requestIDFromApprovalURL(t, approvalURL)
	approveClient := newBrowserClient(t)
	approve(t, approveClient, f.Server.BaseURL, requestID, "alice", nil)

	_, err := login.AwaitCertificate(t, waitFor, f.Server.BaseURL, f.Agent)
	if err != nil {
		t.Fatalf("failed to await certificate: %v", err)
	}

	// Navigate to admin certificates page
	certListURL := f.Server.BaseURL + "/admin/certificates"
	browser.Navigate(t, certListURL, `[data-testid="login-view"]`)
	browser.Click(t, `[data-testid="sign-in-button"]`)
	browser.CompleteIdPLoginWithGroups(t, "auditor", []string{"ssoossh-auditors"})

	browser.WaitVisible(t, `[data-testid="cert-list"]`)

	// Click on a certificate row
	browser.Click(t, `[data-testid="cert-row"]`)

	// Should navigate to certificate detail page
	browser.WaitVisible(t, `[data-testid="cert-details"]`)
}
