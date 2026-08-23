package config

import (
	"testing"

	"github.com/DeRuina/timberjack"
)

// These eight accessors are how server/logging reads a destination's
// settings: they are the loggerSource interface, and every one of them sat
// at 0% coverage. They look too trivial to test until you notice that
// AccessLogging deliberately answers two of them with constants rather than
// fields, which is exactly the kind of thing a well-meant refactor
// "corrects" into a behaviour change nobody notices.
func TestGenericLogging_ShouldExposeItsConfiguredSettings(t *testing.T) {
	// Filename comes from the embedded timberjack.Logger, which is where
	// the rotation settings live too.
	g := &GenericLogging{
		Logger:    timberjack.Logger{Filename: "/var/log/ssoosshd.log"},
		Level:     "debug",
		AddSource: true,
		LogJSON:   true,
	}

	if got := g.LogFilename(); got != "/var/log/ssoosshd.log" {
		t.Errorf("LogFilename() = %q, want the configured filename", got)
	}
	if got := g.LogLevelString(); got != "debug" {
		t.Errorf("LogLevelString() = %q, want %q", got, "debug")
	}
	if !g.LogAddSource() {
		t.Error("LogAddSource() = false, want true")
	}
	if !g.LogJSONEnabled() {
		t.Error("LogJSONEnabled() = false, want true")
	}
}

// The zero value has to read as "not configured" rather than as some
// default destination, or an unconfigured logging block would start writing
// files nobody asked for.
func TestGenericLogging_ShouldReportNothingConfiguredForItsZeroValue(t *testing.T) {
	g := &GenericLogging{}

	if got := g.LogFilename(); got != "" {
		t.Errorf("LogFilename() = %q, want empty for an unconfigured destination", got)
	}
	if g.LogAddSource() {
		t.Error("LogAddSource() = true, want false by default")
	}
	if g.LogJSONEnabled() {
		t.Error("LogJSONEnabled() = true, want false by default")
	}
}

// Access logging answers two of the four with constants: it has no level
// knob of its own, and it never includes source file and line. Both are
// deliberate, and both are invisible from the struct alone.
func TestAccessLogging_ShouldAnswerLevelAndSourceWithConstants(t *testing.T) {
	a := &AccessLogging{Logger: timberjack.Logger{Filename: "/var/log/access.log"}, LogJSON: true}

	if got := a.LogFilename(); got != "/var/log/access.log" {
		t.Errorf("LogFilename() = %q, want the configured filename", got)
	}
	if !a.LogJSONEnabled() {
		t.Error("LogJSONEnabled() = false, want true")
	}
	if got := a.LogLevelString(); got != "info" {
		t.Errorf("LogLevelString() = %q, want %q -- access logging has no level knob", got, "info")
	}
	if a.LogAddSource() {
		t.Error("LogAddSource() = true, want false -- access logging never includes source")
	}
}
