package cmd

import (
	"context"
	"fmt"

	"github.com/bep/simplecobra"
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
	return fmt.Errorf("usage: %s %s", cd.CobraCommand.CommandPath(), c.argSpec)
}

// Name implements simplecobra.Commander.
func (c *simpleCommand) Name() string { return c.name }

// Commands implements simplecobra.Commander.
func (c *simpleCommand) Commands() []simplecobra.Commander { return c.commands }

// Offline implements offlineCommander.
func (c *simpleCommand) Offline() bool { return c.offline }

// Init implements simplecobra.Commander.
func (c *simpleCommand) Init(cd *simplecobra.Commandeer) error {

	cmd := cd.CobraCommand
	cmd.Short = c.short
	cmd.Long = c.long
	if c.argSpec != "" {
		// Cobra appends "[flags]" to the usage line itself, so the spec
		// carries only the positional arguments.
		cmd.Use = c.name + " " + c.argSpec
	}
	if len(c.commands) > 0 {
		// Group command (e.g. ssh, host, service): let cobra's default
		// "print help" behavior handle being invoked with no subcommand,
		// same as Hugo's listCommand.Init clearing RunE. This also means a
		// bare group invocation doesn't require root init to have
		// succeeded, since Run (and its InitErr check) is never reached.
		cmd.RunE = nil
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
// InitErr (if init failed) before ever reaching run, and no-ops for group
// commands that only exist to hold children.
func (c *simpleCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	root, ok := cd.Root.Command.(*RootCommand)
	if !ok {
		return fmt.Errorf("simpleCommand.Run: root command is %T, not *RootCommand", cd.Root.Command)
	}
	if root.InitErr() != nil {
		return root.InitErr()
	}
	if c.run == nil {
		return nil
	}
	return c.run(ctx, cd, root, args)
}
