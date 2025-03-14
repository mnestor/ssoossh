// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"fmt"
	"log"

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
	if len(ca) == 0 {
		log.Println("getting context caRun")
		config := cmd.Context().Value(CONFIG_CTX).(Config)
		fmt.Fprintf(outWriter, "Unable to get CA from server: %s\n", config.Server)
	} else {
		fmt.Fprintf(outWriter, "%s\n", ca)
	}

	return nil
}
