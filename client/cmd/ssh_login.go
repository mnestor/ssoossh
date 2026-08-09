package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newSSHLoginCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "login",
		short: "Authenticate via OIDC and load a signed SSH certificate into the agent (or files).",
		long: "Generates a fresh keypair, sends the public key to the ssoossh server, opens " +
			"the browser for OIDC approval, and waits over SSE for the signed certificate. " +
			"Used from ssh_config's `Match exec`, or interactively before a session.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "ssh login"}
		},
	}
}
