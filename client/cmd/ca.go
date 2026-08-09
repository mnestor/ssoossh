package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newCACommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "ca",
		short: "Retrieve the CA Public Key of the configured ssoossh server.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "ca"}
		},
	}
}
