package cmd

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/client/config"
)

// TestRejectUnknownSubcommand pins the message for a subcommand that does
// not exist, at the root and inside a group.
func TestRejectUnknownSubcommand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "should accept a bare invocation",
			args: nil,
			want: "",
		},
		{
			name: "should name the unknown subcommand and suggest the close one",
			args: []string{"lst"},
			want: `unknown command "lst" for "ssoossh mapping"; did you mean "list"?`,
		},
		{
			name: "should name the unknown subcommand without a suggestion when none is close",
			args: []string{"zzzzzz"},
			want: `unknown command "zzzzzz" for "ssoossh mapping"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &cobra.Command{Use: "ssoossh"}
			group := &cobra.Command{Use: "mapping", SuggestionsMinimumDistance: 2}
			group.AddCommand(&cobra.Command{Use: "list", Run: func(*cobra.Command, []string) {}})
			root.AddCommand(group)

			err := rejectUnknownSubcommand(group, tt.args)

			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestExecute_UnknownSubcommands runs the real tree, which is where the
// upstream checks went wrong: a nested typo named the wrong word, and one
// after a global flag exited 0.
func TestExecute_UnknownSubcommands(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "should name the mistyped word in a nested group",
			args: []string{"host", "mapping", "lst"},
			want: `unknown command "lst" for "ssoossh host mapping"; did you mean "list"?`,
		},
		{
			name: "should fail on a typo that follows a global flag",
			args: []string{"-v", "ssh", "loign"},
			want: `unknown command "loign" for "ssoossh ssh"; did you mean "login"?`,
		},
		{
			name: "should name the mistyped word at the root",
			args: []string{"servce"},
			want: `unknown command "servce" for "ssoossh"; did you mean "service"?`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestExec(t)

			_, err := f.exec.Execute(context.Background(), tt.args)

			var usage *usageError
			if !errors.As(unwrapCommandError(err), &usage) || usage.Error() != tt.want {
				t.Errorf("got %v, want usage error %q", err, tt.want)
			}
		})
	}
}

// TestExecute_ShouldPrintHelpWithoutLoadingConfig covers the bare root and
// group invocations: printing help needs no config, CA or agent, and doing
// that work put network traffic and agent warnings in front of the help.
func TestExecute_ShouldPrintHelpWithoutLoadingConfig(t *testing.T) {
	for _, args := range [][]string{nil, {"ssh"}, {"host", "mapping"}, {"-v"}} {
		t.Run("should not load config for "+joinArgs(args), func(t *testing.T) {
			f := newTestExec(t)
			loaded := false
			f.root.newConfig = func(cmd *cobra.Command) (*config.Config, error) {
				loaded = true
				return &config.Config{}, nil
			}
			f.root.cobraRoot.SetOut(&discard{})

			if _, err := f.exec.Execute(context.Background(), args); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if loaded {
				t.Error("expected help to print without loading config")
			}
		})
	}
}

// joinArgs names an argument list in a subtest name.
func joinArgs(args []string) string {
	if len(args) == 0 {
		return "a bare invocation"
	}
	s := args[0]
	for _, a := range args[1:] {
		s += " " + a
	}
	return s
}

// discard is an io.Writer that drops everything, for help output a test
// does not inspect.
type discard struct{}

// Write implements io.Writer.
func (discard) Write(p []byte) (int, error) { return len(p), nil }

func TestUnwrapCommandError(t *testing.T) {
	cause := errors.New("cause")
	tests := []struct {
		name string
		err  error
		want error
	}{
		{name: "should return the error a CommandError wraps", err: &simplecobra.CommandError{Err: cause}, want: cause},
		{name: "should return any other error unchanged", err: cause, want: cause},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := unwrapCommandError(tt.err); !errors.Is(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestReportError pins what reaches the terminal and the exit status for
// each kind of failure.
func TestReportError(t *testing.T) {
	leaf := &cobra.Command{Use: "ca"}
	(&cobra.Command{Use: "ssoossh"}).AddCommand(leaf)
	cd := &simplecobra.Commandeer{CobraCommand: leaf}

	tests := []struct {
		name     string
		cd       *simplecobra.Commandeer
		err      error
		wantOut  string
		wantCode int
	}{
		{
			name:     "should drop the framework prefix and point at help for a cobra error",
			cd:       cd,
			err:      &simplecobra.CommandError{Err: errors.New("unknown flag: --bogus")},
			wantOut:  "Error: unknown flag: --bogus\nRun 'ssoossh ca --help' for usage.\n",
			wantCode: 2,
		},
		{
			name:     "should treat our own usage error as a usage error",
			cd:       cd,
			err:      &usageError{msg: "usage: ssoossh ca"},
			wantOut:  "Error: usage: ssoossh ca\nRun 'ssoossh ca --help' for usage.\n",
			wantCode: 2,
		},
		{
			name:     "should point at the root help when the command is not one of ours",
			cd:       nil,
			err:      &simplecobra.CommandError{Err: &usageError{msg: `unknown help topic "x"`}},
			wantOut:  "Error: unknown help topic \"x\"\nRun 'ssoossh --help' for usage.\n",
			wantCode: 2,
		},
		{
			name:     "should exit 1 without a usage hint for a runtime failure",
			cd:       cd,
			err:      errors.New("cannot reach https://ssh.example.test: connection refused"),
			wantOut:  "Error: cannot reach https://ssh.example.test: connection refused\n",
			wantCode: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			code := reportError(&out, tt.cd, tt.err)
			if out.String() != tt.wantOut {
				t.Errorf("output = %q, want %q", out.String(), tt.wantOut)
			}
			if code != tt.wantCode {
				t.Errorf("exit = %d, want %d", code, tt.wantCode)
			}
		})
	}
}

// TestExecute_HelpCommand covers `ssoossh help`: a real topic prints that
// command's help, and an unknown one is a usage error instead of exit 0.
func TestExecute_HelpCommand(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
		wantOut string
	}{
		{name: "should print the help for a known topic", args: []string{"help", "ssh", "login"}, wantOut: "ssoossh ssh login"},
		{name: "should reject an unknown topic", args: []string{"help", "nonexistent"}, wantErr: `unknown help topic "nonexistent"`},
		{name: "should reject a typo inside a known group", args: []string{"help", "ssh", "loign"}, wantErr: `unknown help topic "ssh loign"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestExec(t)
			var out bytes.Buffer
			f.root.cobraRoot.SetOut(&out)

			_, err := f.exec.Execute(context.Background(), tt.args)

			if tt.wantErr != "" {
				var usage *usageError
				if !errors.As(unwrapCommandError(err), &usage) || usage.Error() != tt.wantErr {
					t.Fatalf("got %v, want usage error %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !bytes.Contains(out.Bytes(), []byte(tt.wantOut)) {
				t.Errorf("help output does not mention %q:\n%s", tt.wantOut, out.String())
			}
		})
	}
}

