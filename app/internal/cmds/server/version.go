package server

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/internal/app/server/config"
	"github.com/mnestor/ssoossh/internal/common"
)

func versionCmd(cmd *cobra.Command) *cobra.Command {
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version number",
		Run: func(cmd *cobra.Command, args []string) {
			cfg := cmd.Context().Value(common.ContextConfig).(*config.Config)
			if cfg.Verbose {
				fmt.Println("ssoossh " + common.Version + " - " + common.BuildDate)
			} else {
				fmt.Println("ssoossh " + common.Version)
			}
		},
	}

	cmd.AddCommand(versionCmd)
	return versionCmd
}
