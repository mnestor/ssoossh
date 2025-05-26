/*
Created by - 2022 Mike Nestor <mnestor@nasa.gov>
*/
package ssh

import (
	sc "github.com/mnestor/ssoossh/internal/cmd/ssoossh/ssoossh_context"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"github.com/spf13/cobra"
)

func newLogoutCmd() *cobra.Command {
	// certificateCmd represents the certificate command
	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Removes ssh keypair and certificate from ssh-agent or files",
		Long: `Manual cleanup of ssh-agent or files. Removes only certificates and
ssh keypairs generated with this utility.`,
		RunE: logoutRun,
	}
	return logoutCmd
}

func logoutRun(cmd *cobra.Command, args []string) error {
	ag := cmd.Context().Value(sc.ContextKeyAgent).(agent.Agent)
	count, err := ag.RemoveAll()
	if count == 0 {
		msg := "No keys or certificates found in %s to remove.\n"
		cmd.PrintErrf(msg, ag.Type())
		return nil
	}
	if err != nil {
		return err
	}
	msg := "Removed %d keys and certificates from %s.\n"
	cmd.Printf(msg, count, ag.Type())
	return err
}