// TestExecute_FlagValueErrors runs bad flag values through the real tree,
// where the root's FlagErrorFunc rewords pflag's strconv messages.
func TestExecute_FlagValueErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "should say a number flag takes a whole number",
			args: []string{"ssh", "login", "--key-size", "abc"},
			want: `invalid value "abc" for --key-size: expected a whole number`,
		},
		{
			name: "should say a boolean flag takes true or false",
			args: []string{"ssh", "login", "--force=maybe"},
			want: `invalid value "maybe" for --force: expected true or false`,
		},
		{
			name: "should keep pflag's wording for an unknown flag",
			args: []string{"ca", "--bogus"},
			want: "unknown flag: --bogus",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestExec(t)

			_, err := f.exec.Execute(context.Background(), tt.args)

			var usage *usageError
			if !errors.As(unwrapCommandError(err), &usage) || usage.Error() != tt.want {
				t.Errorf("got %v, want usage error %q", err, tt.want)
			}
		})
	}
}

func TestDescribeFlagType(t *testing.T) {
	tests := []struct{ in, want string }{
		{"int", "a whole number"},
		{"count", "a whole number"},
		{"bool", "true or false"},
		{"duration", "a duration such as 30s or 5m"},
		{"stringSlice", "a stringSlice"},
	}
	for _, tt := range tests {
		t.Run("should describe "+tt.in, func(t *testing.T) {
			if got := describeFlagType(tt.in); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
