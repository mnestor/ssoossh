//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// Service enrollment is the unattended-jobs feature: `service enroll` gets a
// code, `service retrieve` redeems it for a certificate on disk, and cron
// runs the second one forever. Neither half had any end-to-end coverage, and
// runServiceEnroll sat at 7.4% statement coverage -- the only executed path
// being its `--key is required` guard.
//
// The property that only an end-to-end test can establish is the one the
// design leans on hardest: the code is bound to the public key it was
// enrolled against, `retrieve` never resubmits a key, and the approver's own
// identity never becomes the certificate's principal.

// serviceAccountClaim is the ID token claim the server is told to read
// service accounts from. Arbitrary, but it has to be a name the harness IdP
// does not already use for something else.
const serviceAccountClaim = "svc_accounts"

// enrollmentCodePattern pulls the code out of `service enroll`'s report.
var enrollmentCodePattern = regexp.MustCompile(`enrollment code is:\s*(\S+)`)

// serviceFixture is a server that permits one service account, plus the
// built client binary.
type serviceFixture struct {
	Server  *harness.Server
	Bin     string
	Account string
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()

	idp := harness.NewIdentityProvider(t)
	srv := harness.StartServer(t, idp, harness.ServerOptions{
		ServiceAccountsField: serviceAccountClaim,
	})
	_, bin := harness.Binaries(t)

	return &serviceFixture{Server: srv, Bin: bin, Account: "deploy-bot"}
}

// approveServiceEnrollment authenticates as alice, who holds the service
// account, and approves requestID for it.
func (f *serviceFixture) approveServiceEnrollment(t *testing.T, requestID string) {
	t.Helper()

	client := newBrowserClient(t)
	if err := harness.AuthenticateWithExtraClaims(client, f.Server.BaseURL, "/approve/"+requestID,
		"alice", nil, map[string]any{serviceAccountClaim: []string{f.Account}}); err != nil {
		t.Fatalf("harness: %v", err)
	}
	if err := harness.ApproveService(client, f.Server.BaseURL, requestID, f.Account); err != nil {
		t.Fatalf("harness: %v", err)
	}
}

// enroll runs `service enroll` for keyPath through to a printed code,
// approving it along the way. extraArgs are appended (e.g. "--retrieve").
func (f *serviceFixture) enroll(t *testing.T, keyPath string, extraArgs ...string) (code string, stdout string) {
	t.Helper()

	args := append([]string{"service", "enroll", "--key", keyPath, "--server", f.Server.BaseURL}, extraArgs...)
	proc := harness.StartClient(t, f.Bin, harness.ClientOptions{Args: args})

	requestID := requestIDFromApprovalURL(t, proc.ApprovalURL(t, waitFor))
	f.approveServiceEnrollment(t, requestID)

	if err := proc.Wait(t, waitFor); err != nil {
		t.Fatalf("service enroll failed: %v\nstdout:\n%s\nstderr:\n%s", err, proc.Stdout(), proc.Stderr())
	}

	out := proc.Stdout()
	m := enrollmentCodePattern.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("service enroll printed no enrollment code\nstdout:\n%s", out)
	}
	return m[1], out
}

