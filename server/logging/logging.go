// Package logging sets up logging based on config.Logging,
// config.HTTP.AccessLogging, config.DB.Logging, and config.QueueLogging —
// and any other "type"-tagged named logger added to New's namedLoggers
// list.
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
	slogmulti "github.com/samber/slog-multi"

	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/server/config"
)

// logDestination returns an io.Writer for the given filename: io.Discard if the
// filename is /dev/null, otherwise the provided writer (e.g. &config.AccessLogging
// or &config.DB.Logging).
func logDestination(filename string, w io.Writer) io.Writer {
	if filename == "/dev/null" {
		return io.Discard
	}
	return w
}

// LevelFromString converts a config-provided log level into an slog.Level.
// It accepts a numeric string (e.g. "-4", "0", "4", "8") or a level name
// understood by slog.Level.UnmarshalText (e.g. "debug", "info", "warn",
// "error", case-insensitive, optionally with a "+n"/"-n" offset). Unrecognized
// or empty values fall back to slog.LevelInfo.
func LevelFromString(level string) slog.Level {
	if n, err := strconv.Atoi(level); err == nil {
		return slog.Level(n)
	}

	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err == nil {
		return l
	}

	return slog.LevelInfo
}

// terminalDetector reports whether log output should be treated as an
// interactive terminal (colorized tint output) rather than plain/JSON.
// ISTERMINAL=1 overrides real isatty detection — useful in development
// when stderr isn't recognized as a real TTY (some IDE-integrated
// terminals, running under a debugger frontend, piping through a pager)
// but colorized output is still wanted. Called once per New, with the
// result passed explicitly to every GetHandler call so every handler
// built in that call agrees on the same answer.
func terminalDetector() bool {
	return isatty.IsTerminal(os.Stderr.Fd()) || os.Getenv("ISTERMINAL") == "1"
}

// dropAttr returns an slog.HandlerOptions.ReplaceAttr function that omits
// the top-level attribute named key. Used so a record routed to its own
// dedicated log (e.g. type=db routed to the db log file) doesn't also
// print that now-redundant "type" attribute in its own destination — you
// already know what it is from which file it's in. Handlers that don't use
// this (the general fallback handler(s) in New) keep "type" visible, since
// that's exactly where it's needed: anything without its own dedicated
// destination has no other way to be identified in the merged stream.
func dropAttr(key string) func(groups []string, a slog.Attr) slog.Attr {
	return func(groups []string, a slog.Attr) slog.Attr {
		if len(groups) == 0 && a.Key == key {
			return slog.Attr{}
		}
		return a
	}
}

// GetHandler builds an slog.Handler writing to w: JSON if json is true,
// otherwise colorized text (via tint) when isTerminal, or plain text
// otherwise. isTerminal is the caller's responsibility to determine (see
// terminalDetector) rather than detected internally.
func GetHandler(json bool, isTerminal bool, w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	switch {
	case json:
		return slog.NewJSONHandler(w, opts)
	case isTerminal:
		return tint.NewTextHandler(w, &tint.Options{
			NoColor:     !isTerminal,
			Level:       opts.Level,
			AddSource:   opts.AddSource,
			ReplaceAttr: opts.ReplaceAttr,
			TimeFormat:  time.Stamp,
		})
	default:
		return slog.NewTextHandler(w, opts)
	}
}

// loggerSource is what New needs from a named logger's config to build its
// handler — implemented directly on *config.GenericLogging and
// *config.AccessLogging (see their LogFilename/LogJSONEnabled/
// LogLevelString/LogAddSource methods), so a named logger's destination is
// always the real config struct itself (via io.Writer, its embedded
// timberjack.Logger's Write) — never a copy or a restatement of its
// fields in a parallel struct here.
type loggerSource interface {
	io.Writer
	LogFilename() string
	LogJSONEnabled() bool
	LogLevelString() string
	LogAddSource() bool
}

