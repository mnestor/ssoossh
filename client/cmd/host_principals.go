package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newHostPrincipalsCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "principals",
		short: "Print the local principal mapping for sshd's AuthorizedPrincipalsCommand.",
		// The "must never touch the network" below is enforced, not just
		// documented: offline makes root's PreRun skip the CA fetch, so
		// there is no server round-trip anywhere in this command's path.
		offline: true,
		long: "Implements AuthorizedPrincipalsCommand. Runs as root, called on every login " +
			"attempt, and must never touch the network — it answers only from whatever " +
			"`host sync` last wrote locally. Cache staleness (via file mtime or `host sync` " +
			"exit status) is the host admin's call, not this command's.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "host principals"}
		},
	}
}
