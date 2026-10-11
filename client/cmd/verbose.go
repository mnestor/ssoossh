package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

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
//
// At the default verbosity only warnings and errors are emitted, and they
// are for whoever is running the command rather than for a bug report, so
// they go through noticeHandler and read like the "Error:" line instead of
// as logfmt ("level=WARN msg=...").
func installTracing(out io.Writer, verbosity int) {
	if verbosity <= 0 {
		slog.SetDefault(slog.New(&noticeHandler{out: out, mu: &sync.Mutex{}}))
		return
	}
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

// noticeHandler writes warnings and errors as single lines for a person:
// "Warning: <message> (key: value, ...)". It drops everything below Warn,
// which is what the default verbosity asked for anyway.
type noticeHandler struct {
	out   io.Writer
	mu    *sync.Mutex
	attrs []slog.Attr
}

// Enabled implements slog.Handler.
func (h *noticeHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelWarn
}

// Handle implements slog.Handler.
func (h *noticeHandler) Handle(_ context.Context, r slog.Record) error {
	prefix := "Warning: "
	if r.Level >= slog.LevelError {
		prefix = "Error: "
	}
	details := make([]string, 0, len(h.attrs)+r.NumAttrs())
	for _, a := range h.attrs {
		details = append(details, fmt.Sprintf("%s: %v", a.Key, a.Value.Resolve()))
	}
	r.Attrs(func(a slog.Attr) bool {
		details = append(details, fmt.Sprintf("%s: %v", a.Key, a.Value.Resolve()))
		return true
	})

	line := prefix + r.Message
	if len(details) > 0 {
		line += " (" + strings.Join(details, ", ") + ")"
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.out, line+"\n")
	return err
}

// WithAttrs implements slog.Handler.
func (h *noticeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &noticeHandler{out: h.out, mu: h.mu, attrs: append(slices.Clone(h.attrs), attrs...)}
}

// WithGroup implements slog.Handler. The client logs no groups; a group's
// attributes are printed under their own keys.
func (h *noticeHandler) WithGroup(string) slog.Handler { return h }
