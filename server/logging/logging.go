// Package logging sets up logging based on config.Logging,
// config.HTTP.AccessLogging, config.DB.Logging, and config.QueueLogging —
// and any other "type"-tagged named logger added to New's namedLoggers
// list.
package logging

import (
	"context"
	"log/slog"
	"os"

	slogmulti "github.com/samber/slog-multi"

	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/server/config"
)

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
		{tag: "queue", src: &c.Queue.Logging},
		{tag: "ldap", src: &c.LDAP.Logging},
	}
	for _, nl := range namedLoggers {
		if h := newNamedHandler(nl); h != nil {
			router = router.Add(h, slogmulti.AttrValueIs("type", nl.tag))
		}
	}

	{ // group these together
		var fanout []slog.Handler

		// Always send errors to stderr when not a terminal (containers,
		// systemd). Part of the fanout below, NOT its own router.Add: the
		// router resolves with FirstMatch, and a predicate-less handler
		// added here matched every record first - so this ERROR-levelled
		// handler swallowed all INFO/DEBUG output whenever stdout was not
		// a terminal. In a terminal everything worked, which is exactly
		// why the bug survived: dev runs looked fine while every systemd,
		// docker, and test-harness process logged errors only, regardless
		// of logging.level.
		if !isTerminal {
			fanout = append(fanout, GetHandler(c.Logging.LogJSON, isTerminal, os.Stderr, &slog.HandlerOptions{
				Level: slog.LevelError,
			}))
		}

		baseLevel := LevelFromString(c.Logging.Level)
		opts := &slog.HandlerOptions{
			Level: baseLevel,

			// Log level add source if < INFO
			AddSource: baseLevel < slog.LevelInfo,
		}

		// Main log handler: discard to io.Discard if /dev/null, otherwise use configured path
		haveMainHandler := false
		if c.Logging.Filename != "" {
			h := GetHandler(c.Logging.LogJSON, isTerminal, logDestination(c.Logging.Filename, &c.Logging), opts)
			fanout = append(fanout, h)
			haveMainHandler = true
		}

		// If we are a terminal or CopyStdout is enabled then log there as
		// well - and always when no main handler exists yet, so a process
		// with no filename configured still logs somewhere. Tracked with
		// haveMainHandler rather than len(fanout), which now also counts
		// the stderr error handler above.
		if isTerminal || c.Logging.CopyStdout || !haveMainHandler {
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
		func(context.Context) error { return c.Queue.Logging.Close() },
	}, nil
}
