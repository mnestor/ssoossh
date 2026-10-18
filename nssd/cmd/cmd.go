// Package cmd is ssoosshd-nss's command tree, built with bep/simplecobra
// as server/cmd builds ssoosshd's: run (the default), cache, override,
// status and lookup. Part 3 of docs/proposals/nss-directory ("Admin
// interface and CLI").
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/nssd/config"
	"github.com/mnestor/ssoossh/nssd/policy"
	"github.com/mnestor/ssoossh/nssd/server"
)

// Environment is what the command tree takes from the host. Tests replace
// the paths and writers; production uses DefaultEnvironment.
type Environment struct {
	// Stdout and Stderr receive output and the log.
	Stdout, Stderr io.Writer
	// NSSwitch is nsswitch.conf.
	NSSwitch string
	// Proc is the proc filesystem.
	Proc string
	// HostFiles are /etc/passwd, /etc/group and /etc/shells. Its
	// Overrides field is ignored: --overrides names that file.
	HostFiles policy.Paths
	// Activation reads the socket-activation environment.
	Activation server.Activation
	// Peers reads peer credentials; nil means server.ProcInspector.
	Peers server.PeerInspector
}

// DefaultEnvironment is the real host.
func DefaultEnvironment() Environment {
	return Environment{
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		NSSwitch:  server.DefaultNSSwitchPath,
		Proc:      "/proc",
		HostFiles: policy.DefaultPaths(),
		Peers:     server.ProcInspector{},
	}
}

// RootCommand is `ssoosshd-nss`. It holds the environment and the global
// flags every subcommand reads, and with no subcommand runs the daemon.
type RootCommand struct {
	env      Environment
	commands []simplecobra.Commander

	// Global flags.
	configPath  string
	adminSocket string
	overrides   string

	// run holds the daemon flags, shared with the run subcommand.
	run runFlags

	// cobraRoot is the assembled cobra command, captured by Init. By the
	// time simplecobra.New returns every subcommand hangs beneath it, which
	// is what lets internal/tools/gendocs write the man pages from the
	// real tree without executing anything.
	cobraRoot *cobra.Command
}

var _ simplecobra.Commander = (*RootCommand)(nil)

// Name implements simplecobra.Commander.
func (r *RootCommand) Name() string { return "ssoosshd-nss" }

// Commands implements simplecobra.Commander.
func (r *RootCommand) Commands() []simplecobra.Commander { return r.commands }

// Init implements simplecobra.Commander.
func (r *RootCommand) Init(cd *simplecobra.Commandeer) error {
	cmd := cd.CobraCommand
	r.cobraRoot = cmd
	cmd.Short = "The ssoossh NSS daemon: directory users and groups for this host through systemd userdb."
	cmd.Long = "ssoosshd-nss answers systemd's io.systemd.UserDatabase varlink interface on " +
		config.UserdbSocket + ", so nss-systemd resolves directory users and groups " +
		"(getpwnam, getgrnam, initgroups) from ssoosshd over pinned HTTPS with a fleet key. " +
		"It caches every answer, serves the cache while the server is unreachable, and never " +
		"serves a name or id that /etc/passwd or /etc/group already has. With no subcommand it runs the daemon."
	cmd.SetOut(r.env.Stdout)
	cmd.SetErr(r.env.Stderr)
	flags := cmd.PersistentFlags()
	flags.StringVarP(&r.configPath, "config", "c", config.DefaultConfigPath, "the daemon's configuration file")
	flags.StringVar(&r.adminSocket, "admin-socket", config.DefaultAdminSocket, "the admin socket the CLI and the daemon share")
	flags.StringVar(&r.overrides, "overrides", config.DefaultOverridesPath, "the host override file")
	r.run.register(cmd)
	return nil
}

// PreRun implements simplecobra.Commander.
func (r *RootCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander: the bare command is `run`.
func (r *RootCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return r.runDaemon(ctx, r.run)
}

// Command is the executable command tree.
type Command struct {
	exec *simplecobra.Exec
	root *RootCommand
}

// NewCommand builds the command tree over env.
func NewCommand(env Environment) (*Command, error) {
	if env.Peers == nil {
		env.Peers = server.ProcInspector{}
	}
	root := &RootCommand{env: env}
	root.commands = []simplecobra.Commander{
		newRunCommand(root),
		newCacheCommand(root),
		newOverrideCommand(root),
		newStatusCommand(root),
		newLookupCommand(root),
	}
	exec, err := simplecobra.New(root)
	if err != nil {
		// not covered: the tree is fixed, and simplecobra fails only on a
		// command whose Init returns an error, which none here do.
		return nil, fmt.Errorf("build the command tree: %w", err)
	}
	return &Command{exec: exec, root: root}, nil
}

// Command returns the root of the cobra tree Execute runs, for the man page
// generator. Building the tree runs every command's Init and nothing else,
// so it reads no configuration and opens no socket.
func (c *Command) Command() *cobra.Command {
	return c.root.cobraRoot
}

// Execute runs the command with args.
func (c *Command) Execute(ctx context.Context, args []string) error {
	_, err := c.exec.Execute(ctx, args)
	return err
}

// Main is the entrypoint: it runs os.Args under a context that SIGINT and
// SIGTERM cancel, prints any error, and returns the exit status.
func Main() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := NewCommand(DefaultEnvironment())
	if err == nil {
		err = c.Execute(ctx, os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "ssoosshd-nss:", err)
		return 1
	}
	return 0
}

// leaf is a subcommand: name, help, flags and a run function, or a group
// of subcommands. root is the tree's root, handed to every constructor, so
// a command reaches the shared flags and environment without a type
// assertion on simplecobra's Commandeer.
type leaf struct {
	root     *RootCommand
	name     string
	use      string
	short    string
	long     string
	args     cobra.PositionalArgs
	flags    func(cmd *cobra.Command)
	run      func(ctx context.Context, root *RootCommand, cmd *cobra.Command, args []string) error
	commands []simplecobra.Commander
}

var _ simplecobra.Commander = (*leaf)(nil)

// Name implements simplecobra.Commander.
func (l *leaf) Name() string { return l.name }

// Commands implements simplecobra.Commander.
func (l *leaf) Commands() []simplecobra.Commander { return l.commands }

// Init implements simplecobra.Commander.
func (l *leaf) Init(cd *simplecobra.Commandeer) error {
	cmd := cd.CobraCommand
	cmd.Short, cmd.Long = l.short, l.long
	if l.use != "" {
		cmd.Use = l.name + " " + l.use
	}
	if l.args != nil {
		cmd.Args = l.args
	}
	if l.flags != nil {
		l.flags(cmd)
	}
	return nil
}

// PreRun implements simplecobra.Commander.
func (l *leaf) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander. A group with no run of its own
// prints its help.
func (l *leaf) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	if l.run == nil {
		return cd.CobraCommand.Help()
	}
	return l.run(ctx, l.root, cd.CobraCommand, args)
}
