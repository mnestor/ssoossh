package server

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/internal/app/server/config"
	bootstrap "github.com/mnestor/ssoossh/internal/bootstrap/server"
	"github.com/mnestor/ssoossh/internal/common"
	"github.com/mnestor/ssoossh/internal/utils/signals"
)

func init() {
	// I would love it if we didn't need to do this globally
	cobra.EnableTraverseRunHooks = true
}

func NewRootCommand(
	ctx context.Context,
	o io.Writer,
	e io.Writer,
	args []string,
) *cobra.Command {
	var rootCmd = &cobra.Command{
		Use:   "ssoossh",
		Short: "An ssh certificate provider allowing sso for ssh and pam(sudo) operations.",
		Long:  "By default, this command starts the ssoossh server.",
		Run: func(cmd *cobra.Command, args []string) {
			// Start the server
			err := bootstrap.Bootstrap(cmd)
			if err != nil {
				slog.Error("Failed to run ssoossh", "error", err)
				os.Exit(1)
			}
		},
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// Load configuration into context for some messaging in help pages
			cfg := config.InitConfig(cmd)
			ctx := context.WithValue(cmd.Context(), common.ContextConfig, cfg)
			cmd.SetContext(ctx)
		},
	}

	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Enable verbose mode")

	versionCmd(rootCmd)
	healthcheckCmd(rootCmd)

	return rootCmd
}

func Execute() {
	o := os.Stdout
	e := os.Stderr
	args := os.Args
	// Get a context that is canceled when the application is stopping
	ctx := signals.SignalContext(context.Background())

	rootCmd := NewRootCommand(ctx, o, e, args)
	err := rootCmd.ExecuteContext(ctx)
	if err != nil {
		os.Exit(1)
	}
}
