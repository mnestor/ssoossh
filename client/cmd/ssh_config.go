package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newSSHConfigCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "config",
		short: "Print or validate the effective ssoossh client configuration.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "ssh config"}
		},
	}
}
