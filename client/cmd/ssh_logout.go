package cmd

import (
	"context"
	"fmt"
	"io"

	"github.com/bep/simplecobra"

	sshagent "github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
)

func newSSHLogoutCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "logout",
		short: "Remove ssoossh-managed keys and certificates from the agent (or files).",
		long: "Removes the certificates ssoossh put in your ssh-agent — those signed by " +
			"the configured CA — and nothing else. Your own keys are left alone. When " +
			"key files are used instead of an agent, the files ssoossh wrote are deleted.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return runLogout(root, cd.CobraCommand.ErrOrStderr())
		},
	}
}

// runLogout removes what ssoossh added and nothing else.
//
// The distinction that matters is which identities are ours. A certificate
// signed by the configured CA is; anything else in the agent — the user's
// personal keys, certificates from an unrelated CA — is not, and removing it
// would be destroying something this program was never asked to manage.
func runLogout(root *RootCommand, out io.Writer) error {
	agent := root.Agent()

	// A FileAgent owns exactly one identity, the files at its configured
	// path, so its RemoveAll is already scoped to ssoossh's own material.
	if agent.Type() == sshagent.AgentTypeFile {
		removed, err := agent.RemoveAll()
		if err != nil {
			return fmt.Errorf("remove key files: %w", err)
		}
		report(out, removed, "key file")
		return nil
	}

	// List(true) returns only certificate identities signed by a trusted CA,
	// which is the discriminator: it needs no metadata, and it does not
	// depend on the "ssoossh" comment surviving whatever agent
	// implementation is in use. Expired ones are included — they are still
	// ours, and logout should not leave them behind.
	//
	// Deliberately not the whole-agent RemoveAll, and deliberately not
	// CleanupAgent: the first removes every identity in the agent including
	// the user's personal keys, and the second removes certificates from
	// *other* CAs, which are equally none of our business.
	ours, err := agent.List(true)
	if err != nil {
		return fmt.Errorf("list agent identities: %w", err)
	}

	removed := 0
	for _, key := range ours {
		if key == nil {
			continue
		}
		if err := agent.Remove(*key); err != nil {
			return fmt.Errorf("remove certificate from %s: %w", agent.Backend(), err)
		}
		removed++
	}

	report(out, removed, "certificate")
	return nil
}

// report states what happened in the terms the user cares about — whether
// anything was actually removed.
func report(out io.Writer, removed int, noun string) {
	switch removed {
	case 0:
		fmt.Fprintf(out, "Nothing to remove: no ssoossh %ss were present.\n", noun)
	case 1:
		fmt.Fprintf(out, "Removed 1 ssoossh %s.\n", noun)
	default:
		fmt.Fprintf(out, "Removed %d ssoossh %ss.\n", removed, noun)
	}
}
