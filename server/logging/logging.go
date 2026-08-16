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
		func(context.Context) error { return c.Queue.Logging.Close() },
	}, nil
}
