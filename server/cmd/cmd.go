// Package cmd wires up the ssoosshd server's cobra command tree using
// bep/simplecobra, mirroring the structure Hugo uses for its own CLI.
package cmd

import (
	"context"
	"log/slog"
	"os"

	"github.com/bep/simplecobra"
	"github.com/italypaleale/go-kit/signals"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/mnestor/ssoossh/server/bootstrap"
)

var _ simplecobra.Commander = (*RootCommand)(nil)

// RootCommand is the root `ssoosshd` command. The bootstrap process runs here.
type RootCommand struct {
	commands []simplecobra.Commander
}

// Name implements simplecobra.Commander.
func (r *RootCommand) Name() string { return "ssoosshd" }

// Commands implements simplecobra.Commander.
func (r *RootCommand) Commands() []simplecobra.Commander { return r.commands }

// Init implements simplecobra.Commander.
func (r *RootCommand) Init(cd *simplecobra.Commandeer) error {
	cmd := cd.CobraCommand
	cmd.Short = "The ssoossh server — an SSH certificate authority that issues against your OIDC identity provider."
	cmd.Long = "The ssoossh server is the trust anchor and policy decision point for SSH access. " +
		"It authenticates users through OIDC, optionally enriches identity from LDAP, " +
		"and signs SSH public keys into certificates carrying the principals, critical options, " +
		"extensions, and validity window that policy allows. " +
		"Lifetime is derived from issuance context rather than a single global setting, " +
		"and the server configuration is the outer bound on every option a client or user can request. " +
		"A web UI handles approval, shows what was issued and what was trimmed, " +
		"and gives users a history of their own certificates. " +
		"It issues user, host, and service certificates, and never receives a private key."

	cmd.PersistentFlags().StringP("config", "c", "", "path to the ssoosshd config file")

	return nil
}

// PreRun implements simplecobra.Commander. This does nothing; all real work
// happens in Run (for the bare command) and in subcommands.
func (r *RootCommand) PreRun(this, runner *simplecobra.Commandeer) error {
	return nil
}

// Run implements simplecobra.Commander. For the root (bare ssoosshd with no
// subcommand), this bootstraps and runs the server until context is canceled.
func (r *RootCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return bootstrap.Bootstrap(cd.CobraCommand)
}

func newExec() (*simplecobra.Exec, error) {
	root := &RootCommand{
		commands: []simplecobra.Commander{
			newVersionCommand(),
		},
	}
	return simplecobra.New(root)
}

// Command wraps the ssoosshd root simplecobra.Exec to maintain backward-compatible
// test interfaces while using simplecobra for the actual command execution.
type Command struct {
	exec *simplecobra.Exec
	args []string
}

// NewCommand builds the ssoosshd root command using simplecobra.
func NewCommand() *Command {
	exec, err := newExec()
	if err != nil {
		slog.Error("failed to initialize command tree", "error", err)
		os.Exit(1)
	}
	return &Command{exec: exec, args: []string{}}
}

// Use returns the command's usage line. For testing and introspection.
func (c *Command) Use() string {
	return "ssoosshd"
}

// PersistentFlags returns the root command's persistent flags. Reconstructs
// the cobra command tree to provide the interface tests expect.
func (c *Command) PersistentFlags() *pflag.FlagSet {
	_, rootCD, _ := c.commandeer()
	return rootCD.CobraCommand.PersistentFlags()
}

// Flags returns the root command's local flags.
func (c *Command) Flags() *pflag.FlagSet {
	_, rootCD, _ := c.commandeer()
	return rootCD.CobraCommand.Flags()
}

// SetArgs sets the arguments to parse.
func (c *Command) SetArgs(args []string) {
	c.args = args
}

// ExecuteContext executes the command with the given context.
func (c *Command) ExecuteContext(ctx context.Context) error {
	_, err := c.exec.Execute(ctx, c.args)
	return err
}

// Execute runs the command with a context that's canceled on shutdown
// signals (e.g. SIGINT/SIGTERM), exiting the process with status 1 on error.
func (c *Command) Execute() {
	ctx := signals.SignalContext(context.Background())

	err := c.ExecuteContext(ctx)
	if err != nil {
		os.Exit(1)
	}
}

// Find locates a subcommand by name. For testing.
func (c *Command) Find(args []string) (*cobra.Command, []string, error) {
	_, rootCD, _ := c.commandeer()
	return rootCD.CobraCommand.Find(args)
}

// Command returns the underlying cobra command. For testing.
func (c *Command) Command() *cobra.Command {
	_, rootCD, _ := c.commandeer()
	return rootCD.CobraCommand
}

// commandeer initializes the command tree and returns the root commandeer.
// This is called lazily the first time an interface method needs it.
func (c *Command) commandeer() (*simplecobra.Exec, *simplecobra.Commandeer, error) {
	// Create a new Exec instance to initialize the command tree.
	// We need a fresh one to get the properly initialized Commandeer.
	exec, err := newExec()
	if err != nil {
		return nil, nil, err
	}
	// Initialize the command tree by executing a help command.
	// This triggers the Init methods without actually running anything.
	rootCD, _ := exec.Execute(context.Background(), []string{"--help"})
	return exec, rootCD, nil
}
