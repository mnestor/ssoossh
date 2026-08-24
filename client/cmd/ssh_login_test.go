package cmd

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	xssh "golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
	sshagent "github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// newLoginRoot assembles a RootCommand around a stub agent and API client,
// which is all runLogin touches.
func newLoginRoot(cfg *config.Config, ag *stubAgent, client *fakeAPIClient) *RootCommand {
	if cfg == nil {
		cfg = &config.Config{}
	}
	return &RootCommand{cfg: cfg, api: client, ssh: ag}
}

// TestRunLogin_ShouldReuseAValidCertificate is the ergonomic claim the whole
// project rests on: one approval covers a workday, not one per connection.
func TestRunLogin_ShouldReuseAValidCertificate(t *testing.T) {
	ours := newTestCA(t)
	valid := newTestCert(t, ours, "alice", 8*time.Hour)
	ag := &stubAgent{identities: []xssh.PublicKey{valid}, cas: []xssh.PublicKey{ours.public}}
	client := &fakeAPIClient{}

	var out bytes.Buffer
	if err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.createdWith) != 0 {
		t.Error("expected no certificate request when a valid one is already loaded")
	}
	if !strings.Contains(out.String(), "Already have a valid certificate") {
		t.Errorf("got %q, want it to say the existing certificate was reused", out.String())
	}
}

// TestRunLogin_ShouldRequestANewCertificateWhenTheLoadedOneIsExpired is the
// other half: reuse must not extend to a certificate that cannot be used.
func TestRunLogin_ShouldRequestANewCertificateWhenTheLoadedOneIsExpired(t *testing.T) {
	ours := newTestCA(t)
	expired := newTestCert(t, ours, "alice", -time.Minute)
	ag := &stubAgent{identities: []xssh.PublicKey{expired}, cas: []xssh.PublicKey{ours.public}}

	fresh := newTestCert(t, ours, "alice", time.Hour)
	client := &fakeAPIClient{result: &api.CertificateResult{
		Status:      api.StatusApproved,
		Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
	}}

	var out bytes.Buffer
	if err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.createdWith) != 1 {
		t.Fatalf("expected exactly one certificate request, got %d", len(client.createdWith))
	}
	if ag.added == nil || ag.added.Certificate() == nil {
		t.Fatal("expected the new certificate to be loaded into the agent")
	}
}

// TestRunLogin_ShouldNotReuseACertificateAboutToExpire covers the boundary:
// a certificate with seconds left is technically valid and practically
// useless, since the connection it is reused for outlives it.
func TestRunLogin_ShouldNotReuseACertificateAboutToExpire(t *testing.T) {
	ours := newTestCA(t)
	expiring := newTestCert(t, ours, "alice", reuseGrace/2)
	ag := &stubAgent{identities: []xssh.PublicKey{expiring}, cas: []xssh.PublicKey{ours.public}}

	fresh := newTestCert(t, ours, "alice", time.Hour)
	client := &fakeAPIClient{result: &api.CertificateResult{
		Status:      api.StatusApproved,
		Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
	}}

	var out bytes.Buffer
	if err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.createdWith) != 1 {
		t.Error("expected a certificate expiring within the reuse grace period to be replaced")
	}
}

func TestRunLogin_ShouldRequestANewCertificateWhenForced(t *testing.T) {
	ours := newTestCA(t)
	valid := newTestCert(t, ours, "alice", 8*time.Hour)
	ag := &stubAgent{identities: []xssh.PublicKey{valid}, cas: []xssh.PublicKey{ours.public}}

	fresh := newTestCert(t, ours, "alice", time.Hour)
	client := &fakeAPIClient{result: &api.CertificateResult{
		Status:      api.StatusApproved,
		Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
	}}

	var out bytes.Buffer
	if err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.createdWith) != 1 {
		t.Error("expected --force to request a new certificate despite a valid one being loaded")
	}
}

