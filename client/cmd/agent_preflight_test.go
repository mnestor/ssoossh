package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xssh "golang.org/x/crypto/ssh"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
	sshagent "github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
)

// TestRunLogin_PreflightShouldSucceedWithHealthyAgent verifies that a healthy
// agent passes the preflight and the login proceeds.
func TestRunLogin_PreflightShouldSucceedWithHealthyAgent(t *testing.T) {
	ours := newTestCA(t)
	ag := &stubAgent{cas: []xssh.PublicKey{ours.public}}
	fresh := newTestCert(t, ours, "alice", 3600)
	client := &fakeAPIClient{result: &api.CertificateResult{
		Status:      api.StatusApproved,
		Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
	}}

	var out bytes.Buffer
	if err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.createdWith) != 1 {
		t.Error("expected a certificate request to be created when preflight passes")
	}
}

// TestRunLogin_PreflightShouldFailBeforeRequestWhenAgentAddFails tests that
// when the agent cannot add a key, the preflight fails closed and no
// certificate request is created.
func TestRunLogin_PreflightShouldFailBeforeRequestWhenAgentAddFails(t *testing.T) {
	ours := newTestCA(t)
	ag := &stubAgent{
		cas:    []xssh.PublicKey{ours.public},
		addErr: errors.New("agent storage full"),
	}
	client := &fakeAPIClient{}

	var out bytes.Buffer
	err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, false)
	if err == nil {
		t.Fatal("expected preflight to fail when agent.Add fails")
	}

	if len(client.createdWith) != 0 {
		t.Error("expected no certificate request when preflight failed")
	}

	if !errors.Is(err, ag.addErr) {
		t.Errorf("got error %v, want the underlying add error", err)
	}
}

// TestRunLogin_PreflightShouldFailBeforeRequestWhenAgentRemoveFails tests that
// when the probe key cannot be removed, the preflight fails and no certificate
// request is created.
func TestRunLogin_PreflightShouldFailBeforeRequestWhenAgentRemoveFails(t *testing.T) {
	ours := newTestCA(t)
	ag := &stubAgent{
		cas:       []xssh.PublicKey{ours.public},
		removeErr: errors.New("cannot remove from agent"),
	}
	client := &fakeAPIClient{}

	var out bytes.Buffer
	err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, false)
	if err == nil {
		t.Fatal("expected preflight to fail when agent.Remove fails")
	}

	if len(client.createdWith) != 0 {
		t.Error("expected no certificate request when preflight failed")
	}
}

