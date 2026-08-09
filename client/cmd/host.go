package cmd

import "github.com/bep/simplecobra"

func newHostCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "host",
		short: "Manage host certificates and principal mapping.",
		commands: []simplecobra.Commander{
			newHostSignCommand(),
			newHostRenewCommand(),
			newHostSyncCommand(),
			newHostPrincipalsCommand(),
		},
	}
}
