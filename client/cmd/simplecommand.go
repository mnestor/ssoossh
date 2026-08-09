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

	commands []simplecobra.Commander

	run func(ctx context.Context, cd *simplecobra.Commandeer, root *RootCommand, args []string) error

	// InitFunc optionally overrides default Init behavior for this command.
	// If set, it is invoked instead of the built-in Init implementation.
	init func(cd *simplecobra.Commandeer) error
}

// Name implements simplecobra.Commander.
func (c *simpleCommand) Name() string { return c.name }

// Commands implements simplecobra.Commander.
func (c *simpleCommand) Commands() []simplecobra.Commander { return c.commands }

// Init implements simplecobra.Commander.
func (c *simpleCommand) Init(cd *simplecobra.Commandeer) error {

	cmd := cd.CobraCommand
	cmd.Short = c.short
	cmd.Long = c.long
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
