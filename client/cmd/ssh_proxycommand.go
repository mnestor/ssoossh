package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/internal/errs"
)

func newSSHProxyCommandCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "proxycommand",
		short: "Ensure a valid certificate, then relay stdio to the target host over TCP.",
		long: "For use as ssh_config's ProxyCommand. Stays running for the life of the " +
			"session and requires a live ssh-agent — unlike `ssh login`, ssh won't re-read " +
			"key files that change out from under it, so file-backed keys are not supported " +
			"in this mode.",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			return &errs.NotImplementedError{What: "ssh proxycommand"}
		},
	}
}
