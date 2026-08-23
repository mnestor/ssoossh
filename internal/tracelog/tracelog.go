// Package tracelog defines the client's verbosity ladder and the redaction
// applied to anything it prints.
//
// It lives in internal/ because the trace points are spread across the
// client and the shared API package, and both need the same level
// definitions. Keeping the levels in one place is what stops -vv on one
// side meaning something different from -vv on the other.
package tracelog

import (
	"log/slog"
	"strings"
)

// LevelTrace sits below slog.LevelDebug, giving three tiers under Warn for
// the -v/-vv/-vvv ladder. slog ships only Debug and Info there, which is one
// short.
const LevelTrace = slog.LevelDebug - 4

// LevelFor maps a -v count to the lowest level that should be emitted.
//
//	0   warnings and errors only — the default, unchanged from before the
//	    flag existed
//	1   the steps a command takes
//	2   HTTP requests, ssh-agent operations, file reads and writes
//	3   bodies, headers, and full certificate contents
func LevelFor(verbosity int) slog.Level {
	switch {
	case verbosity <= 0:
		return slog.LevelWarn
	case verbosity == 1:
		return slog.LevelInfo
	case verbosity == 2:
		return slog.LevelDebug
	default:
		return LevelTrace
	}
}

// LevelName renders a level for output. slog prints LevelTrace as
// "DEBUG-4", which tells a reader nothing.
func LevelName(v any) string {
	level, ok := v.(slog.Level)
	if !ok {
		return "INFO"
	}
	if level <= LevelTrace {
		return "TRACE"
	}
	return level.String()
}

// Redacted is what stands in for a secret. Fixed text rather than a masked
// prefix: a prefix is still a prefix, and this output exists to be pasted
// into bug reports.
const Redacted = "[redacted]"

// sensitiveHeaders are never printed. Matched case-insensitively, since
// header canonicalization is not guaranteed by the time these are read.
var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"proxy-authorization": true,
}

// Header returns a header value safe to print.
func Header(name, value string) string {
	if sensitiveHeaders[strings.ToLower(name)] {
		return Redacted
	}
	return value
}

// sensitiveFields are JSON keys whose values are never printed. The
// enrollment code is here despite not being a standalone credential (it is
// useless without the private key it was enrolled against) — it is still a
// capability, and nothing is gained by putting it in a pasted log.
var sensitiveFields = []string{
	"code",
	"password",
	"token",
	"secret",
	"private_key",
	"enrollment_code",
}

// Body returns a request or response body safe to print, replacing the
// value of any sensitive field.
//
// Deliberately a textual scrub rather than a parse-and-reserialize: bodies
// reach here as raw bytes, some are not JSON at all, and a body that fails
// to parse is exactly the kind this is wanted for. The cost is that it is
// conservative — it redacts anything that looks like one of these fields,
// including in a body that merely mentions one.
func Body(body string) string {
	for _, field := range sensitiveFields {
		body = redactField(body, field)
	}
	return body
}

// redactField replaces the JSON string value following "field": with
// Redacted, wherever it appears.
func redactField(body, field string) string {
	needle := `"` + field + `"`
	var out strings.Builder
	rest := body
	for {
		i := strings.Index(rest, needle)
		if i < 0 {
			out.WriteString(rest)
			return out.String()
		}
		// Everything up to and including the key name is kept: the reader
		// needs to see which field was withheld.
		out.WriteString(rest[:i+len(needle)])
		rest = rest[i+len(needle):]

		// Only a string value is redacted. A non-string (a number, an
		// object) is left alone rather than guessed at — none of the
		// sensitive fields are non-strings, so anything else is a field
		// that merely shares the name.
		colon := strings.Index(rest, ":")
		open := strings.Index(rest, `"`)
		if colon < 0 || open < 0 || open < colon {
			continue
		}
		end := strings.Index(rest[open+1:], `"`)
		if end < 0 {
			continue
		}
		out.WriteString(rest[:open+1])
		out.WriteString(Redacted)
		rest = rest[open+1+end:]
	}
}
