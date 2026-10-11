package cmd

import "github.com/bep/simplecobra"

func newHostCommand() simplecobra.Commander {
	var mappingFile string

	return &simpleCommand{
		name:  "host",
		short: "Manage local sshd principal mapping, and check sshd's setup.",
		long: "Configures the local principal mapping sshd consults through " +
			"AuthorizedPrincipalsCommand: which certificate principals may log in as which " +
			"local account. The mapping is a file on this host; nothing about it is stored " +
			"on, or synced from, the server. Also checks that this host's sshd will accept " +
			"ssoossh certificates.",
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