// namedLoggerConfig is one "type"-tagged sub-logger: records carrying a
// matching "type" attribute are routed to their own handler/destination
// (with "type" itself stripped from the output there — see dropAttr)
// instead of the general log. Add an entry to New's namedLoggers slice for
// each new named logger (e.g. a future "signer" or "listener" component —
// see docs/watermill-signer-plan.md) instead of writing another bespoke
// routing block.
type namedLoggerConfig struct {
	tag string
	src loggerSource
}

// newNamedHandler builds cfg's handler wired for its own dedicated
// destination, or nil if cfg.src has no filename configured (no dedicated
// logger for this tag, so records with this tag fall through to the
// general handler like any other).
func newNamedHandler(cfg namedLoggerConfig) slog.Handler {
	filename := cfg.src.LogFilename()
	if filename == "" {
		return nil
	}
	return GetHandler(cfg.src.LogJSONEnabled(), false, logDestination(filename, cfg.src), &slog.HandlerOptions{
		AddSource:   cfg.src.LogAddSource(),
		Level:       LevelFromString(cfg.src.LogLevelString()),
		ReplaceAttr: dropAttr("type"),
	})
}

// New builds an slog logger from c.Logging plus every named logger listed
// below, installs it as the default via slog.SetDefault, and returns a
// close function for every configured rotating log file (c.Logging,
// c.HTTP.AccessLogging, c.DB.Logging, c.QueueLogging all embed
// timberjack.Logger) — the caller must run these on shutdown so rotation
// goroutines and file handles are released cleanly; each is safe to call
// even if that logger was never configured with a filename (timberjack.Logger.Close
// is a no-op when nothing was ever opened).
//
// Named-logger records (identified by a "type" attribute) are routed to
// their own configured destination when a filename is set; everything else
// — no dedicated destination configured, or no "type" attribute at all —
// goes to the general destination(s) (main log file and/or stdout,
// depending on config) with "type" left intact.
func New(c *config.Config) (closeFns []func(context.Context) error, err error) {
	isTerminal := terminalDetector()
	router := slogmulti.Router()

	namedLoggers := []namedLoggerConfig{
		{tag: "accesslog", src: &c.HTTP.AccessLogging},
		{tag: "db", src: &c.DB.Logging},
		{tag: "queue", src: &c.QueueLogging},
	}
	for _, nl := range namedLoggers {
		if h := newNamedHandler(nl); h != nil {
			router = router.Add(h, slogmulti.AttrValueIs("type", nl.tag))
		}
	}

	// always send errors to stderr if not a terminal
	// will get these anyway below if a terminal
	// I think this is good for use in containers?
	if !isTerminal {
		h := GetHandler(c.Logging.LogJSON, isTerminal, os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelError,
		})
		router = router.Add(h)
	}

	{ // group these together
		var fanout []slog.Handler

		baseLevel := LevelFromString(c.Logging.Level)
		opts := &slog.HandlerOptions{
			Level: baseLevel,

			// Log level add source if < INFO
			AddSource: baseLevel < slog.LevelInfo,
		}

		// Main log handler: discard to io.Discard if /dev/null, otherwise use configured path
		if c.Logging.Filename != "" {
			h := GetHandler(c.Logging.LogJSON, isTerminal, logDestination(c.Logging.Filename, &c.Logging), opts)
			fanout = append(fanout, h)
		}

		// if we are a terminal or CopyStdout enabled then log there as well
		if isTerminal || c.Logging.CopyStdout || len(fanout) == 0 {
			h := GetHandler(c.Logging.LogJSON, isTerminal, os.Stdout, opts)
			fanout = append(fanout, h)
		}
		router = router.Add(slogmulti.Fanout(fanout...))
	}

	logger := slog.New(
		router.
			FirstMatch().
			Handler(),
	)
	if c.Logging.IncludeAppName {
		logger = logger.With(slog.String("app", version.Name))
	}
	if c.Logging.IncludeAppVersion {
		logger = logger.With(slog.String("version", version.Version))
	}

	slog.SetDefault(logger)

	return []func(context.Context) error{
		func(context.Context) error { return c.Logging.Close() },
		func(context.Context) error { return c.HTTP.AccessLogging.Close() },
		func(context.Context) error { return c.DB.Logging.Close() },
		func(context.Context) error { return c.QueueLogging.Close() },
	}, nil
}
