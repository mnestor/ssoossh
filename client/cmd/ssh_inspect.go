package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newSSHInspectCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "inspect",
		short: "Print details of the currently held ssoossh certificate(s).",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "ssh inspect"}
		},
	}
}
