package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// rejectUnknownSubcommand is the Args validator for the root and every
// group command. Invoked bare they print help; given anything else, the
// first argument is a subcommand that does not exist.
//
// It replaces two upstream behaviours that each got this wrong: cobra
// checks unknown subcommands only at the root, and simplecobra's
// after-the-fact check names the wrong argument ("unknown command
// \"mapping\"" for `host mapping lst`) and stops at the first flag, so
// `ssoossh -v ssh loign` printed help and exited 0.
func rejectUnknownSubcommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return unknownCommandError(cmd, args[0])
}

// unknownCommandError reports name as an unknown subcommand of cmd, with
// cobra's own suggestions for what was probably meant.
func unknownCommandError(cmd *cobra.Command, name string) error {
	msg := fmt.Sprintf("unknown command %q for %q", name, cmd.CommandPath())
	if suggestions := cmd.SuggestionsFor(name); len(suggestions) > 0 {
		quoted := make([]string, len(suggestions))
		for i, s := range suggestions {
			quoted[i] = fmt.Sprintf("%q", s)
		}
		msg += "; did you mean " + strings.Join(quoted, " or ") + "?"
	}
	return &usageError{msg: msg}
}

// usageError is an error in how a command was invoked, as opposed to a
// failure while running it.
type usageError struct {
	msg string
}

// Error implements error.
func (e *usageError) Error() string { return e.msg }

// unwrapCommandError returns the error simplecobra wrapped in a
// CommandError. CommandError has no Unwrap method, so errors.As cannot see
// through it on its own.
func unwrapCommandError(err error) error {
	var cmdErr *simplecobra.CommandError
	if errors.As(err, &cmdErr) {
		return cmdErr.Err
	}
	return err
}

// usageTemplate is cobra's default usage template with one change: a
// command prints either its own usage line (when runnable) or the
// "<path> [command]" line (when it only groups subcommands), never both.
// The root and the groups are runnable so that rejectUnknownSubcommand runs
// for them, and their Use already says "[command]", so the stock template
// printed the same line twice.
const usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{else if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`

// reportError prints err for the person who ran the command and returns the
// exit status: 2 for a mistake in how the command was invoked, 1 for a
// failure while running it. Scripts can tell the two apart, and a usage
// mistake ends with where to read the usage.
//
// simplecobra wraps every error cobra raises itself (unknown flag, bad flag
// value, unknown command) in a CommandError whose message starts "command
// error:". That prefix is dropped: it names the framework, not the problem.
func reportError(w io.Writer, cd *simplecobra.Commandeer, err error) int {
	var cmdErr *simplecobra.CommandError
	isUsage := errors.As(err, &cmdErr)
	err = unwrapCommandError(err)

	var usage *usageError
	if errors.As(err, &usage) {
		isUsage = true
	}

	fmt.Fprintln(w, "Error:", err)
	if !isUsage {
		return 1
	}

	// cd is nil when the invoked command is not one of ours: cobra's own
	// completion command, or the help command below.
	path := "ssoossh"
	if cd != nil {
		path = cd.CobraCommand.CommandPath()
	}
	fmt.Fprintf(w, "Run '%s --help' for usage.\n", path)
	return 2
}

// newHelpCommand replaces cobra's help command, which answered `ssoossh
// help nonexistent` with "Unknown help topic", the root usage, and exit 0.
// An unknown topic is now a usage error like an unknown command.
func newHelpCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Long:  "Prints the help for any command. Type ssoossh help followed by the command's path, e.g. ssoossh help ssh login.",
		ValidArgsFunction: func(c *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			cmd, _, err := c.Root().Find(args)
			if err != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			var completions []cobra.Completion
			for _, sub := range cmd.Commands() {
				if (sub.IsAvailableCommand() || sub.Name() == "help") && strings.HasPrefix(sub.Name(), toComplete) {
					completions = append(completions, cobra.CompletionWithDesc(sub.Name(), sub.Short))
				}
			}
			return completions, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(c *cobra.Command, args []string) error {
			cmd, rest, err := c.Root().Find(args)
			if err != nil || cmd == nil || len(rest) > 0 {
				return &usageError{msg: fmt.Sprintf("unknown help topic %q", strings.Join(args, " "))}
			}
			cmd.InitDefaultHelpFlag()
			return cmd.Help()
		},
	}
}

// describeFlagError is the root's FlagErrorFunc, which every subcommand
// inherits. A flag given a value of the wrong kind reached the terminal in
// strconv's words ("strconv.ParseInt: parsing \"abc\": invalid syntax");
// this says what the flag takes instead. Every flag error is a usage error.
func describeFlagError(_ *cobra.Command, err error) error {
	var invalid *pflag.InvalidValueError
	if errors.As(err, &invalid) {
		return &usageError{msg: fmt.Sprintf("invalid value %q for --%s: expected %s",
			invalid.GetValue(), invalid.GetFlag().Name, describeFlagType(invalid.GetFlag().Value.Type()))}
	}
	return &usageError{msg: err.Error()}
}

// describeFlagType names what a flag of pflag type t accepts.
func describeFlagType(t string) string {
	switch t {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "count":
		return "a whole number"
	case "bool":
		return "true or false"
	case "duration":
		return "a duration such as 30s or 5m"
	default:
		return "a " + t
	}
}
