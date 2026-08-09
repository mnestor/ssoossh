package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newSSHLogoutCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "logout",
		short: "Remove ssoossh-managed keys and certificates from the agent (or files).",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "ssh logout"}
		},
	}
}