// TestRunLogin_ShouldPrintTheApprovalURLBeforeWaiting is the direct
// regression test for the API shape this phase had to fix. The URL is
// useless if it only appears after someone has already approved.
func TestRunLogin_ShouldPrintTheApprovalURLBeforeWaiting(t *testing.T) {
	ours := newTestCA(t)
	ag := &stubAgent{cas: []xssh.PublicKey{ours.public}}
	fresh := newTestCert(t, ours, "alice", time.Hour)

	var out bytes.Buffer
	var printedByWaitTime string
	client := &fakeAPIClient{
		pending: &api.PendingRequest{
			RequestID:   "req-1",
			ApprovalURL: "https://ssh.example.test/approve/req-1",
		},
		result: &api.CertificateResult{
			Status:      api.StatusApproved,
			Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
		},
	}
	client.onAwait = func() { printedByWaitTime = out.String() }

	if err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(printedByWaitTime, "https://ssh.example.test/approve/req-1") {
		t.Errorf("the approval URL had not been printed by the time the wait began; output so far was %q", printedByWaitTime)
	}
}

// TestRunLogin_ShouldAskForTheExtensionsAnInteractiveSessionNeeds guards
// against the empty-request trap: the server narrows by intersection, so
// asking for nothing yields a certificate that cannot open a session.
func TestRunLogin_ShouldAskForTheExtensionsAnInteractiveSessionNeeds(t *testing.T) {
	if len(loginExtensions) == 0 {
		t.Fatal("login must request extensions; the server narrows by intersection, so an empty request grants none")
	}
	found := false
	for _, ext := range loginExtensions {
		if ext == "permit-pty" {
			found = true
		}
	}
	if !found {
		t.Error("login should ask for permit-pty, without which no interactive session is possible")
	}
}

