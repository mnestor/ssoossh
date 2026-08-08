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
		Short: "The ssoossh server — an SSH certificate authority that issues against your OIDC identity provider.",
		Long: "The ssoossh server is the trust anchor and policy decision point for SSH access. " +
			"It authenticates users through OIDC, optionally enriches identity from LDAP, " +
			"and signs SSH public keys into certificates carrying the principals, critical options, " +
			"extensions, and validity window that policy allows. " +
			"Lifetime is derived from issuance context rather than a single global setting, " +
			"and the server configuration is the outer bound on every option a client or user can request. " +
			"A web UI handles approval, shows what was issued and what was trimmed, " +
			"and gives users a history of their own certificates. " +
			"It issues user, host, and service certificates, and never receives a private key.",
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
