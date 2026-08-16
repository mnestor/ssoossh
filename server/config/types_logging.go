package config

import "github.com/DeRuina/timberjack"

// AppLogging configures the application's main log output.
type AppLogging struct {
	timberjack.Logger
	Level             string `mapstructure:"level"`
	CopyStdout        bool   `mapstructure:"enable_stdout"`
	IncludeAppName    bool   `mapstructure:"include_app_name"`
	IncludeAppVersion bool   `mapstructure:"include_app_version"`
	LogJSON           bool   `mapstructure:"log_json"`
}

// GenericLogging configures a named, independently-routed log destination
// (e.g. database queries, the message queue) — anything that just needs a
// level, a rotating file, and a JSON/text choice. See server/logging.New,
// which routes records tagged with a given name to their own GenericLogging
// destination when one is configured.
type GenericLogging struct {
	timberjack.Logger
	Level     string `mapstructure:"level"`
	AddSource bool   `mapstructure:"add_source"`
	LogJSON   bool   `mapstructure:"log_json"`
}

// AccessLogging configures which fields the HTTP access log records.
type AccessLogging struct {
	timberjack.Logger
	WithUserAgent      bool `mapstructure:"log_user_agent"`
	WithRequestHeader  bool `mapstructure:"log_request_header"`
	WithClientIP       bool `mapstructure:"log_client_ip"`
	WithRequestID      bool `mapstructure:"log_request_id"`
	WithRequestBody    bool `mapstructure:"log_request_body"`
	WithResponseBody   bool `mapstructure:"log_response_body"`
	WithResponseHeader bool `mapstructure:"log_response_header"`
	WithSpanID         bool `mapstructure:"log_span_id"`
	WithTraceID        bool `mapstructure:"log_trace_id"`
	LogJSON            bool `mapstructure:"log_json"`
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

// LogLevelString always returns "info" — access logging has no separate
// level knob, unlike GenericLogging. Part of server/logging's loggerSource
// interface.
func (a *AccessLogging) LogLevelString() string { return "info" }

// LogAddSource always returns false — access logging never includes
// source file/line. Part of server/logging's loggerSource interface.
func (a *AccessLogging) LogAddSource() bool { return false }
