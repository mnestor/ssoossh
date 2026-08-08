// Package cmd defines the ssoosshd root Cobra command.
package cmd

import (
	"context"
	"log/slog"
	"os"

	"github.com/italypaleale/go-kit/signals"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/server/bootstrap"
)

// Command wraps the ssoosshd root cobra.Command.
type Command struct {
	cobra.Command
}

// NewCommand builds the ssoosshd root command, which bootstraps and runs
// the server when executed.
func NewCommand() *Command {
	cc := cobra.Command{
		Use:   "ssoosshd",
		Short: "Single sign-on for ssh authenticator.",
		Long:  "Webservice to provide authentication and generation of ssh certificates.",
		Run: func(cmd *cobra.Command, args []string) {
			// Start the server
			err := bootstrap.Bootstrap(cmd)
			if err != nil {
				slog.Error("Failed to run ssoosshd", "error", err)
				os.Exit(1)
			}
		},
	}

	cc.PersistentFlags().StringP("config", "c", "", "path to the ssoosshd config file")

	return &Command{cc}
}

// Execute runs the command with a context that's canceled on shutdown
// signals (e.g. SIGINT/SIGTERM), exiting the process with status 1 on error.
func (c *Command) Execute() {
	// Get a context that is canceled when the application is stopping
	ctx := signals.SignalContext(context.Background())

	err := c.ExecuteContext(ctx)
	if err != nil {
		os.Exit(1)
	}
}
