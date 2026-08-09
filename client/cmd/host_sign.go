package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newHostSignCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "sign",
		short: "Request first-time issuance of a host certificate via OIDC approval.",
		long: "A human vouches for the machine through the OIDC approval chain — the " +
			"anti-MITM control for host certificates. Use `host renew` for subsequent " +
			"issuance once a valid host certificate already exists.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "host sign"}
		},
	}
}
