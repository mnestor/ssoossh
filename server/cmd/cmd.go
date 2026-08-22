// Package cmd wires up the ssoosshd server's cobra command tree using
// bep/simplecobra, mirroring the structure Hugo uses for its own CLI.
package cmd

import (
	"context"
	"log/slog"
	"os"
	"sync"

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

	// cobraCmd is populated during Init and accessed by tests to get the
	// underlying cobra.Command without executing anything.
	cobraCmd *cobra.Command
	mu       sync.Mutex
}

// Name implements simplecobra.Commander.
func (r *RootCommand) Name() string { return "ssoosshd" }

// Commands implements simplecobra.Commander.
func (r *RootCommand) Commands() []simplecobra.Commander { return r.commands }

// Init implements simplecobra.Commander.
func (r *RootCommand) Init(cd *simplecobra.Commandeer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cobraCmd = cd.CobraCommand

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

// CobraCommand returns the underlying cobra.Command. Only available after Init
// has been called. For testing and introspection.
func (r *RootCommand) CobraCommand() *cobra.Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cobraCmd
}

// Command wraps the ssoosshd root simplecobra.Exec to maintain backward-compatible
// test interfaces while using simplecobra for the actual command execution.
type Command struct {
	exec *simplecobra.Exec
	root *RootCommand
	args []string
}

// NewCommand builds the ssoosshd root command using simplecobra.
func NewCommand() *Command {
	root := &RootCommand{
		commands: []simplecobra.Commander{
			newVersionCommand(),
		},
	}

	exec, err := simplecobra.New(root)
	if err != nil {
		slog.Error("failed to initialize command tree", "error", err)
		os.Exit(1)
	}

	return &Command{exec: exec, root: root, args: nil}
}

// Use returns the command's usage line. For testing and introspection.
func (c *Command) Use() string {
	return "ssoosshd"
}

// PersistentFlags returns the root command's persistent flags.
func (c *Command) PersistentFlags() *pflag.FlagSet {
	cmd := c.root.CobraCommand()
	if cmd == nil {
		return nil
	}
	return cmd.PersistentFlags()
}

// Flags returns the root command's local flags.
func (c *Command) Flags() *pflag.FlagSet {
	cmd := c.root.CobraCommand()
	if cmd == nil {
		return nil
	}
	return cmd.Flags()
}

// SetArgs sets the arguments to parse. Used by tests. This sets both the
// internal args for ExecuteContext and the cobra.Command's args.
func (c *Command) SetArgs(args []string) {
	c.args = args
	cmd := c.root.CobraCommand()
	if cmd != nil {
		cmd.SetArgs(args)
	}
}

// ExecuteContext executes the command with the given context and the args
// previously set via SetArgs, or os.Args[1:] if none were set.
func (c *Command) ExecuteContext(ctx context.Context) error {
	args := c.args
	if args == nil {
		args = os.Args[1:]
	}
	_, err := c.exec.Execute(ctx, args)
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
	cmd := c.root.CobraCommand()
	if cmd == nil {
		return nil, nil, nil
	}
	return cmd.Find(args)
}

// Command returns the underlying cobra command. For testing.
func (c *Command) Command() *cobra.Command {
	return c.root.CobraCommand()
}
