package cmd

import (
	"context"

	"github.com/bep/simplecobra"

	"github.com/mnestor/ssoossh/server/bootstrap"
)

var _ simplecobra.Commander = (*serveCommand)(nil)

// serveCommand is the parent for "ssoosshd serve" with modes (full, api).
type serveCommand struct {
	commands []simplecobra.Commander
}

func newServeCommand() simplecobra.Commander {
	return &serveCommand{
		commands: []simplecobra.Commander{
			newServeFullCommand(),
			newServeAPICommand(),
		},
	}
}

// Name implements simplecobra.Commander.
func (c *serveCommand) Name() string { return "serve" }

// Commands implements simplecobra.Commander.
func (c *serveCommand) Commands() []simplecobra.Commander { return c.commands }

// Init implements simplecobra.Commander.
func (c *serveCommand) Init(cd *simplecobra.Commandeer) error {
	cd.CobraCommand.Short = "Start the ssoosshd server in the specified mode."
	cd.CobraCommand.Long = "Start the ssoosshd server.\n\n" +
		"Available modes:\n" +
		"  full - webserver, listener, and in-process signer (default for bare 'serve')\n" +
		"  api  - webserver and listener only; requires separate signer process"
	return nil
}

// PreRun implements simplecobra.Commander.
func (c *serveCommand) PreRun(this, runner *simplecobra.Commandeer) error {
	return nil
}

// Run implements simplecobra.Commander. Bare "serve" defaults to full mode.
func (c *serveCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	// Bare "serve" with no subcommand runs full mode for backward compatibility
	return bootstrap.BootstrapServe(cd.CobraCommand, bootstrap.ServerModeFull)
}

var _ simplecobra.Commander = (*serveFullCommand)(nil)

// serveFullCommand runs the full mode: webserver + listener + in-process signer.
type serveFullCommand struct{}

func newServeFullCommand() simplecobra.Commander { return &serveFullCommand{} }

// Name implements simplecobra.Commander.
func (c *serveFullCommand) Name() string { return "full" }

// Commands implements simplecobra.Commander.
func (c *serveFullCommand) Commands() []simplecobra.Commander { return nil }

// Init implements simplecobra.Commander.
func (c *serveFullCommand) Init(cd *simplecobra.Commandeer) error {
	cd.CobraCommand.Short = "Run the server with webserver, listener, and in-process signer."
	cd.CobraCommand.Long = "Full mode (default) runs all components in a single process: " +
		"the HTTP server, the listener/resolver for certificate signatures, and the signer. " +
		"This is suitable for single-instance deployments."
	return nil
}

// PreRun implements simplecobra.Commander.
func (c *serveFullCommand) PreRun(this, runner *simplecobra.Commandeer) error {
	return nil
}

// Run implements simplecobra.Commander.
func (c *serveFullCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return bootstrap.BootstrapServe(cd.CobraCommand, bootstrap.ServerModeFull)
}

var _ simplecobra.Commander = (*serveAPICommand)(nil)

// serveAPICommand runs API-only mode: webserver + listener, no signer.
type serveAPICommand struct{}

func newServeAPICommand() simplecobra.Commander { return &serveAPICommand{} }

// Name implements simplecobra.Commander.
func (c *serveAPICommand) Name() string { return "api" }

// Commands implements simplecobra.Commander.
func (c *serveAPICommand) Commands() []simplecobra.Commander { return nil }

// Init implements simplecobra.Commander.
func (c *serveAPICommand) Init(cd *simplecobra.Commandeer) error {
	cd.CobraCommand.Short = "Run the server without the signer (API mode)."
	cd.CobraCommand.Long = "API mode runs the HTTP server and listener/resolver but not the signer. " +
		"This mode requires a separate signer process and a shared message broker (NATS). " +
		"Suitable for multi-instance deployments where the signer is isolated."
	return nil
}

// PreRun implements simplecobra.Commander.
func (c *serveAPICommand) PreRun(this, runner *simplecobra.Commandeer) error {
	return nil
}

// Run implements simplecobra.Commander.
func (c *serveAPICommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return bootstrap.BootstrapServe(cd.CobraCommand, bootstrap.ServerModeAPI)
}