// TestCheckOutcome covers every terminal status ssoosshd can send. An
// unhandled one would be reported as success with no certificate to load.
func TestCheckOutcome(t *testing.T) {
	tests := []struct {
		name    string
		result  *api.CertificateResult
		wantErr string
	}{
		{
			name:   "should succeed when approved with a certificate",
			result: &api.CertificateResult{Status: api.StatusApproved, Certificate: "ssh-ed25519-cert-v01@openssh.com AAAA"},
		},
		{
			name:    "should fail when approved with no certificate",
			result:  &api.CertificateResult{Status: api.StatusApproved},
			wantErr: "no certificate was delivered",
		},
		{
			name:    "should explain a denial",
			result:  &api.CertificateResult{Status: api.StatusDenied},
			wantErr: "denied",
		},
		{
			name:    "should explain an expiry",
			result:  &api.CertificateResult{Status: api.StatusExpired},
			wantErr: "expired before anyone approved",
		},
		{
			name:    "should explain a server-side failure",
			result:  &api.CertificateResult{Status: api.StatusFailed},
			wantErr: "could not issue the certificate",
		},
		{
			name:    "should reject an enrollment outcome",
			result:  &api.CertificateResult{Status: api.StatusEnrolled},
			wantErr: "service enrollment",
		},
		{
			name:    "should reject an unknown status",
			result:  &api.CertificateResult{Status: "something-new"},
			wantErr: "unrecognized outcome",
		},
		{
			name:    "should reject a missing result",
			result:  nil,
			wantErr: "resolved with no outcome",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkOutcome(tt.result)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("got %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestDescribeWaitError_ShouldAdviseRerunningOnGone covers the one status
// worth naming: the certificate really was issued, they are never persisted,
// and running the command again is the actual fix.
func TestDescribeWaitError_ShouldAdviseRerunningOnGone(t *testing.T) {
	err := describeWaitError(&api.ResponseError{StatusCode: http.StatusGone, Message: "certificate no longer available"})
	if !strings.Contains(err.Error(), "run ssh login again") {
		t.Errorf("got %q, want advice to run the command again", err)
	}
}

func TestDescribeWaitError_ShouldWrapOtherFailures(t *testing.T) {
	cause := errors.New("connection refused")
	err := describeWaitError(cause)
	if !errors.Is(err, cause) {
		t.Errorf("got %q, want the underlying error preserved for unwrapping", err)
	}
}

func TestRunLogin_ShouldFailWhenTheRequestIsDenied(t *testing.T) {
	ours := newTestCA(t)
	ag := &stubAgent{cas: []xssh.PublicKey{ours.public}}
	client := &fakeAPIClient{result: &api.CertificateResult{Status: api.StatusDenied}}

	var out bytes.Buffer
	err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, false)
	if err == nil {
		t.Fatal("expected a denial to fail the command, since Match exec reads exit status")
	}
	if ag.added != nil {
		t.Error("expected nothing to be loaded into the agent after a denial")
	}
}

func TestRunLogin_ShouldReportAnInvalidKeyConfiguration(t *testing.T) {
	ours := newTestCA(t)
	ag := &stubAgent{cas: []xssh.PublicKey{ours.public}}
	cfg := &config.Config{SSHKey: config.SSHKeyOptions{Type: config.SSHKeyTypeRSA, Size: 512}}

	var out bytes.Buffer
	err := runLogin(context.Background(), newLoginRoot(cfg, ag, &fakeAPIClient{}), &out, false)
	if err == nil {
		t.Fatal("expected an unusable key size to fail before any request is made")
	}
	if !strings.Contains(err.Error(), "ssh key configuration") {
		t.Errorf("got %q, want it to name the key configuration", err)
	}
}

// TestRunLogin_ShouldGenerateTheConfiguredKeyType checks the wiring the plan
// called out: ResolveSSHKey's answer must reach generation, not just
// validation.
func TestRunLogin_ShouldGenerateTheConfiguredKeyType(t *testing.T) {
	tests := []struct {
		name       string
		cfg        *config.Config
		wantPrefix string
	}{
		{
			name:       "should default to ecdsa P-384",
			cfg:        &config.Config{},
			wantPrefix: "ecdsa-sha2-nistp384 ",
		},
		{
			name:       "should honor an explicit ecdsa configuration",
			cfg:        &config.Config{SSHKey: config.SSHKeyOptions{Type: config.SSHKeyTypeECDSA, Size: 384}},
			wantPrefix: "ecdsa-sha2-nistp384 ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ours := newTestCA(t)
			ag := &stubAgent{cas: []xssh.PublicKey{ours.public}}
			fresh := newTestCert(t, ours, "alice", time.Hour)
			client := &fakeAPIClient{result: &api.CertificateResult{
				Status:      api.StatusApproved,
				Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
			}}

			var out bytes.Buffer
			if err := runLogin(context.Background(), newLoginRoot(tt.cfg, ag, client), &out, false); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(client.createdWith) != 1 {
				t.Fatalf("expected one request, got %d", len(client.createdWith))
			}
			if !strings.HasPrefix(client.createdWith[0], tt.wantPrefix) {
				t.Errorf("got a %q key, want one starting %q", client.createdWith[0], tt.wantPrefix)
			}
		})
	}
}

func TestExpiryPhrase(t *testing.T) {
	ours := newTestCA(t)

	tests := []struct {
		name string
		cert *xssh.Certificate
		want string
	}{
		{name: "should report no expiry for a nil certificate", cert: nil, want: "no expiry"},
		{name: "should report an expired certificate", cert: newTestCert(t, ours, "alice", -time.Hour), want: "already expired"},
		{name: "should report the remaining time", cert: newTestCert(t, ours, "alice", 2*time.Hour), want: "from now"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expiryPhrase(tt.cert); !strings.Contains(got, tt.want) {
				t.Errorf("got %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

func TestPrincipalList(t *testing.T) {
	ours := newTestCA(t)
	cert := newTestCert(t, ours, "alice", time.Hour)
	cert.ValidPrincipals = []string{"alice", "root"}

	if got := principalList(cert); got != "alice, root" {
		t.Errorf("got %q, want %q", got, "alice, root")
	}
	if got := principalList(nil); got != "no principals" {
		t.Errorf("got %q, want %q", got, "no principals")
	}
}

// TestRunLogin_ShouldRemoveTheCertificateItReplaces is what makes --force
// mean anything. Adding a certificate to an agent does not replace the one
// before it — they are separate identities — so without this the "replaced"
// certificate stays loaded and usable for its full lifetime, which is the
// opposite of what someone forcing a new one wants.
func TestRunLogin_ShouldRemoveTheCertificateItReplaces(t *testing.T) {
	ours := newTestCA(t)
	old := newTestCert(t, ours, "alice", 8*time.Hour)
	ag := &stubAgent{identities: []xssh.PublicKey{old}, cas: []xssh.PublicKey{ours.public}}

	fresh := newTestCert(t, ours, "alice", time.Hour)
	client := &fakeAPIClient{result: &api.CertificateResult{
		Status:      api.StatusApproved,
		Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
	}}

	var out bytes.Buffer
	if err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, key := range ag.identities {
		if bytes.Equal(key.Marshal(), old.Marshal()) {
			t.Error("the superseded certificate is still loaded and still valid")
		}
	}
	if len(ag.identities) != 1 {
		t.Errorf("got %d identities, want only the new certificate", len(ag.identities))
	}
}

// TestRunLogin_ShouldLeaveOtherIdentitiesAlone applies the logout rule to
// pruning: only certificates signed by our CA are ours to remove.
func TestRunLogin_ShouldLeaveOtherIdentitiesAlone(t *testing.T) {
	ours := newTestCA(t)
	theirs := newTestCA(t)
	personal := newTestKey(t)
	foreign := newTestCert(t, theirs, "alice", time.Hour)

	ag := &stubAgent{
		identities: []xssh.PublicKey{personal, foreign},
		cas:        []xssh.PublicKey{ours.public},
	}

	fresh := newTestCert(t, ours, "alice", time.Hour)
	client := &fakeAPIClient{result: &api.CertificateResult{
		Status:      api.StatusApproved,
		Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
	}}

	var out bytes.Buffer
	if err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	remaining := map[string]bool{}
	for _, key := range ag.identities {
		remaining[string(key.Marshal())] = true
	}
	if !remaining[string(personal.Marshal())] {
		t.Error("login removed the user's personal key")
	}
	if !remaining[string(foreign.Marshal())] {
		t.Error("login removed a certificate from an unrelated CA")
	}
}

// TestRunLogin_ShouldNotFailWhenPruningDoes: a stale certificate that could
// not be removed is worth mentioning, not worth failing a login that already
// produced a usable certificate.
//
// The preflight does one successful Remove (for the probe keypair cleanup),
// then pruning attempts a second Remove (for the old certificate). By setting
// removeErrAfterCount to 1, we allow the preflight to succeed but make pruning fail.
func TestRunLogin_ShouldNotFailWhenPruningDoes(t *testing.T) {
	ours := newTestCA(t)
	old := newTestCert(t, ours, "alice", 8*time.Hour)
	ag := &stubAgent{
		identities:          []xssh.PublicKey{old},
		cas:                 []xssh.PublicKey{ours.public},
		removeErrAfterCount: 1, // Allow preflight Remove, fail on pruning Remove
	}

	fresh := newTestCert(t, ours, "alice", time.Hour)
	client := &fakeAPIClient{result: &api.CertificateResult{
		Status:      api.StatusApproved,
		Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
	}}

	var out bytes.Buffer
	if err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, true); err != nil {
		t.Fatalf("expected the login to succeed despite a failed removal, got %v", err)
	}
	if !strings.Contains(out.String(), "could not remove") {
		t.Errorf("got %q, want the stale certificate mentioned", out.String())
	}
}

// TestEffectiveExtensions_ShouldRequestTheFullSetByDefault verifies the
// baseline: without any opt-outs or policy forbidding, all extensions are
// requested.
func TestEffectiveExtensions_ShouldRequestTheFullSetByDefault(t *testing.T) {
	cfg := &config.Config{}
	removals := make(map[string]extensionRemovalReason)

	exts, err := effectiveExtensions(cfg, removals)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(exts) != len(loginExtensions) {
		t.Errorf("got %d extensions, want %d (all of them)", len(exts), len(loginExtensions))
	}
	if len(removals) != 0 {
		t.Errorf("got removals %v, want none with default config", removals)
	}
}

// TestEffectiveExtensions_ShouldApplyConfigOptOuts checks that config file
// settings remove extensions from the requested set.
func TestEffectiveExtensions_ShouldApplyConfigOptOuts(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *config.Config
		wantRemoved []string
	}{
		{
			name:        "should respect no_pty in config",
			cfg:         &config.Config{CertificateExtensions: config.CertificateExtensionOptions{NoPTY: true}},
			wantRemoved: []string{"permit-pty"},
		},
		{
			name:        "should respect no_agent_forwarding in config",
			cfg:         &config.Config{CertificateExtensions: config.CertificateExtensionOptions{NoAgentForwarding: true}},
			wantRemoved: []string{"permit-agent-forwarding"},
		},
		{
			name: "should respect multiple opt-outs in config",
			cfg: &config.Config{CertificateExtensions: config.CertificateExtensionOptions{
				NoPTY:            true,
				NoPortForwarding: true,
			}},
			wantRemoved: []string{"permit-pty", "permit-port-forwarding"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			removals := make(map[string]extensionRemovalReason)
			exts, err := effectiveExtensions(tt.cfg, removals)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, removed := range tt.wantRemoved {
				if reason, ok := removals[removed]; !ok {
					t.Errorf("extension %q should have been removed, got removals %v", removed, removals)
				} else if reason != removed_config {
					t.Errorf("extension %q should be removed by config, got reason %v", removed, reason)
				}

				// Verify it's not in the returned set.
				for _, ext := range exts {
					if ext == removed {
						t.Errorf("extension %q should be removed but is in the result", removed)
					}
				}
			}
		})
	}
}

// TestEffectiveExtensions_ShouldRefuseEmptySet ensures a certificate cannot
// be issued with no extensions, which would be unusable.
func TestEffectiveExtensions_ShouldRefuseEmptySet(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		errText string
	}{
		{
			name: "should refuse when config removes all extensions",
			cfg: &config.Config{CertificateExtensions: config.CertificateExtensionOptions{
				NoPTY:             true,
				NoAgentForwarding: true,
				NoPortForwarding:  true,
				NoX11Forwarding:   true,
				NoUserRC:          true,
			}},
			errText: "all certificate extensions were opted out via configuration",
		},
		{
			name: "should refuse when policy forbids all extensions",
			cfg: &config.Config{
				ForbiddenCertificateExtensions: loginExtensions,
			},
			errText: "all certificate extensions are forbidden by policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			removals := make(map[string]extensionRemovalReason)
			_, err := effectiveExtensions(tt.cfg, removals)
			if err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), tt.errText) {
				t.Errorf("got error %q, want it to contain %q", err, tt.errText)
			}
		})
	}
}

// TestEffectiveExtensions_ShouldEnforcePolicyAsAFloor checks that policy
// forbidding overrides user settings — a flag cannot re-add what policy forbids.
// When this results in an empty set, it should fail with a policy-specific error.
func TestEffectiveExtensions_ShouldEnforcePolicyAsAFloor(t *testing.T) {
	// Config requests only port-forwarding (opts out of the rest).
	// Policy forbids port-forwarding.
	// Expected: error because policy forbade the only thing config allowed.
	cfg := &config.Config{
		CertificateExtensions: config.CertificateExtensionOptions{
			NoPTY:             true,
			NoAgentForwarding: true,
			NoX11Forwarding:   true,
			NoUserRC:          true,
			// NoPortForwarding: false (it's requested)
		},
		ForbiddenCertificateExtensions: []string{"permit-port-forwarding"},
	}

	removals := make(map[string]extensionRemovalReason)
	_, err := effectiveExtensions(cfg, removals)
	if err == nil {
		t.Fatal("expected an error when policy forbids the only allowed extension")
	}
	if !strings.Contains(err.Error(), "forbidden by policy") {
		t.Errorf("got error %q, want it to mention policy", err)
	}
}

// TestRunLogin_ShouldIncludeEffectiveExtensionsInTheRequest checks that
// the effective set (after all opt-outs and policy) is actually passed to
// the API.
func TestRunLogin_ShouldIncludeEffectiveExtensionsInTheRequest(t *testing.T) {
	tests := []struct {
		name           string
		cfg            *config.Config
		wantExtensions []string
	}{
		{
			name:           "should request default extensions when nothing is opted out",
			cfg:            &config.Config{},
			wantExtensions: loginExtensions,
		},
		{
			name: "should exclude opted-out extensions",
			cfg: &config.Config{CertificateExtensions: config.CertificateExtensionOptions{
				NoAgentForwarding: true,
			}},
			wantExtensions: []string{"permit-pty", "permit-port-forwarding", "permit-X11-forwarding", "permit-user-rc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ours := newTestCA(t)
			ag := &stubAgent{cas: []xssh.PublicKey{ours.public}}
			fresh := newTestCert(t, ours, "alice", time.Hour)
			client := &fakeAPIClient{result: &api.CertificateResult{
				Status:      api.StatusApproved,
				Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
			}}

			var out bytes.Buffer
			if err := runLogin(context.Background(), newLoginRoot(tt.cfg, ag, client), &out, false); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(client.createdWithOpts) != 1 {
				t.Fatalf("expected one request, got %d", len(client.createdWithOpts))
			}

			opts := client.createdWithOpts[0]
			if len(opts.Extensions) != len(tt.wantExtensions) {
				t.Errorf("got %d extensions, want %d: %v", len(opts.Extensions), len(tt.wantExtensions), opts.Extensions)
			}

			// Order doesn't matter for this test, just presence.
			extSet := make(map[string]bool)
			for _, ext := range opts.Extensions {
				extSet[ext] = true
			}
			for _, want := range tt.wantExtensions {
				if !extSet[want] {
					t.Errorf("extension %q not in request, got %v", want, opts.Extensions)
				}
			}
		})
	}
}

// The bug this pins, end to end through a real FileAgent: pruneSuperseded
// removes every loaded identity that is not the certificate just installed.
// Two defects lined up to make that fatal. FileAgent.List answered with the
// bare public key, which never compares equal to a certificate, so prune
// judged the identity it had just written to be a superseded one; and
// FileAgent.Remove ignored the key handed to it and deleted the whole
// identity, so that judgement landed. A file-backed `ssh login` wrote its
// three key files, verified them, then deleted them and reported success —
// `ls ~/.ssh/id_ssoossh*` found nothing afterwards.
//
// Both are fixed. This test only exercises the List half — with List
// answering correctly, prune finds its match and never calls Remove at all
// — so Remove honouring its argument is pinned in
// TestFileAgent_Remove instead.
func TestPruneSuperseded_ShouldNotDeleteTheCertificateItJustInstalled(t *testing.T) {
	t.Parallel()

	ca, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}
	caSigner, err := xssh.NewSignerFromKey(ca.Private())
	if err != nil {
		t.Fatalf("NewSignerFromKey() error = %v", err)
	}
	leaf, err := keypair.NewEd25519KeyPair()
	if err != nil {
		t.Fatalf("NewEd25519KeyPair() error = %v", err)
	}

	cert := &xssh.Certificate{
		Key:         leaf.Public(),
		CertType:    xssh.UserCert,
		KeyId:       "mnestor",
		ValidAfter:  uint64(time.Now().Add(-time.Hour).Unix()),    //nolint:gosec // a Unix timestamp is positive for any real date
		ValidBefore: uint64(time.Now().Add(8 * time.Hour).Unix()), //nolint:gosec // a Unix timestamp is positive for any real date
	}
	if err := cert.SignCert(rand.Reader, caSigner); err != nil {
		t.Fatalf("SignCert() error = %v", err)
	}
	leaf.SetCertificate(cert)

	keyPath := filepath.Join(t.TempDir(), "id_ssoossh")
	ag, err := sshagent.NewFileAgent(keyPath)
	if err != nil {
		t.Fatalf("NewFileAgent() error = %v", err)
	}
	if err := ag.SetCA(string(xssh.MarshalAuthorizedKey(ca.Public()))); err != nil {
		t.Fatalf("SetCA() error = %v", err)
	}
	if err := ag.AddKeypair(leaf); err != nil {
		t.Fatalf("AddKeypair() error = %v", err)
	}

	pruneSuperseded(ag, cert, &bytes.Buffer{})

	for _, path := range []string{keyPath, keyPath + ".pub", keyPath + "-cert.pub"} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to survive pruning the certificate it holds: %v", path, err)
		}
	}
}

