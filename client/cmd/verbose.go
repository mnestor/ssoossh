package cmd

import (
	"io"
	"log/slog"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/internal/tracelog"
)

// verboseFlagName is the repeatable flag, and verboseEnvVar its numeric
// environment equivalent for invocations whose command line is not yours to
// edit — an ssh_config `Match exec` line, a cron entry, a systemd unit.
const (
	verboseFlagName = "verbose"
	verboseEnvVar   = "SSOOSSH_VERBOSE"
)

// maxVerbosity caps the ladder. Three levels is what there is distinct
// content for, and it matches ssh's own -v/-vv/-vvv, which matters for a
// tool people invoke from ssh_config. Extra v's clamp rather than error:
// someone reaching for -vvvvv wants "as much as you have".
const maxVerbosity = 3

// verbosityFor resolves how much tracing was asked for. The flag wins when
// passed; otherwise $SSOOSSH_VERBOSE, which must be a plain count. --debug
// implies level 1, so `--debug` alone gives the configuration report plus
// the high-level steps that explain what it then did.
//
// Junk in the variable reads as zero rather than as an error: a diagnostic
// aid must never be the reason a login fails.
func verbosityFor(cmd *cobra.Command) int {
	level := 0
	if cmd != nil {
		if n, err := cmd.Flags().GetCount(verboseFlagName); err == nil {
			level = n
		}
	}
	if level == 0 {
		if n, err := strconv.Atoi(os.Getenv(verboseEnvVar)); err == nil && n > 0 {
			level = n
		}
	}
	if debugEnabled(cmd) && level < 1 {
		level = 1
	}
	if level > maxVerbosity {
		level = maxVerbosity
	}
	return level
}

// installTracing points slog at out for the rest of the process, at the
// level verbosity asks for.
//
// The client configures no handler otherwise — its handful of slog.Warn and
// slog.Error calls land on the default one — so this is where client-side
// logging gets set up at all. A text handler, not JSON: the audience is a
// person reading their terminal or pasting into a bug report, not a log
// pipeline. Timestamps are dropped for the same reason; they add a column
// of noise to output whose ordering is the informative part.
func installTracing(out io.Writer, verbosity int) {
	slog.SetDefault(slog.New(slog.NewTextHandler(out, &slog.HandlerOptions{
		Level: tracelog.LevelFor(verbosity),
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			if a.Key == slog.LevelKey {
				return slog.Attr{Key: a.Key, Value: slog.StringValue(tracelog.LevelName(a.Value.Any()))}
			}
			return a
		},
	})))
}
