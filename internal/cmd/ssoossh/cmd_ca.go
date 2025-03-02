// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"fmt"

	"github.com/spf13/cobra"
)

var caCmd = &cobra.Command{
	Use:     "ca",
	Short:   "print certificate authority pubkeys",
	RunE:    caRun,
	PreRunE: preRun,
}

func caRun(cmd *cobra.Command, args []string) error {
	fmt.Fprintf(outWriter, "There are %d keys in use by the server\n", len(ca))
	for _, c := range ca {
		fmt.Fprintf(outWriter, "\tCA: %s", c)
	}

	return nil
}