// The attribution the user sees has to name the layer they can actually
// change. Flags reach effectiveExtensions through viper -- bindFlags binds
// --no-pty to certificate_extensions.no_pty -- so by the time the value is
// read there is nothing in the struct distinguishing "I typed this" from "a
// file said this". Without SetByFlag the removal is blamed on config, which
// sends someone who just typed --no-pty looking through config files for it.
func TestEffectiveExtensions_ShouldAttributeRemovalsToFlagsWhenAFlagSetThem(t *testing.T) {
	cfg := &config.Config{
		CertificateExtensions: config.CertificateExtensionOptions{NoPTY: true},
		SetByFlag:             map[string]bool{"certificate_extensions.no_pty": true},
	}
	removals := make(map[string]extensionRemovalReason)

	if _, err := effectiveExtensions(cfg, removals); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, ok := removals["permit-pty"]; !ok {
		t.Fatal("expected permit-pty to be removed")
	} else if got != removed_flag {
		t.Errorf("got removal reason %v for a flag-set opt-out, want removed_flag", got)
	}
}

// The same opt-out with no flag behind it stays attributed to config.
func TestEffectiveExtensions_ShouldAttributeRemovalsToConfigWhenNoFlagSetThem(t *testing.T) {
	cfg := &config.Config{
		CertificateExtensions: config.CertificateExtensionOptions{NoPTY: true},
	}
	removals := make(map[string]extensionRemovalReason)

	if _, err := effectiveExtensions(cfg, removals); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := removals["permit-pty"]; got != removed_config {
		t.Errorf("got removal reason %v, want removed_config", got)
	}
}

