package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newHostRenewCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "renew",
		short: "Renew an existing host certificate, authenticated by the current valid one.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "host renew"}
		},
	}
}
