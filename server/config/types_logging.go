package config

import "github.com/DeRuina/timberjack"

// AppLogging configures the application's main log output.
type AppLogging struct {
	// Log-file rotation, via the embedded timberjack logger:
	// filename, maxsize, maxage, maxbackups, localtime, compression,
	// rotationinterval, backuptimeformat, rotateatminutes, rotateat,
	// appendtimeafterext, and filemode.
	//
	// An unset filename means no log file is written and output goes to
	// stdout instead. maxsize is in megabytes (default 100); maxage is in
	// days (default: keep forever); maxbackups caps retained rotated files
	// (default: keep all, subject to maxage); localtime uses local time
	// rather than UTC in rotated filenames; compression is none, gzip, or
	// zstd; rotationinterval is a duration forcing rotation on a schedule in
	// addition to size. The rest tune backup filename formatting and
	// rotation at specific times. See the DeRuina/timberjack package for the
	// per-field detail.
	timberjack.Logger

	// Level is the minimum log level: a level name (debug, info, warn,
	// error, case-insensitive, optionally with a numeric +N or -N offset) or
	// a raw numeric slog level such as -4, 0, 4, or 8. An unrecognized or
	// empty value falls back to info.
	Level string `mapstructure:"level"`

	// CopyStdout also writes the main log to stdout even when a filename is
	// set. When no filename is set, or the process is attached to a
	// terminal, stdout is used regardless.
	CopyStdout bool `mapstructure:"enable_stdout" example:"false"`

	// IncludeAppName adds an "app" attribute, the string "ssoossh", to every
	// log record.
	IncludeAppName bool `mapstructure:"include_app_name"`

	// IncludeAppVersion adds a "version" attribute to every log record.
	IncludeAppVersion bool `mapstructure:"include_app_version"`

	// LogJSON writes JSON instead of text. Text is meant to be read, not
	// parsed: the message is written as bare prose so quoted values in it
	// are not escaped, which is not a machine-readable format. Set this
	// wherever a log collector has to pull fields back out.
	LogJSON bool `mapstructure:"log_json" example:"false"`
}

// GenericLogging configures a named, independently-routed log destination
// (e.g. database queries, the message queue) — anything that just needs a
// level, a rotating file, and a JSON/text choice. See server/logging.New,
// which routes records tagged with a given name to their own
// destination when one is configured.
type GenericLogging struct {
	// Log-file rotation for this destination, via the embedded timberjack
	// logger, with the same keys and meanings as logging.* above. This
	// destination is only split out of the main log once its filename is
	// set; until then its records go to the general log.
	timberjack.Logger

	// Level is the minimum log level for this destination, in the same form
	// as logging.level.
	Level string `mapstructure:"level"`

	// AddSource includes the source file and line on each record.
	AddSource bool `mapstructure:"add_source" example:"false"`

	// LogJSON writes JSON instead of text. Text is meant to be read, not
	// parsed: the message is written as bare prose so quoted values in it
	// are not escaped, which is not a machine-readable format. Set this
	// wherever a log collector has to pull fields back out.
	LogJSON bool `mapstructure:"log_json" example:"false"`
}

// AccessLogging configures which fields the HTTP access log records and
// where it writes them. The access log is routed separately from the main
// application log, so its destination and format can differ.
type AccessLogging struct {
	// Log-file rotation for the access log, via the embedded timberjack
	// logger, with the same keys and meanings as logging.* above. The access
	// log is only split into its own file once its filename is set.
	timberjack.Logger

	// Level is the minimum log level for the access log, in the same form as
	// logging.level. Requests are logged at INFO, client errors (4xx) at
	// WARN and server errors (5xx) at ERROR, so "info" logs every request
	// and "warn" logs only the failures.
	//
	// This is what lets the access log be more verbose than the application
	// log: the two are filtered independently, and both still reach stdout.
	// Left empty the access log has no threshold of its own and is filtered
	// at logging.level along with everything else — which, with the shipped
	// logging.level of WARN, means no successful request is ever logged.
	Level string `mapstructure:"level"`

	// WithUserAgent records the request's User-Agent header.
	WithUserAgent bool `mapstructure:"log_user_agent"`

	// WithRequestHeader records the full request header set. Verbose, and it
	// captures whatever the client sent.
	WithRequestHeader bool `mapstructure:"log_request_header"`

	// WithClientIP records the client address, as resolved through
	// http.trusted_proxies.
	WithClientIP bool `mapstructure:"log_client_ip"`

	// WithRequestID records the per-request correlation ID.
	WithRequestID bool `mapstructure:"log_request_id"`

	// WithRequestBody records the request body. Off by default and worth
	// leaving off: request bodies on this server carry public keys and
	// enrollment codes.
	WithRequestBody bool `mapstructure:"log_request_body"`

	// WithResponseBody records the response body. Off by default and worth
	// leaving off: response bodies carry issued certificates and enrollment
	// tokens.
	WithResponseBody bool `mapstructure:"log_response_body"`

	// WithResponseHeader records the full response header set.
	WithResponseHeader bool `mapstructure:"log_response_header"`

	// WithSpanID records the OpenTelemetry span ID.
	WithSpanID bool `mapstructure:"log_span_id"`

	// WithTraceID records the OpenTelemetry trace ID.
	WithTraceID bool `mapstructure:"log_trace_id"`

	// LogJSON writes JSON instead of text. Text is meant to be read, not
	// parsed: the message is written as bare prose so quoted values in it
	// are not escaped, which is not a machine-readable format. Set this
	// wherever a log collector has to pull fields back out.
	LogJSON bool `mapstructure:"log_json" example:"false"`
}

// LogFilename returns the configured destination filename, or "" if
// logging to this destination isn't configured. Part of
// server/logging's loggerSource interface.
func (g *GenericLogging) LogFilename() string { return g.Filename }

// LogJSONEnabled reports whether this destination should be written as
// JSON rather than text. Part of server/logging's loggerSource interface.
func (g *GenericLogging) LogJSONEnabled() bool { return g.LogJSON }

// LogLevelString returns the configured minimum log level, unparsed (see
// server/logging.LevelFromString). Part of server/logging's loggerSource
// interface.
func (g *GenericLogging) LogLevelString() string { return g.Level }

// LogAddSource reports whether log records for this destination should
// include the source file/line. Part of server/logging's loggerSource
// interface.
func (g *GenericLogging) LogAddSource() bool { return g.AddSource }

// LogFilename returns the configured access-log filename, or "" if not
// configured. Part of server/logging's loggerSource interface.
func (a *AccessLogging) LogFilename() string { return a.Filename }

// LogJSONEnabled reports whether the access log should be written as JSON
// rather than text. Part of server/logging's loggerSource interface.
func (a *AccessLogging) LogJSONEnabled() bool { return a.LogJSON }

// LogLevelString returns the configured minimum access-log level,
// unparsed (see server/logging.LevelFromString). Empty means the access log
// has no threshold of its own; server/logging.namedRoute reads it that way
// and leaves those records to the general log's level. Part of
// server/logging's loggerSource interface.
func (a *AccessLogging) LogLevelString() string { return a.Level }

// LogAddSource always returns false — access logging never includes
// source file/line. Part of server/logging's loggerSource interface.
func (a *AccessLogging) LogAddSource() bool { return false }
