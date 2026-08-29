package logging

import (
	"io"
	"log/slog"
)

// AttrKeyType is the slog attribute key whose value routes a record to a
// named logger. Producers must attach it via Tagged rather than typing the
// key by hand.
const AttrKeyType = "type"

// The named-logger tags. A record tagged with one of these goes ONLY to
// that logger's configured destination (see the package doc's destination
// contract); a typo'd tag would silently fall through to the main log,
// which is why producers and the router share these constants instead of
// string literals.
const (
	TagAccessLog = "accesslog"
	TagDB        = "db"
	TagQueue     = "queue"
	TagLDAP      = "ldap"
	TagAudit     = "audit"
)

// Tagged returns a logger whose records carry the named-logger tag, for
// handing to a subsystem (gin access logging, gorm, watermill). The one
// sanctioned way to produce the attribute.
func Tagged(tag string) *slog.Logger {
	return slog.With(slog.String(AttrKeyType, tag))
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
// see docs/internals/signing-pipeline.md) instead of writing another bespoke
// routing block.
type namedLoggerConfig struct {
	tag string
	src loggerSource
}
