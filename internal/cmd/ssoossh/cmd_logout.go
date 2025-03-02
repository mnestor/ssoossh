/*
Created by - 2022 Mike Nestor <mnestor@nasa.gov>
*/
package ssoossh

import (
	"github.com/spf13/cobra"
)

// certificateCmd represents the certificate command
var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Removes ssh keypair and certificate from ssh-agent",
	Long: `Manual cleanup of ssh-agent. Removes only certificates and
ssh keypairs generated with this utility.`,
	RunE:    logoutRun,
	PreRunE: preRun,
}

func logoutRun(cmd *cobra.Command, args []string) error {
	return agent.CleanupAgent()
}
