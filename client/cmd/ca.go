package cmd

import (
	"context"
	"fmt"

	"github.com/bep/simplecobra"
)

func newCACommand() simplecobra.Commander {
	return &simpleCommand{
		name:  "ca",
		short: "Retrieve the CA Public Key of the configured ssoossh server.",
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