// TestRunLogin_PreflightProbeKeyIsRemovedOnSuccess verifies that the probe
// keypair is cleaned up after a successful preflight.
func TestRunLogin_PreflightProbeKeyIsRemovedOnSuccess(t *testing.T) {
	ours := newTestCA(t)
	ag := &stubAgent{cas: []xssh.PublicKey{ours.public}}
	fresh := newTestCert(t, ours, "alice", 3600)
	client := &fakeAPIClient{result: &api.CertificateResult{
		Status:      api.StatusApproved,
		Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
	}}

	var out bytes.Buffer
	if err := runLogin(context.Background(), newLoginRoot(nil, ag, client), &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// After a successful login, only the real certificate should be in the agent.
	// The probe key should have been removed.
	if ag.added == nil || ag.added.Certificate() == nil {
		t.Fatal("expected the real certificate to be loaded")
	}

	// Count how many identities are in the agent - should be just the one real certificate
	identities, err := ag.List(false)
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	if len(identities) != 1 {
		t.Errorf("got %d identities, want 1 (the real certificate); this suggests the probe key was not cleaned up", len(identities))
	}
}

// TestRunLogin_PreflightShouldFailWhenFallbackDisabledAndAgentFails tests
// that when the live agent fails the preflight and fallback_file_agent is false,
// the login fails before any certificate request is made.
func TestRunLogin_PreflightShouldFailWhenFallbackDisabledAndAgentFails(t *testing.T) {
	ours := newTestCA(t)
	tmpdir := t.TempDir()
	keyPath := filepath.Join(tmpdir, "id_ssoossh")

	cfg := &config.Config{
		UseAgent:          true,
		FallbackFileAgent: false,
		Filename:          keyPath,
	}

	// The live agent will fail the preflight
	failingAgent := &stubAgent{
		cas:       []xssh.PublicKey{ours.public},
		addErr:    errors.New("agent unavailable"),
		agentType: "ssh-agent", // Mark as SshAgent so it probes with probeAgentPreflight
	}

	client := &fakeAPIClient{}
	root := newLoginRoot(cfg, failingAgent, client)

	var out bytes.Buffer
	err := runLogin(context.Background(), root, &out, false)

	if err == nil {
		t.Fatal("expected error when agent fails and fallback is disabled")
	}

	if len(client.createdWith) != 0 {
		t.Error("expected no certificate request when preflight failed")
	}

	if !strings.Contains(err.Error(), "fallback is disabled") {
		t.Errorf("error should mention fallback is disabled, got: %v", err)
	}
}

// TestRunLogin_PreflightShouldAbortWhenFallbackDisabled tests that when
// the live agent fails and fallback is disabled, the login fails before
// any certificate request is created.
func TestRunLogin_PreflightShouldAbortWhenFallbackDisabled(t *testing.T) {
	ours := newTestCA(t)
	cfg := &config.Config{
		UseAgent:          true,
		FallbackFileAgent: false,
	}

	ag := &stubAgent{
		cas:    []xssh.PublicKey{ours.public},
		addErr: errors.New("agent unavailable"),
	}
	client := &fakeAPIClient{}

	var out bytes.Buffer
	err := runLogin(context.Background(), newLoginRoot(cfg, ag, client), &out, false)

	if err == nil {
		t.Fatal("expected login to fail when agent preflight fails and fallback is disabled")
	}

	if len(client.createdWith) != 0 {
		t.Error("expected no certificate request when preflight failed with no fallback")
	}
}

// TestRunLogin_PreflightShouldSucceedWithUseAgentFalse tests that when
// use_agent is false, the preflight against file storage succeeds.
func TestRunLogin_PreflightShouldSucceedWithUseAgentFalse(t *testing.T) {
	ours := newTestCA(t)
	tmpdir := t.TempDir()
	keyPath := filepath.Join(tmpdir, "id_ssoossh")

	cfg := &config.Config{
		UseAgent: false,
		Filename: keyPath,
	}

	// Create a file agent for testing
	fileAgentImpl, err := sshagent.NewFileAgent(keyPath)
	if err != nil {
		t.Fatalf("NewFileAgent() error: %v", err)
	}
	if err := fileAgentImpl.SetCA(string(xssh.MarshalAuthorizedKey(ours.public))); err != nil {
		t.Fatalf("SetCA() error: %v", err)
	}

	fresh := newTestCert(t, ours, "alice", 3600)
	client := &fakeAPIClient{result: &api.CertificateResult{
		Status:      api.StatusApproved,
		Certificate: string(xssh.MarshalAuthorizedKey(fresh)),
	}}

	// Construct RootCommand directly instead of using newLoginRoot
	root := &RootCommand{cfg: cfg, api: client, ssh: fileAgentImpl}

	var out bytes.Buffer
	if err := runLogin(context.Background(), root, &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(client.createdWith) != 1 {
		t.Error("expected a certificate request to be created when file agent preflight passes")
	}
}

// TestRunLogin_PreflightShouldFailBeforeRequestWhenFileAgentAddFails tests
// that when file-based key storage cannot be written, preflight fails and no
// request is created.
func TestRunLogin_PreflightShouldFailBeforeRequestWhenFileAgentAddFails(t *testing.T) {
	t.Parallel()

	ours := newTestCA(t)

	// The probe writes beside the configured key file, so the failure has to
	// come from the directory it writes into. A parent that is a regular file
	// fails every write below it with ENOTDIR, for every user. Permission bits
	// would not: codecover.yaml runs the suite as root inside a container,
	// where an "unwritable" directory is still writable, the probe passed, and
	// the request this test forbids went out.
	notADir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(notADir, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}
	keyPath := filepath.Join(notADir, "id_ecdsa")

	cfg := &config.Config{
		UseAgent: false,
		Filename: keyPath,
	}

	fileAgent, err := sshagent.NewFileAgent(keyPath)
	if err != nil {
		t.Fatalf("NewFileAgent() error: %v", err)
	}
	if err := fileAgent.SetCA(string(xssh.MarshalAuthorizedKey(ours.public))); err != nil {
		t.Fatalf("SetCA() error: %v", err)
	}

	client := &fakeAPIClient{}
	root := &RootCommand{cfg: cfg, api: client, ssh: fileAgent}

	var out bytes.Buffer
	err = runLogin(context.Background(), root, &out, false)
	if err == nil {
		t.Fatal("expected preflight to fail when the key directory cannot be created")
	}

	if len(client.createdWith) != 0 {
		t.Error("expected no certificate request when preflight failed")
	}
}
