//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// TestPreflight_ShouldSucceedWithHealthyAgent tests that a successful login
// completes normally and leaves only the certificate in the agent
// (no probe key left behind).
func TestPreflight_ShouldSucceedWithHealthyAgent(t *testing.T) {
	f := newFixture(t)

	login := harness.StartLogin(t, f.SsoosshBin, f.Server.BaseURL, f.Agent.Socket)

	// Check that no keys are in the agent yet (before approval).
	// The preflight probes and then removes the probe key, so the agent
	// should be empty before approval.
	beforeApprovalKeys := f.Agent.AllKeys(t)
	if len(beforeApprovalKeys) != 0 {
		t.Fatalf("expected no keys in agent after preflight, but found %d", len(beforeApprovalKeys))
	}

	// Proceed with approval.
	approvalURL := login.ApprovalURL(t, waitFor)
	requestID := requestIDFromApprovalURL(t, approvalURL)
	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval: %v\nstderr:\n%s", err, login.Stderr())
	}

	// After login, the agent should contain ONLY the certificate (no probe key).
	certs := f.Agent.Certificates(t)
	if len(certs) != 1 {
		t.Fatalf("expected exactly 1 certificate in agent after login, got %d", len(certs))
	}

	allKeys := f.Agent.AllKeys(t)
	if len(allKeys) != 1 {
		t.Fatalf("expected exactly 1 key in agent after login, got %d; probe key was not cleaned up", len(allKeys))
	}
}

// TestPreflight_ShouldFallbackToFilesWhenAgentFailsPreflight tests that when
// the live agent fails the preflight and fallback_file_agent is true, the
// login falls back to file storage and succeeds.
func TestPreflight_ShouldFallbackToFilesWhenAgentFailsPreflight(t *testing.T) {
	f := newFixture(t)

	home := t.TempDir()

	// Start a client with a broken agent but fallback enabled and file storage writable.
	brokenSocket := harness.StartBrokenAgent(t)
	login := harness.StartClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:     []string{"ssh", "login", "--server", f.Server.BaseURL},
		Home:     home,
		UserYAML: "use_agent: true\nfallback_file_agent: true\n",
		Env:      map[string]string{"SSH_AUTH_SOCK": brokenSocket},
	})

	// With fallback enabled, the approval URL should appear
	// (preflight fails on agent but succeeds on file fallback).
	approvalURL := login.ApprovalURL(t, waitFor)
	if !strings.Contains(approvalURL, "/approve/") {
		t.Fatalf("expected approval URL despite broken agent (due to fallback), got: %q", approvalURL)
	}

	requestID := requestIDFromApprovalURL(t, approvalURL)
	client := newBrowserClient(t)
	approve(t, client, f.Server.BaseURL, requestID, "alice", nil)

	if err := login.Wait(t, waitFor); err != nil {
		t.Fatalf("ssh login failed after approval with fallback: %v\nstderr:\n%s", err, login.Stderr())
	}

	// Assert the fallback message was printed (usually to stderr).
	combined := login.Stderr() + login.Stdout()
	if !strings.Contains(combined, "falling back") && !strings.Contains(combined, "fallback") {
		t.Errorf("expected fallback message in output, stderr: %s, stdout: %s", login.Stderr(), login.Stdout())
	}

	// Assert certificate ended up in files, not the agent.
	privateKey, publicKey, certificate := harness.KeyFilePaths(home, keyFilename)
	for _, path := range []string{privateKey, publicKey, certificate} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist after fallback login: %v", path, err)
		}
	}

	// Assert agent is empty (nothing went into the broken agent).
	agentKeys := f.Agent.AllKeys(t)
	if len(agentKeys) > 0 {
		t.Errorf("expected no keys in agent after fallback, but found %d", len(agentKeys))
	}
}

// TestPreflight_ShouldAbortBeforeApprovalWhenAgentFailsAndFallbackDisabled
// tests that when the agent fails the preflight and fallback_file_agent is
// false, the login aborts BEFORE the approval URL is printed and BEFORE a
// certificate request is created server-side.
func TestPreflight_ShouldAbortBeforeApprovalWhenAgentFailsAndFallbackDisabled(t *testing.T) {
	f := newFixture(t)

	// Start a client with a broken agent and fallback disabled.
	brokenSocket := harness.StartBrokenAgent(t)
	login := harness.StartClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:     []string{"ssh", "login", "--server", f.Server.BaseURL},
		Home:     t.TempDir(),
		UserYAML: "use_agent: true\nfallback_file_agent: false\n",
		Env:      map[string]string{"SSH_AUTH_SOCK": brokenSocket},
	})

	// The login should fail before the approval URL is printed.
	if err := login.Wait(t, waitFor); err == nil {
		t.Fatal("expected login to fail with broken agent and fallback disabled")
	}

	stdout := login.Stdout()
	stderr := login.Stderr()

	// ASSERT 1: Approval URL was NOT printed (preflight failed BEFORE request creation).
	if strings.Contains(stdout, "/approve/") {
		t.Error("approval URL was printed despite agent failing and fallback disabled; preflight should have aborted first")
	}

	// ASSERT 2: Error message names the fallback setting.
	if !strings.Contains(stderr, "fallback") && !strings.Contains(stderr, "disabled") {
		t.Errorf("error did not mention fallback or disabled: %s", stderr)
	}
}

// TestPreflight_ShouldNotLeaveProbeKeyInFilesAfterSuccessfulLogin verifies
// that the preflight's probe key (a temporary file in the key directory)
// is cleaned up and doesn't appear in the final key listing.
func TestPreflight_ShouldNotLeaveProbeKeyInFilesAfterSuccessfulLogin(t *testing.T) {
	f := newFixture(t)

	home := loginWithFileStorage(t, f, "alice", "")

	// After a successful file-backed login, only the three expected files
	// should exist (private key, public key, certificate). The probe should
	// have been cleaned up during the preflight.
	privateKey, publicKey, certificate := harness.KeyFilePaths(home, keyFilename)
	dir := harness.KeyFileDir(home, keyFilename)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to list key directory: %v", err)
	}

	// Collect all files that match the pattern of our key files or probes.
	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.Contains(entry.Name(), keyFilename) {
			files = append(files, entry.Name())
		}
	}

	// Should be exactly 3 files: id_ssoossh, id_ssoossh.pub, id_ssoossh-cert.pub
	if len(files) != 3 {
		t.Errorf("expected 3 key files, found %d: %v", len(files), files)
	}

	// Verify the expected files exist
	for _, path := range []string{privateKey, publicKey, certificate} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected %s to exist: %v", path, err)
		}
	}

	// Verify no probe files exist (would be named with -preflight-probe)
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "probe") {
			t.Errorf("found unexpected probe file: %s", entry.Name())
		}
	}
}
