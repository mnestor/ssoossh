// Package logging sets up logging based on config.Logging, config.HTTP.AccessLogging,
// and config.DB.Logging.
package logging

import (
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

// GetHandler builds an slog.Handler writing to w: JSON if json is true,
// otherwise colorized text (via tint) when stderr is a terminal, or plain
// text otherwise.
func GetHandler(json bool, w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	isTerminal := isatty.IsTerminal(os.Stderr.Fd()) || os.Getenv("ISTERMINAL") == "1"

	if json {
		return slog.NewJSONHandler(w, opts)
	} else if isTerminal {
		return tint.NewTextHandler(w, &tint.Options{
			NoColor:     !isTerminal,
			Level:       opts.Level,
			AddSource:   opts.AddSource,
			ReplaceAttr: opts.ReplaceAttr,
			TimeFormat:  time.Stamp,
		})
	}
	return slog.NewTextHandler(w, opts)
}

// Setup builds an slog logger from c.Logging, c.HTTP.AccessLogging, and
// c.DB.Logging and installs it as the default via slog.SetDefault. Access
// log and DB log entries (identified by a "type" attribute) are routed to
// their own configured destinations when a filename is set; everything else
// goes to the main log destination, plus stdout/stderr as configured.
func Setup(c *config.Config) error {
	baseLevel := LevelFromString(c.Logging.Level)

	opts := &slog.HandlerOptions{
		Level: baseLevel,

		// Log level add source if < INFO
		AddSource: baseLevel < slog.LevelInfo,
	}

	router := slogmulti.Router()

	if c.HTTP.AccessLogging.Filename != "" {
		h := GetHandler(
			c.HTTP.AccessLogging.LogJSON,
			logDestination(c.HTTP.AccessLogging.Filename, &c.HTTP.AccessLogging),
			&slog.HandlerOptions{
				AddSource: false,
				Level:     slog.LevelInfo,
			})
		router = router.Add(h, slogmulti.AttrValueIs("type", "accesslog"))
	}

	if c.DB.Logging.Filename != "" {
		h := GetHandler(
			c.DB.Logging.LogJSON,
			logDestination(c.DB.Logging.Filename, &c.DB.Logging),
			&slog.HandlerOptions{
				AddSource: c.DB.Logging.AddSource,
				Level:     LevelFromString(c.DB.Logging.Level),
			})
		router = router.Add(h, slogmulti.AttrValueIs("type", "db"))
	}

	isTerminal := isatty.IsTerminal(os.Stderr.Fd()) || os.Getenv("ISTERMINAL") == "1"

	// always send errors to stderr if not a terminal
	// will get these anyway below if a terminal
	// I think this is good for use in containers?
	if !isTerminal {
		h := GetHandler(c.Logging.LogJSON, os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelError,
		})
		router = router.Add(h)
	}

	{ // group these together
		var fanout []slog.Handler

		// Main log handler: discard to io.Discard if /dev/null, otherwise use configured path
		if c.Logging.Filename != "" {
			h := GetHandler(c.Logging.LogJSON, logDestination(c.Logging.Filename, &c.Logging), opts)
			fanout = append(fanout, h)
		}

		// if we are a terminal or CopyStdout enabled then log there as well
		if isTerminal || c.Logging.CopyStdout || len(fanout) == 0 {
			h := GetHandler(c.Logging.LogJSON, os.Stdout, opts)
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

	return nil
}
