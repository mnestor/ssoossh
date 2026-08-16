package cmd

import "github.com/bep/simplecobra"

func newSSHCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "ssh",
		short: "Manage interactive SSH certificates.",
		commands: []simplecobra.Commander{
			newSSHLoginCommand(),
			newSSHLogoutCommand(),
			newSSHProxyCommandCommand(),
			newSSHInspectCommand(),
			newSSHConfigCommand(),
		},
	}
}
