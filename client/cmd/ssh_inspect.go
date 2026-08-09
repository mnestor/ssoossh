package cmd

import (
	"context"
	"fmt"

	"github.com/bep/simplecobra"
	"golang.org/x/crypto/ssh"
)

func newSSHInspectCommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "inspect",
		short: "Print details of the currently held ssoossh certificate(s).",
		run: func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error {
			keys, err := root.ssh.List(true)
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				fmt.Println("no keys signed by your set ca")
				return nil
			}
			for _, k := range keys {
				fmt.Print(string(ssh.MarshalAuthorizedKey(*k)))
			}
			return nil
		},
	}
}
