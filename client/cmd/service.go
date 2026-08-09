package cmd

import "github.com/bep/simplecobra"

func newServiceCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "service",
		short: "Manage service-account (non-interactive) certificate enrollment.",
		commands: []simplecobra.Commander{
			newServiceEnrollCommand(),
			newServiceRetrieveCommand(),
		},
	}
}
