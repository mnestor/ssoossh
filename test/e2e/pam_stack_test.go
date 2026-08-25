//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// pamApproverGroup is the OIDC group cert_options.pam.require_group demands
// of an approver. PAM issuance fails closed: leaving require_group unset
// means no PAM certificate is ever issued (see CertOptionsPAM.RequireGroup),
// so the fixture must configure it explicitly.
const pamApproverGroup = "pam-approvers"

// pamApprover is the browser identity that approves or denies. Deliberately
// not "games" (the local account pamtest.c authenticates): the certificate's
// principal comes from the request's local username, not the approver's
// identity (see service.Approve), and using distinct names proves it.
const pamApprover = "pam-operator"

// newPAMStackFixture starts an IdP and a ssoosshd with PAM issuance enabled,
// builds pam_ssoossh.so and the pamtest driver, and installs a dedicated
// /etc/pam.d stack wiring the module to this fixture's server and CA.
// Returns the server (for approval calls), the pamtest binary, and the
// service name actually installed -- InstallPAMService adds a per-run
// suffix so concurrent e2e runs on one host cannot share a service file,
// so the returned name is the one to hand pamtest, never the base.
func newPAMStackFixture(t *testing.T, serviceName string) (*harness.Server, string, string) {
	t.Helper()

	idp := harness.NewIdentityProvider(t)
	srv := harness.StartServer(t, idp, harness.ServerOptions{PAMRequireGroup: pamApproverGroup})
	modulePath, pamtestPath := harness.PAMArtifacts(t)
	service := harness.InstallPAMService(t, serviceName, modulePath, srv)

	return srv, pamtestPath, service
}

// TestPAMStack_ShouldAuthenticateWhenApproved drives a real PAM transaction:
// pamtest calls pam_authenticate against a real /etc/pam.d stack loading
// pam_ssoossh.so, the module requests a certificate from a real ssoosshd,
// a browser identity in the required group approves, and the module's four
// checks pass against the issued certificate.
func TestPAMStack_ShouldAuthenticateWhenApproved(t *testing.T) {
	srv, pamtestBin, service := newPAMStackFixture(t, "ssoossh-e2e-pam-approve")

	pt := harness.StartPamtest(t, pamtestBin, service)
	requestID := requestIDFromApprovalURL(t, pt.ApprovalURL(t))
	approve(t, newBrowserClient(t), srv.BaseURL, requestID, pamApprover, []string{pamApproverGroup})

	exitCode, output := pt.Wait(t)
	if exitCode != 0 {
		t.Errorf("pamtest exited %d, want 0\noutput:\n%s", exitCode, output)
	}
	if !strings.Contains(output, "auth=Success") {
		t.Errorf("pam_authenticate did not report Success\noutput:\n%s", output)
	}
}

// TestPAMStack_ShouldRefuseWhenDenied is the same transaction with the
// browser identity denying: pam_authenticate must come back PAM_AUTH_ERR
// ("Authentication failure" from Linux-PAM's pam_strerror) and pamtest must
// exit non-zero.
func TestPAMStack_ShouldRefuseWhenDenied(t *testing.T) {
	srv, pamtestBin, service := newPAMStackFixture(t, "ssoossh-e2e-pam-deny")

	pt := harness.StartPamtest(t, pamtestBin, service)
	requestID := requestIDFromApprovalURL(t, pt.ApprovalURL(t))
	deny(t, newBrowserClient(t), srv.BaseURL, requestID, pamApprover)

	exitCode, output := pt.Wait(t)
	if exitCode == 0 {
		t.Errorf("pamtest exited 0 after denial, want non-zero\noutput:\n%s", output)
	}
	if !strings.Contains(output, "auth=Authentication failure") {
		t.Errorf("pam_authenticate did not report Authentication failure\noutput:\n%s", output)
	}
}
