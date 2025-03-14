// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCaCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "ca",
		Short:   "print certificate authority pubkeys",
		RunE:    caRun,
		PreRunE: preRun,
	}
}

func caRun(cmd *cobra.Command, args []string) error {
	config := cmd.Context().Value(CONFIG_CTX).(*Config)
	if len(config.CA) == 0 {
		fmt.Fprintf(outWriter, "Unable to get CA from server: %s\n", config.Server)
	} else {
		fmt.Fprintf(outWriter, "%s\n", config.CA)
	}

	return nil
}
