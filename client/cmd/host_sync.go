package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newHostSyncCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "sync",
		short: "Pull the principal mapping from the server and write it locally.",
		long: "Writes whatever `host principals` answers from on the next login attempt. " +
			"Purely local mapping files remain supported alongside server-synced ones.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "host sync"}
		},
	}
}
