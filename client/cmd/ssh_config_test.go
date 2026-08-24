package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// `ssh config` is the wiring harness and nothing else. The recipes are the
// whole product of the command, so each of the two modes has to be there
// along with the constraint that decides between them.
func TestRunConfig_ShouldPrintBothSshConfigRecipes(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := runConfig(&out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []string{
		"Match host bastion.example.com exec",
		"ProxyCommand ssoossh ssh proxycommand",
		"Requires an ssh-agent",
		"ssoossh service enroll",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("expected the guidance to contain %q, got:\n%s", want, out.String())
		}
	}
}

// The command must not report settings: --debug is the only place those are
// answered, and a second, shorter version here is what made the two drift
// apart in the first place.
func TestRunConfig_ShouldNotReportResolvedSettings(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	if err := runConfig(&out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, unwanted := range []string{"TLS verification", "Key type", "CA public key", "Storage"} {
		if strings.Contains(out.String(), unwanted) {
			t.Errorf("expected no %q line; --debug reports settings, not this command. Got:\n%s", unwanted, out.String())
		}
	}
}

// Declared offline so root's PreRun skips the CA fetch. Without it the
// command cannot answer when no server is configured or the configured one
// is down, which is exactly when someone reads it.
func TestSSHConfigCommand_ShouldDeclareItselfOffline(t *testing.T) {
	t.Parallel()

	if !isOffline(newSSHConfigCommand()) {
		t.Error("ssh config must be offline: it prints a static recipe and has no reason to reach the server")
	}
}

// The long help embeds the same guidance the command prints, so --help and
// the generated man page carry the recipes rather than only describing
// them, and cannot drift from what a plain run says.
func TestSSHConfigCommand_ShouldPutTheSameGuidanceInItsHelp(t *testing.T) {
	t.Parallel()

	cmd, ok := newSSHConfigCommand().(*simpleCommand)
	if !ok {
		t.Fatalf("newSSHConfigCommand() returned %T, want *simpleCommand", newSSHConfigCommand())
	}
	if !strings.Contains(cmd.long, sshConfigGuidance) {
		t.Errorf("expected the long help to embed the guidance verbatim, got:\n%s", cmd.long)
	}
	if !strings.Contains(cmd.long, "--debug") {
		t.Errorf("expected the long help to send the reader to --debug for settings, got:\n%s", cmd.long)
	}
}
