package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/client/config"
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

	// requiredFlags are flags the command cannot run without. checkArity
	// reports them missing before PreRun, so a call missing --code does
	// not load config and fetch the CA first; cobra's own required-flag
	// check runs only after PreRun.
	requiredFlags []string

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
	// way: a command that consults no setting has none to enforce. A file
	// named with --config that is missing or broken still fails the command
	// (config.ExplicitConfigError): that is a typo, not a broken host.
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

// checkArity is a leaf command's cobra Args validator, derived from argSpec
// so the usage line and the check cannot disagree. Extra arguments used to
// be ignored silently: `ssoossh ca extra` made a request, `host principals
// alice bob` answered for alice, and `ssh inspect garbage.txt` ignored the
// file. Cobra runs it before PreRun, so a mistyped call loads nothing.
func (c *simpleCommand) checkArity(cmd *cobra.Command, args []string) error {
	var missing []string
	for _, name := range c.requiredFlags {
		if !cmd.Flags().Changed(name) {
			missing = append(missing, "--"+name)
		}
	}
	switch len(missing) {
	case 0:
	case 1:
		return &usageError{msg: "missing required flag " + missing[0]}
	default:
		return &usageError{msg: "missing required flags " + strings.Join(missing[:len(missing)-1], ", ") + " and " + missing[len(missing)-1]}
	}

	minArgs, maxArgs := arity(c.argSpec)
	switch {
	case c.argSpec == "" && len(args) > 0:
		return &usageError{msg: fmt.Sprintf("%s takes no arguments, got %q", cmd.CommandPath(), args[0])}
	case len(args) < minArgs:
		missing := strings.Fields(c.argSpec)[len(args)]
		return &usageError{msg: fmt.Sprintf("missing %s; usage: %s %s", missing, cmd.CommandPath(), c.argSpec)}
	case maxArgs >= 0 && len(args) > maxArgs:
		return &usageError{msg: fmt.Sprintf("unexpected argument %q; usage: %s %s", args[maxArgs], cmd.CommandPath(), c.argSpec)}
	}
	return nil
}

// arity reads an argSpec: each <name> is required, each [name] optional,
// and a name ending in "..." takes any number (maxArgs -1).
func arity(spec string) (minArgs, maxArgs int) {
	variadic := false
	for _, token := range strings.Fields(spec) {
		if strings.HasPrefix(token, "<") {
			minArgs++
		}
		if strings.HasSuffix(strings.TrimRight(token, ">]"), "...") {
			variadic = true
			continue
		}
		maxArgs++
	}
	if variadic {
		maxArgs = -1
	}
	return minArgs, maxArgs
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
	if len(c.commands) == 0 {
		// Cobra appends "[flags]" to the usage line itself, so Use carries
		// only the positional arguments, and none at all for a command
		// that takes none (simplecobra's default "[flags] [args]" claimed
		// arguments that every such command ignored).
		cmd.Use = strings.TrimSpace(c.name + " " + c.argSpec)
		cmd.Args = c.checkArity
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
		if err := c.init(cd); err != nil {
			return err
		}
	}
	// Annotated for cobra too, so shell completion offers them first.
	for _, name := range c.requiredFlags {
		if err := cmd.MarkFlagRequired(name); err != nil {
			return fmt.Errorf("mark --%s required: %w", name, err)
		}
	}
	return nil
}

// PreRun implements simplecobra.Commander. Root's PreRun (see cmd.go)
// already handles all shared init; leaf/group commands have nothing of
// their own to do here.
func (c *simpleCommand) PreRun(this, runner *simplecobra.Commandeer) error { return nil }

// Run implements simplecobra.Commander. It fails closed with the root's
// InitErr (if init failed) before ever reaching run, unless the command is
// configFree and the failure is not a --config file the person named, and
// no-ops for group commands that only exist to hold
// children.
func (c *simpleCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	root, ok := cd.Root.Command.(*RootCommand)
	if !ok {
		return fmt.Errorf("simpleCommand.Run: root command is %T, not *RootCommand", cd.Root.Command)
	}
	if err := root.InitErr(); err != nil {
		var explicit *config.ExplicitConfigError
		if !c.configFree || errors.As(err, &explicit) {
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
