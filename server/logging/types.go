package logging

import "io"

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
// see docs/signing-pipeline.md) instead of writing another bespoke
// routing block.
type namedLoggerConfig struct {
	tag string
	src loggerSource
}
