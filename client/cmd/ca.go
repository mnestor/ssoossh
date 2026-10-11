package cmd

import (
	"context"
	"fmt"

	"github.com/bep/simplecobra"
)

func newCACommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "ca",
		short: "Print the CA public key of the configured ssoossh server.",
		long: "Prints the public key of the server's certificate authority in authorized_keys " +
			"format, one key per line (two while a CA rotation is under way). Hosts that accept " +
			"ssoossh certificates name this file in sshd's TrustedUserCAKeys.",
		example: "  # Trust ssoossh certificates on an sshd host\n" +
			"  ssoossh ca | sudo tee /etc/ssh/ssoossh_ca.pub\n" +
			"  # then, in sshd_config: TrustedUserCAKeys /etc/ssh/ssoossh_ca.pub",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			ca, err := root.api.GetCA(ctx)
			if err != nil {
				return err
			}
			fmt.Println(ca)

			return nil
		},
	}
}
