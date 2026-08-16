package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newServiceRetrieveCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "retrieve",
		short: "Retrieve a service certificate using a previously issued enrollment code.",
		long: "Posts only the enrollment code from `service enroll` — never resubmits the " +
			"public key — so a stolen code cannot be paired with an attacker's own keypair.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "service retrieve"}
		},
	}
}
