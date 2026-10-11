package cmd

import (
	"context"
	"fmt"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"
)

var _ simplecobra.Commander = (*simpleCommand)(nil)

// simpleCommand is a reusable Commander for leaf and group commands,
// mirroring Hugo's commands.simpleCommand (see commands/commandeer.go in
// gohugoio/hugo). Group commands (ssh, host, service) set commands and
// leave run nil; leaf commands set run and leave commands nil.
type simpleCommand struct {
	name  string
	short string
	long  string

	// example is cobra's Example: invocations shown under "Examples:" in
	// --help, indented two spaces, with a comment line above each.
	example string

	// argSpec names the positional arguments in the usage line, in the
	// conventional notation: "<account> <principal>", "<account>
	// [principal]", "<command> [args...]". Leave empty for a command that
	// takes none.
	//
	// simplecobra names every leaf "<name> [flags] [args]", which says a
	// command takes arguments without saying what they are or how many --
	// so `host mapping add --help` documented neither of the two it
	// requires, and the only way to find out was to run it and read the
	// error. Setting this feeds cobra's Use, which is what --help, the
	// generated CLI reference (internal/tools/genclidocs), and the man
	// pages all render.
	argSpec string

	commands []simplecobra.Commander

	// offline marks a command that must complete without contacting the
	// server, so root's PreRun skips the CA fetch and everything built to
	// serve it. See offlineCommander in offline.go.
	offline bool

	// configFree marks a command that reads no configuration setting at
	// all, so a configuration that failed to load is reported as a warning
	// rather than refusing to run. `host principals` needs it because sshd
	// calls it on every login and a refusal there denies certificate logins
	// on the host; `ssh config` because it is what someone reads when the
	// configuration is the broken thing. No admin lock can be bypassed this
	// way: a command that consults no setting has none to enforce.
	configFree bool

	// serverOptional marks a command whose work is local but which may ask
	// the server one question itself, so root's PreRun builds nothing for
	// it either: an unreachable server or a host with no ssh-agent must not
	// stop the local work. See serverOptionalCommander in offline.go.
	serverOptional bool

	run func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error

	// InitFunc optionally overrides default Init behavior for this command.
	// If set, it is invoked instead of the built-in Init implementation.
	init func(cd *simplecobra.Commandeer) error
}

// usageError reports a call that is missing the positional arguments the
// command needs. It is built from the command's own path and argSpec, so
// the message a user gets for calling it wrong and the usage line --help
// prints cannot drift apart -- and neither hard-codes the binary name.
func (c *simpleCommand) usageError(cd *simplecobra.Commandeer) error {
	return &usageError{msg: fmt.Sprintf("usage: %s %s", cd.CobraCommand.CommandPath(), c.argSpec)}
}

// Name implements simplecobra.Commander.
func (c *simpleCommand) Name() string { return c.name }

// Commands implements simplecobra.Commander.
func (c *simpleCommand) Commands() []simplecobra.Commander { return c.commands }

// Offline implements offlineCommander.
func (c *simpleCommand) Offline() bool { return c.offline }

// ServerOptional implements serverOptionalCommander.
func (c *simpleCommand) ServerOptional() bool { return c.serverOptional }

// Init implements simplecobra.Commander.
func (c *simpleCommand) Init(cd *simplecobra.Commandeer) error {

	cmd := cd.CobraCommand
	cmd.Short = c.short
	cmd.Long = c.long
	cmd.Example = c.example
	if c.argSpec != "" {
		// Cobra appends "[flags]" to the usage line itself, so the spec
		// carries only the positional arguments.
		cmd.Use = c.name + " " + c.argSpec
	}
	if len(c.commands) > 0 {
		// Group command (e.g. ssh, host, service): invoked bare it prints
		// help, and given anything else it names the unknown subcommand
		// (see rejectUnknownSubcommand). Neither needs root init, so the
		// PreRun that loads config and fetches the CA is dropped for the
		// group itself; its children keep their own. See usageTemplate for
		// why runnable groups still get a single usage line.
		cmd.Use = c.name + " [command]"
		cmd.Args = rejectUnknownSubcommand
		cmd.PreRunE = nil
		cmd.RunE = func(cmd *cobra.Command, _ []string) error { return cmd.Help() }
	}

	// If the caller provided a custom InitFunc, use it to allow different
	// versions/variants of simpleCommand to customize initialization.
	if c.init != nil {
		return c.init(cd)
	}
	return nil
}

// PreRun implements simplecobra.Commander. Root's PreRun (see cmd.go)
// already handles all shared init; leaf/group commands have nothing of
// their own to do here.
func (c *simpleCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander. It fails closed with the root's
// InitErr (if init failed) before ever reaching run, unless the command is
// configFree, and no-ops for group commands that only exist to hold
// children.
func (c *simpleCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	root, ok := cd.Root.Command.(*RootCommand)
	if !ok {
		return fmt.Errorf("simpleCommand.Run: root command is %T, not *RootCommand", cd.Root.Command)
	}
	if err := root.InitErr(); err != nil {
		if !c.configFree {
			return err
		}
		// stderr, never stdout: sshd parses `host principals` stdout as
		// the principal list.
		fmt.Fprintf(cd.CobraCommand.ErrOrStderr(), "Warning: %v; continuing without the configuration\n", err)
	}
	if c.run == nil {
		return nil
	}
	return c.run(ctx, cd, root, args)
}
