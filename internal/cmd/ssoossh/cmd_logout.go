/*
Created by - 2022 Mike Nestor <mnestor@nasa.gov>
*/
package ssoossh

import (
	"github.com/spf13/cobra"
)

func newLogoutCmd() *cobra.Command {
	// certificateCmd represents the certificate command
	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Removes ssh keypair and certificate from ssh-agent",
		Long: `Manual cleanup of ssh-agent. Removes only certificates and
ssh keypairs generated with this utility.`,
		RunE:    logoutRun,
		PreRunE: preRun,
	}
	return logoutCmd
}
func logoutRun(cmd *cobra.Command, args []string) error {
	return agent.CleanupAgent()
}