// readCertificate parses the certificate `service retrieve` wrote beside
// keyPath, following the OpenSSH naming the command's guidance promises.
func readCertificate(t *testing.T, keyPath string) *ssh.Certificate {
	t.Helper()

	data, err := os.ReadFile(keyPath + "-cert.pub")
	if err != nil {
		t.Fatalf("expected a certificate at %s-cert.pub: %v", keyPath, err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		t.Fatalf("certificate at %s-cert.pub does not parse: %v", keyPath, err)
	}
	cert, ok := pub.(*ssh.Certificate)
	if !ok {
		t.Fatalf("%s-cert.pub holds a %s, not a certificate", keyPath, pub.Type())
	}
	return cert
}

func TestService_ShouldEnrollThenRetrieveACertificateBoundToTheServiceAccount(t *testing.T) {
	f := newServiceFixture(t)
	keyPath := filepath.Join(t.TempDir(), "svckey")

	code, out := f.enroll(t, keyPath)

	// enroll generates the keypair when neither half exists, and the three
	// OpenSSH-named files have to end up side by side or ssh will not find
	// the certificate. The guidance the command prints says exactly this,
	// so it is worth checking rather than assuming.
	for _, suffix := range []string{"", ".pub"} {
		if _, err := os.Stat(keyPath + suffix); err != nil {
			t.Fatalf("expected service enroll to generate %s: %v", keyPath+suffix, err)
		}
	}
	if !strings.Contains(out, "IdentityFile") {
		t.Errorf("expected enroll to print the ssh_config recipe, got:\n%s", out)
	}

	res := harness.RunClient(t, f.Bin, harness.ClientOptions{
		Args: []string{"service", "retrieve", "--code", code, "--key", keyPath, "--server", f.Server.BaseURL},
	})
	if res.ExitCode != 0 {
		t.Fatalf("service retrieve failed with exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	cert := readCertificate(t, keyPath)

	// The principal is the service account the approver chose, never the
	// approving identity. alice approved this; a certificate for alice
	// would be the whole feature failing open.
	if got := cert.ValidPrincipals; len(got) != 1 || got[0] != f.Account {
		t.Errorf("got principals %v, want [%s]", got, f.Account)
	}
	if slicesContains(cert.ValidPrincipals, "alice") {
		t.Error("the certificate carries the approver's identity as a principal")
	}

	caKey, err := harness.ParseAuthorizedKey(f.Server.CAPublicKey)
	if err != nil {
		t.Fatalf("harness: failed to parse the test CA public key: %v", err)
	}
	if !harness.SameSSHKey(cert.SignatureKey, caKey) {
		t.Error("the retrieved certificate is not signed by the configured test CA")
	}
}

// The certificate must be bound to the key that was enrolled. `retrieve`
// posts only the code -- it never sends a public key -- so the server has to
// be the one remembering which key the code belongs to.
func TestService_ShouldBindTheCertificateToTheEnrolledKey(t *testing.T) {
	f := newServiceFixture(t)
	keyPath := filepath.Join(t.TempDir(), "svckey")

	code, _ := f.enroll(t, keyPath)

	res := harness.RunClient(t, f.Bin, harness.ClientOptions{
		Args: []string{"service", "retrieve", "--code", code, "--key", keyPath, "--server", f.Server.BaseURL},
	})
	if res.ExitCode != 0 {
		t.Fatalf("service retrieve failed with exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	enrolled, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatalf("failed to read the enrolled public key: %v", err)
	}
	enrolledKey, err := harness.ParseAuthorizedKey(string(enrolled))
	if err != nil {
		t.Fatalf("enrolled public key does not parse: %v", err)
	}

	cert := readCertificate(t, keyPath)
	if !harness.SameSSHKey(cert.Key, enrolledKey) {
		t.Error("the retrieved certificate is not bound to the enrolled public key")
	}
}

func TestService_ShouldRetrieveImmediatelyWhenAskedTo(t *testing.T) {
	f := newServiceFixture(t)
	keyPath := filepath.Join(t.TempDir(), "svckey")

	_, out := f.enroll(t, keyPath, "--retrieve")

	// --retrieve exists so the operator finds out the enrollment works now
	// rather than when cron first runs, so the certificate has to be on
	// disk by the time enroll exits.
	cert := readCertificate(t, keyPath)
	if got := cert.ValidPrincipals; len(got) != 1 || got[0] != f.Account {
		t.Errorf("got principals %v, want [%s]", got, f.Account)
	}
	if !strings.Contains(out, "retrieved right away") {
		t.Errorf("expected enroll to report the immediate retrieval, got:\n%s", out)
	}
}

// Codes are reusable by design -- that is what makes them safe to put in a
// cron job -- so a second redemption has to work.
func TestService_ShouldAllowTheCodeToBeRedeemedAgain(t *testing.T) {
	f := newServiceFixture(t)
	keyPath := filepath.Join(t.TempDir(), "svckey")

	code, _ := f.enroll(t, keyPath)

	for i, args := range [][]string{
		{"service", "retrieve", "--code", code, "--key", keyPath, "--server", f.Server.BaseURL},
		// --force on the second run, or the local freshness cache returns
		// the certificate already on disk and the server is never asked --
		// which would make this test pass without proving reuse.
		{"service", "retrieve", "--code", code, "--key", keyPath, "--server", f.Server.BaseURL, "--force"},
	} {
		res := harness.RunClient(t, f.Bin, harness.ClientOptions{Args: args})
		if res.ExitCode != 0 {
			t.Fatalf("redemption %d failed with exit %d\nstderr:\n%s", i+1, res.ExitCode, res.Stderr)
		}
	}

	cert := readCertificate(t, keyPath)
	if got := cert.ValidPrincipals; len(got) != 1 || got[0] != f.Account {
		t.Errorf("got principals %v, want [%s]", got, f.Account)
	}
}

func TestService_ShouldRefuseAnUnknownEnrollmentCode(t *testing.T) {
	f := newServiceFixture(t)
	keyPath := filepath.Join(t.TempDir(), "svckey")

	res := harness.RunClient(t, f.Bin, harness.ClientOptions{
		Args: []string{"service", "retrieve", "--code", "not-a-real-code", "--key", keyPath, "--server", f.Server.BaseURL},
	})

	if res.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an unknown code, got 0\nstdout:\n%s", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "not found or expired") {
		t.Errorf("expected the error to say the code is unknown, got:\n%s", res.Stderr)
	}
	if _, err := os.Stat(keyPath + "-cert.pub"); err == nil {
		t.Error("a failed retrieval wrote a certificate file")
	}
}

func TestService_ShouldRefuseADeniedEnrollment(t *testing.T) {
	f := newServiceFixture(t)
	keyPath := filepath.Join(t.TempDir(), "svckey")

	proc := harness.StartClient(t, f.Bin, harness.ClientOptions{
		Args: []string{"service", "enroll", "--key", keyPath, "--server", f.Server.BaseURL},
	})
	requestID := requestIDFromApprovalURL(t, proc.ApprovalURL(t, waitFor))

	client := newBrowserClient(t)
	authenticate(t, client, f.Server.BaseURL, "/approve/"+requestID, "alice", nil)
	if err := harness.Deny(client, f.Server.BaseURL, requestID); err != nil {
		t.Fatalf("harness: %v", err)
	}

	if err := proc.Wait(t, waitFor); err == nil {
		t.Fatal("expected service enroll to fail after denial, got a clean exit")
	}
	if !strings.Contains(proc.Stderr(), "denied") {
		t.Errorf("expected the error to say the request was denied, got:\n%s", proc.Stderr())
	}
}

// Half a keypair is an error, not something to work around: ssh will not use
// a certificate without the private key beside it, so an enrollment built
// from a lone public key produces certificates nothing on the host can
// present.
func TestService_ShouldRefuseAHalfPresentKeypair(t *testing.T) {
	f := newServiceFixture(t)

	tests := []struct {
		name    string
		present string
		wantMsg string
	}{
		{name: "public key without private", present: ".pub", wantMsg: "ssh needs the private key beside the public one"},
		{name: "private key without public", present: "", wantMsg: "the public key must sit beside the private key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyPath := filepath.Join(t.TempDir(), "svckey")
			if err := os.WriteFile(keyPath+tt.present, []byte("placeholder\n"), 0600); err != nil {
				t.Fatalf("failed to write the half keypair: %v", err)
			}

			// A reachable server, even though nothing is sent: root's PreRun
			// fetches the CA before any command's own validation runs, so
			// against a dead address every one of these cases reports a
			// connection error instead of the message being asserted.
			res := harness.RunClient(t, f.Bin, harness.ClientOptions{
				Args: []string{"service", "enroll", "--key", keyPath, "--server", f.Server.BaseURL},
			})

			if res.ExitCode == 0 {
				t.Fatalf("expected a non-zero exit, got 0\nstdout:\n%s", res.Stdout)
			}
			if !strings.Contains(res.Stderr, tt.wantMsg) {
				t.Errorf("expected an error saying %q, got:\n%s", tt.wantMsg, res.Stderr)
			}
		})
	}
}

// Note the server has to be reachable for these: argument validation runs
// inside the command, and root's PreRun fetches the CA on the way there. So
// `ssoossh service enroll` with no --key against a down server reports the
// connection failure rather than the usage error -- a real ordering, worth
// pinning here so a change to it is a deliberate one.
func TestService_ShouldRequireItsMandatoryFlags(t *testing.T) {
	f := newServiceFixture(t)

	tests := []struct {
		name    string
		args    []string
		wantMsg string
	}{
		{name: "enroll without --key", args: []string{"service", "enroll"}, wantMsg: "--key is required"},
		{name: "retrieve without --code", args: []string{"service", "retrieve"}, wantMsg: "--code is required"},
		{name: "retrieve without --key", args: []string{"service", "retrieve", "--code", "x"}, wantMsg: "--key is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append(append([]string{}, tt.args...), "--server", f.Server.BaseURL)

			res := harness.RunClient(t, f.Bin, harness.ClientOptions{Args: args})

			if res.ExitCode == 0 {
				t.Fatalf("expected a non-zero exit, got 0\nstdout:\n%s", res.Stdout)
			}
			if !strings.Contains(res.Stderr, tt.wantMsg) {
				t.Errorf("expected %q, got:\n%s", tt.wantMsg, res.Stderr)
			}
		})
	}
}

// slicesContains avoids pulling slices into a file that needs it once.
func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
