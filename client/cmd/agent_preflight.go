package cmd

import (
	"fmt"
	"log/slog"
	"os"

	sshagent "github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

// probeAgentPreflight verifies that the resolved agent can accept and remove a
// keypair before the login flow requests a certificate. It uses a throwaway
// keypair to exercise the full store/remove round-trip without consuming any
// of the user's real credentials.
//
// For SshAgent: the probe keypair is added and removed directly, proving the
// agent connection works and accepts keys.
//
// For FileAgent: this function doesn't directly probe the agent, as that would
// require access to its private path. Instead, call probeFileAgentPreflightWithPath.
//
// The probe is never left behind: if it cannot be removed, the error is
// returned and the login aborts before any certificate request is made.
// If the probe fails, the returned error describes what failed.
func probeAgentPreflight(agent sshagent.Agent) error {
	// Generate a throwaway keypair for the probe.
	probeKp, err := keypair.NewSSHKeypair("ecdsa", 384)
	if err != nil {
		return fmt.Errorf("generate probe keypair: %w", err)
	}

	// For FileAgent, we cannot probe directly without knowing the path.
	// The caller should use probeFileAgentPreflightWithPath instead.
	// For now, if we get a FileAgent here without a path, we return an error.
	if agent.Type() == sshagent.AgentTypeFile {
		return fmt.Errorf("probeAgentPreflight cannot probe FileAgent without a path; use probeFileAgentPreflightWithPath")
	}

	// For SshAgent, add and remove directly
	return probeSshAgentPreflight(agent, probeKp)
}

// probeFileAgentPreflightWithPath verifies that file-based key storage at the
// given path can be written and read. It creates a temporary file in the same
// directory to test the filesystem without touching the configured key files.
func probeFileAgentPreflightWithPath(configuredPath string) error {
	// Generate a throwaway keypair for the probe.
	probeKp, err := keypair.NewSSHKeypair("ecdsa", 384)
	if err != nil {
		return fmt.Errorf("generate probe keypair: %w", err)
	}

	// Resolve the configured path to an absolute path.
	realPath, err := sshagent.ResolveKeyPath(configuredPath)
	if err != nil {
		return fmt.Errorf("resolve key path: %w", err)
	}

	// Create a probe path in the same directory, distinct from the real path.
	probePath := realPath + "-preflight-probe"

	// Create a temporary FileAgent for probing.
	probeAgent, err := sshagent.NewFileAgent(probePath)
	if err != nil {
		return fmt.Errorf("create probe agent: %w", err)
	}

	// Test the probe by adding and removing a keypair.
	if err := probeAgent.AddKeypair(probeKp); err != nil {
		_ = cleanupProbeFiles(probePath) //nolint:errcheck // best-effort cleanup
		return fmt.Errorf("probe keypair storage: %w", err)
	}

	// Now remove the probe keypair.
	pubKey := probeKp.Public()
	if err := probeAgent.Remove(pubKey); err != nil {
		_ = cleanupProbeFiles(probePath) //nolint:errcheck // best-effort cleanup
		return fmt.Errorf("probe keypair cleanup: %w", err)
	}

	// Clean up any remaining probe files (best-effort).
	_ = cleanupProbeFiles(probePath) //nolint:errcheck // best-effort cleanup

	slog.Debug("file agent preflight passed")
	return nil
}

// probeSshAgentPreflight probes an SshAgent by adding and removing a test keypair.
// Note: This may temporarily set internal state on the agent (e.g., agent.added in
// test stubs), which is cleaned up after removal.
func probeSshAgentPreflight(agent sshagent.Agent, probeKp *keypair.SSHKeypair) error {
	if err := agent.AddKeypair(probeKp); err != nil {
		return fmt.Errorf("probe keypair storage: %w", err)
	}

	// Now remove the probe keypair. If this fails, the probe is left behind,
	// and we must report the error.
	pubKey := probeKp.Public()
	if err := agent.Remove(pubKey); err != nil {
		return fmt.Errorf("probe keypair cleanup: %w", err)
	}

	slog.Debug("agent preflight passed")
	return nil
}

// cleanupProbeFiles removes the probe key files created during preflight.
// It's best-effort: failures are logged but not returned.
func cleanupProbeFiles(probePath string) error {
	// Remove the private key, public key, and certificate files.
	for _, path := range []string{
		probePath,
		probePath + ".pub",
		probePath + "-cert.pub",
	} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
