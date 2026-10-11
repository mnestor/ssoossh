package cmd

import "github.com/bep/simplecobra"

func newSSHCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "ssh",
		short: "Manage interactive SSH certificates.",
		long: "Certificates for people. `login` gets one (or reuses a valid one), `logout` " +
			"removes it, `inspect` shows what is loaded, `proxycommand` gets one and then " +
			"hands the connection to a relay, and `config` prints the ssh_config lines that " +
			"make ssh run all this for you. Start with `ssoossh ssh config`.",
		commands: []simplecobra.Commander{
			newSSHLoginCommand(),
			newSSHLogoutCommand(),
			newSSHProxyCommandCommand(),
			newSSHInspectCommand(),
			newSSHConfigCommand(),
		},
	}
}
