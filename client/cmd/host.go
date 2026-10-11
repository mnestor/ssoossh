package cmd

import "github.com/bep/simplecobra"

func newHostCommand() simplecobra.Commander {
	var mappingFile string

	return &simpleCommand{
		name:  "host",
		short: "Manage local sshd principal mapping, and check sshd's setup.",
		long: "Configures the principal mapping used when sshd asks for authorized principals " +
			"via AuthorizedPrincipalsCommand, and checks that this host's sshd will accept " +
			"ssoossh certificates. The mapping is managed locally and never fetched from " +
			"the server.",
		init: func(cd *simplecobra.Commandeer) error {
			cd.CobraCommand.PersistentFlags().StringVar(&mappingFile, "file", defaultMappingPath,
				"path to the local principal mapping file (YAML: local account -> list of principals)")
			return nil
		},
		commands: []simplecobra.Commander{
			newHostPrincipalsCommand(func() string { return mappingFile }),
			newHostMappingCommand(func() string { return mappingFile }),
			newHostCheckCommand(),
		},
	}
}
