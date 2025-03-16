// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
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
	config := getConfig(cmd.Context())
	if len(config.CA) == 0 {
		cmd.Printf("Unable to get CA from server: %s\n", config.Server)
	} else {
		cmd.Printf("%s\n", config.CA)
	}

	return nil
}
