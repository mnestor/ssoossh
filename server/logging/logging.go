// Package logging sets up the process's slog default from config.Logging
// plus the "type"-tagged named loggers (access log, db, queue, ldap, audit).
//
// # The destination contract
//
// Every record lands according to this table, and changes to this package
// should start by updating the table:
//
//	record                                  goes to
//	--------------------------------------  -----------------------------------
//	type=<tag>, tag has a configured file   ONLY that file (exclusive; the
//	                                        now-redundant "type" attr dropped)
//	type=<tag>, no file but a level set     ALL of the general destinations,
//	                                        filtered at that tag's own level
//	                                        rather than logging.level
//	everything else                         ALL of the general destinations,
//	                                        filtered at logging.level:
//	  - the main log file                     when logging.filename is set
//	  - stdout                                when in a terminal, or
//	                                          logging.enable_stdout, or no main
//	                                          file is configured (logs must go
//	                                          somewhere)
//	  - stderr, ERROR and up only             when NOT in a terminal (the
//	                                          container/systemd convention)
//
// The middle row is what lets the access log run at INFO while the
// application log stays at WARN, with both on stdout — a container wants its
// requests logged without every INFO the rest of the process emits. Without
// it a named logger's level did nothing at all until it was also given a
// file, which is not what "the minimum log level for this destination"
// says, and left the shipped config logging no requests anywhere.
//
// # Exclusive routes versus broadcast — read before editing
//
// Two different composition primitives are in play and confusing them was
// this package's one production bug:
//
//   - The slogmulti Router with FirstMatch is EXCLUSIVE: a record is given
//     to the first route whose predicate matches and to nothing else. That
//     is what named loggers need — a db query routed to the db file must
//     not also land in the main log.
//   - slogmulti.Fanout is BROADCAST: every handler in it sees the record,
//     each applying its own level filter. That is what the general
//     destinations need — "and also stderr for errors" is a fanout member,
//     never a route. A predicate-less route added to the router matches
//     everything first and silently starves every route after it; that is
//     exactly how INFO/DEBUG output vanished for every non-terminal
//     process while dev terminals looked fine.
//
// So the shape is fixed: the router holds one route per configured named
// logger and ends with the general fanout as its catch-all. Add new
// destinations inside the fanout; add new named loggers to namedLoggers.
//
// OpenTelemetry note: when a logs exporter is configured,
// bootstrap.initObservability later wraps the default logger built here in
// another fanout that adds the OTel handler. It composes on top of this
// package's output; nothing here needs to know about it.
package logging

import (
	"context"
	"log/slog"
	"os"

	slogmulti "github.com/samber/slog-multi"

	"github.com/mnestor/ssoossh/internal/version"
	"github.com/mnestor/ssoossh/server/config"
)

// New builds the process logger per the destination contract above,
// installs it via slog.SetDefault, and returns one close function per
// rotating file logger — the caller must run these on shutdown so rotation
// goroutines and file handles are released. Each is safe to call even when
// that destination was never opened.
func New(c *config.Config) (closeFns []func(context.Context) error, err error) {
	isTerminal := terminalDetector()

	router := slogmulti.Router()
	for _, nl := range namedLoggers(c) {
		if h := namedRoute(c, nl, isTerminal); h != nil {
			router = router.Add(h, slogmulti.AttrValueIs(AttrKeyType, nl.tag))
		}
	}
	router = router.Add(generalFanout(c, isTerminal, LevelFromString(c.Logging.Level)))

	logger := slog.New(router.FirstMatch().Handler())
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
		// LDAP and audit are named loggers like the ones above, so their
		// rotating files need releasing on the same terms. LDAP's was
		// missing: a deployment that set ldap.logging.filename leaked the
		// handle and its rotation goroutine across shutdown.
		func(context.Context) error { return c.LDAP.Logging.Close() },
		func(context.Context) error { return c.Audit.Logging.Close() },
	}, nil
}

// namedLoggers lists every "type"-tagged sub-logger. New components (a
// future "signer" or "listener" log, say) get an entry here rather than a
// bespoke routing block — see https://mnestor.github.io/ssoossh/internals/architecture/.
func namedLoggers(c *config.Config) []namedLoggerConfig {
	return []namedLoggerConfig{
		{tag: TagAccessLog, src: &c.HTTP.AccessLogging},
		{tag: TagDB, src: &c.DB.Logging},
		{tag: TagQueue, src: &c.Queue.Logging},
		{tag: TagLDAP, src: &c.LDAP.Logging},
		{tag: TagAudit, src: &c.Audit.Logging},
	}
}

// namedRoute builds the exclusive route for one "type"-tagged sub-logger,
// or nil when that tag has neither its own file nor its own level and so
// belongs in the catch-all with everything else.
//
// The two cases differ only in where the records land: a tag with a file
// gets that file alone, while a tag with just a level keeps the general
// destinations and only moves its threshold. Both are routes, so a record
// takes one of them or the fanout, never two (see the package doc).
func namedRoute(c *config.Config, nl namedLoggerConfig, isTerminal bool) slog.Handler {
	if nl.src.LogFilename() != "" {
		return newNamedHandler(nl)
	}
	if level := nl.src.LogLevelString(); level != "" {
		return generalFanout(c, isTerminal, LevelFromString(level))
	}
	return nil
}

// generalFanout builds the broadcast handler for the general destinations:
// the main file, stdout, and the non-terminal stderr error copy, per the
// destination contract in the package doc. Always returns at least one
// destination — a process whose config names no file still logs to stdout.
//
// level is the threshold to filter at: logging.level for the catch-all, or
// a named logger's own level for the route built by namedRoute. Only the
// threshold varies; every caller gets the same set of destinations.
func generalFanout(c *config.Config, isTerminal bool, level slog.Level) slog.Handler {
	opts := &slog.HandlerOptions{
		Level: level,
		// Source locations only when someone turned the level below INFO:
		// they are debugging, and file:line is worth the line width.
		AddSource: level < slog.LevelInfo,
	}

	var fanout []slog.Handler

	haveMainFile := c.Logging.Filename != ""
	if haveMainFile {
		fanout = append(fanout, GetHandler(c.Logging.LogJSON, isTerminal, logDestination(c.Logging.Filename, &c.Logging), opts))
	}

	if isTerminal || c.Logging.CopyStdout || !haveMainFile {
		fanout = append(fanout, GetHandler(c.Logging.LogJSON, isTerminal, os.Stdout, opts))
	}

	// The container/systemd convention: errors are duplicated to stderr so
	// a supervisor separating the streams still surfaces them. A fanout
	// member with its own ERROR floor — never a router route (see the
	// package doc for the bug that was).
	if !isTerminal {
		fanout = append(fanout, GetHandler(c.Logging.LogJSON, isTerminal, os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelError,
		}))
	}

	return slogmulti.Fanout(fanout...)
}
