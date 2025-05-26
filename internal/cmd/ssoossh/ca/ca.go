// Created By Mike Nestor <me@mikenestor.org>
package ca

import (
	"github.com/spf13/cobra"

	sc "github.com/mnestor/ssoossh/internal/cmd/ssoossh/ssoossh_context"
	config "github.com/mnestor/ssoossh/internal/config/client"
)

func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ca",
		Short: "Get the CA certificate from the server",
		Long:  "Retrieve the CA certificate from the SSO SSH server. This is used to know which certificates are issued by the server.",
		RunE:  caRun,
	}
}

func caRun(cmd *cobra.Command, args []string) error {
	cfg, ok := cmd.Context().Value(sc.ContextKeyConfig).(config.Config)
	if !ok || len(cfg.CA) == 0 {
		cmd.Printf("Unable to get CA from server: %s\n", cfg.Server)
	} else {
		cmd.Printf("%s\n", cfg.CA)
	}

	return nil
}
