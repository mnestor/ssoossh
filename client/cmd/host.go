package cmd

import "github.com/bep/simplecobra"

func newHostCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "host",
		short: "Manage local sshd principal mapping.",
		long: "Configures the principal mapping used when sshd asks for authorized principals " +
			"via AuthorizedPrincipalsCommand. Principals can be synced from the server or " +
			"managed locally.",
		commands: []simplecobra.Commander{
			newHostPrincipalsCommand(),
			newHostMappingCommand(),
		},
	}
}