// The "opted out via command-line flags" message existed but nothing could
// produce it: removed_flag was declared, switched on in two places, and
// never assigned. Opting everything out with flags reported the config
// wording.
func TestEffectiveExtensions_ShouldBlameFlagsWhenFlagsEmptyTheSet(t *testing.T) {
	cfg := &config.Config{
		CertificateExtensions: config.CertificateExtensionOptions{
			NoPTY:             true,
			NoAgentForwarding: true,
			NoPortForwarding:  true,
			NoX11Forwarding:   true,
			NoUserRC:          true,
		},
		SetByFlag: map[string]bool{
			"certificate_extensions.no_pty":              true,
			"certificate_extensions.no_agent_forwarding": true,
			"certificate_extensions.no_port_forwarding":  true,
			"certificate_extensions.no_x11_forwarding":   true,
			"certificate_extensions.no_user_rc":          true,
		},
	}
	removals := make(map[string]extensionRemovalReason)

	_, err := effectiveExtensions(cfg, removals)
	if err == nil {
		t.Fatal("expected an error when every extension is opted out")
	}
	if !strings.Contains(err.Error(), "command-line flags") {
		t.Errorf("got %q, want it to blame command-line flags", err.Error())
	}
}

// And the summary line names the flag rather than the config file.
func TestPrintEffectiveExtensions_ShouldNameFlagsAsTheReason(t *testing.T) {
	removals := map[string]extensionRemovalReason{"permit-pty": removed_flag}
	var buf bytes.Buffer

	printEffectiveExtensions(&buf, []string{"permit-user-rc"}, loginExtensions, removals)

	if !strings.Contains(buf.String(), "permit-pty(flag)") {
		t.Errorf("got %q, want it to attribute permit-pty to a flag", buf.String())
	}
}
