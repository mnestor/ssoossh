// Created By Mike Nestor <me@mikenestor.org>
package ssh

import (
	"fmt"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Short:   "list certificate authority pubkeys",
	RunE:    listRun,
	PreRunE: preRun,
}

func listRun(cmd *cobra.Command, args []string) error {
	fmt.Fprintf(outWriter, "There are %d keys in use by the server\n", len(ca))
	for _, c := range ca {
		fmt.Fprintf(outWriter, "\tCA: %s", c)
	}

	return nil
}
