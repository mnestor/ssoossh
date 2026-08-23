package cmd

import "github.com/bep/simplecobra"

func newHostCommand() simplecobra.Commander {
	var mappingFile string

	return &simpleCommand{
		name:  "host",
		short: "Manage local sshd principal mapping.",
		long: "Configures the principal mapping used when sshd asks for authorized principals " +
			"via AuthorizedPrincipalsCommand. Principals can be synced from the server or " +
			"managed locally.",
		init: func(cd *simplecobra.Commandeer) error {
			cd.CobraCommand.PersistentFlags().StringVar(&mappingFile, "file", "/etc/ssoossh/principals.json",
				"path to the local principal mapping file (JSON object: local account -> list of principals)")
			return nil
		},
		commands: []simplecobra.Commander{
			newHostPrincipalsCommand(func() string { return mappingFile }),
			newHostMappingCommand(func() string { return mappingFile }),
		},
	}
}
