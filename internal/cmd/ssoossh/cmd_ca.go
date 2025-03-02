// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"fmt"

	"github.com/spf13/cobra"
)

var caCmd = &cobra.Command{
	Use:     "ca",
	Short:   "print certificate authority pubkeys",
	Example: "asdf",
	RunE:    caRun,
	PreRunE: preRun,
}

func caRun(cmd *cobra.Command, args []string) error {
	if len(ca) == 0 {
		fmt.Fprintf(outWriter, "Unable to get CA from server: %s\n", config.Server)
	} else {
		fmt.Fprintf(outWriter, "%s\n", ca)
	}

	return nil
}
