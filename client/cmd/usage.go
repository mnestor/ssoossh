package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"
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
