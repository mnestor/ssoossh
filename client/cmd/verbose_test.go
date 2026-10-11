package cmd

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"

	"github.com/spf13/cobra"
)

// verboseCommand builds a command carrying -v and --debug the way
// RootCommand.Init registers them.
func verboseCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()

	cmd := &cobra.Command{Use: "ssoossh"}
	cmd.PersistentFlags().CountP(verboseFlagName, "v", "")
	cmd.PersistentFlags().Bool(debugFlagName, false, "")
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return cmd
}

// $SSOOSSH_VERBOSE exists for invocations whose command line is not yours to
// edit -- an ssh_config Match exec line, a cron entry -- so it is the route
// that matters most and the one nothing had exercised.
//
// Junk in the variable reads as zero rather than as an error: a diagnostic
// aid must never be the reason a login fails.
func TestVerbosityFor_ShouldResolveTheRequestedLevel(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  string
		want int
	}{
		{name: "nothing asked for", want: 0},
		{name: "one -v", args: []string{"-v"}, want: 1},
		{name: "two -v", args: []string{"-vv"}, want: 2},
		{name: "three -v", args: []string{"-vvv"}, want: 3},
		{name: "more than the maximum clamps", args: []string{"-vvvvv"}, want: maxVerbosity},
		{name: "from the environment", env: "2", want: 2},
		{name: "environment above the maximum clamps", env: "9", want: maxVerbosity},
		{name: "junk in the environment reads as none", env: "loud", want: 0},
		{name: "a negative count in the environment is ignored", env: "-3", want: 0},
		{name: "the flag wins over the environment", args: []string{"-vvv"}, env: "1", want: 3},
		{name: "--debug implies one level", args: []string{"--debug"}, want: 1},
		{name: "--debug does not lower an explicit level", args: []string{"--debug", "-vv"}, want: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(verboseEnvVar, tt.env)
			cmd := verboseCommand(t, tt.args...)

			if got := verbosityFor(cmd); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

// A nil command is what a caller has before the tree is built. The
// environment still has to be honoured there, since that is the only route
// left.
func TestVerbosityFor_ShouldStillReadTheEnvironmentWithNoCommand(t *testing.T) {
	t.Setenv(verboseEnvVar, "2")

	if got := verbosityFor(nil); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

// TestNoticeHandler pins how a warning or error reads at the default
// verbosity: one line, like the "Error:" a failed command prints, not
// logfmt.
func TestNoticeHandler(t *testing.T) {
	tests := []struct {
		name string
		log  func(l *slog.Logger)
		want string
	}{
		{
			name: "should prefix a warning and list its attributes",
			log:  func(l *slog.Logger) { l.Warn("the server's CA key set was replaced", "path", "/tmp/ca.json") },
			want: "Warning: the server's CA key set was replaced (path: /tmp/ca.json)\n",
		},
		{
			name: "should prefix an error",
			log:  func(l *slog.Logger) { l.Error("the principals map could not be loaded") },
			want: "Error: the principals map could not be loaded\n",
		},
		{
			name: "should include attributes added with With",
			log:  func(l *slog.Logger) { l.With("account", "deploy").Warn("odd", "n", 2) },
			want: "Warning: odd (account: deploy, n: 2)\n",
		},
		{
			name: "should drop anything below a warning",
			log:  func(l *slog.Logger) { l.Info("starting") },
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			tt.log(slog.New(&noticeHandler{out: &out, mu: &sync.Mutex{}}))
			if out.String() != tt.want {
				t.Errorf("got %q, want %q", out.String(), tt.want)
			}
		})
	}
}
