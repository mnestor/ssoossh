package logging

import (
	"io"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/lmittmann/tint"
	"github.com/mattn/go-isatty"
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
		ReplaceAttr: dropAttr(AttrKeyType),
	})
}
