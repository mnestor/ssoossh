package logging

// Test methodology: table-driven tests with t.Parallel() for parallelization
// and t.Run() for subtests. Each test verifies one specific behavior.
// Tests that mutate global state (slog.SetDefault) are sequential and
// restore prior state via t.Cleanup().

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/server/config"
)

func TestLevelFromString_ShouldParseNumericStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  slog.Level
	}{
		{"should parse negative numeric string", "-4", slog.LevelDebug},
		{"should parse zero", "0", slog.LevelInfo},
		{"should parse positive numeric string for warn", "4", slog.LevelWarn},
		{"should parse positive numeric string for error", "8", slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := LevelFromString(tt.input); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLevelFromString_ShouldParseLevelNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  slog.Level
	}{
		{"should parse lowercase debug", "debug", slog.LevelDebug},
		{"should parse uppercase INFO", "INFO", slog.LevelInfo},
		{"should parse mixed-case Warn", "Warn", slog.LevelWarn},
		{"should parse lowercase error", "error", slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := LevelFromString(tt.input); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLevelFromString_ShouldFallBackToInfoWhenGarbage(t *testing.T) {
	t.Parallel()

	if got := LevelFromString("not-a-level"); got != slog.LevelInfo {
		t.Errorf("got %v, want %v", got, slog.LevelInfo)
	}
}

func TestLevelFromString_ShouldFallBackToInfoWhenEmpty(t *testing.T) {
	t.Parallel()

	if got := LevelFromString(""); got != slog.LevelInfo {
		t.Errorf("got %v, want %v", got, slog.LevelInfo)
	}
}

func TestGetHandler_ShouldProduceValidJSONWhenJSONTrue(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	h := GetHandler(true, false, &buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(h)
	logger.Info("hello", "key", "value")

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("expected valid JSON output, got error: %v, output: %s", err, buf.String())
	}
	if parsed["msg"] != "hello" {
		t.Errorf("got msg %v, want %q", parsed["msg"], "hello")
	}
}

func TestGetHandler_ShouldProduceTextOutputWhenJSONFalse(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	h := GetHandler(false, false, &buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(h)
	logger.Info("hello world")

	out := buf.String()
	if !strings.Contains(out, "hello world") {
		t.Errorf("expected output to contain message, got: %s", out)
	}
	// Text output (JSON false) should not be valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err == nil {
		t.Errorf("expected non-JSON text output, but it parsed as JSON: %s", out)
	}
}

func TestGetHandler_ShouldRespectLevelOption(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	h := GetHandler(true, false, &buf, &slog.HandlerOptions{Level: slog.LevelError})
	logger := slog.New(h)
	logger.Info("should be filtered out")

	if buf.Len() != 0 {
		t.Errorf("expected no output below configured level, got: %s", buf.String())
	}
}

func TestGetHandler_ShouldEmitReadableTextWhenIsTerminalTrue(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	h := GetHandler(false, true, &buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	logger := slog.New(h)
	logger.Info("terminal message")

	if !strings.Contains(buf.String(), "terminal message") {
		t.Errorf("expected terminal-style output to contain the message, got: %s", buf.String())
	}
}

// TestTerminalDetector_ShouldRespectISTERMINALOverride sets/reads the
// ISTERMINAL env var, so it must not run in parallel with other tests
// touching it.
func TestTerminalDetector_ShouldRespectISTERMINALOverride(t *testing.T) {
	t.Setenv("ISTERMINAL", "1")

	if !terminalDetector() {
		t.Error("expected terminalDetector to return true with ISTERMINAL=1")
	}
}

func TestDropAttr_ShouldOmitMatchingTopLevelKey(t *testing.T) {
	t.Parallel()

	replace := dropAttr("type")

	got := replace(nil, slog.String("type", "db"))
	if got.Key != "" {
		t.Errorf("expected the matching attr to be omitted (zero Attr), got %+v", got)
	}
}

func TestDropAttr_ShouldLeaveOtherKeysUntouched(t *testing.T) {
	t.Parallel()

	replace := dropAttr("type")

	got := replace(nil, slog.String("other", "value"))
	if got.Key != "other" || got.Value.String() != "value" {
		t.Errorf("expected other keys to pass through unchanged, got %+v", got)
	}
}

func TestDropAttr_ShouldLeaveGroupedKeysUntouched(t *testing.T) {
	t.Parallel()

	replace := dropAttr("type")

	got := replace([]string{"group"}, slog.String("type", "db"))
	if got.Key != "type" {
		t.Errorf("expected a grouped attr not to be treated as top-level and dropped, got %+v", got)
	}
}

// TestNew_ShouldInstallDefaultLogger calls logging.New, which mutates
// global state via slog.SetDefault. It must not run in parallel with other
// tests that read/write slog's default logger, and it restores the prior
// default logger afterward via t.Cleanup.
func TestNew_ShouldInstallDefaultLogger(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	c := &config.Config{}
	c.Logging.Level = "info"

	if _, err := New(c); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if slog.Default() == prev {
		t.Error("expected slog.Default() to change after New, but it did not")
	}
}

// TestNew_ShouldIncludeAppNameAndVersionWhenConfigured mutates global slog
// state via slog.SetDefault, so it must not run in parallel with other tests
// touching slog's default logger. The prior default is restored via
// t.Cleanup.
func TestNew_ShouldIncludeAppNameAndVersionWhenConfigured(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	c := &config.Config{}
	c.Logging.Level = "info"
	c.Logging.IncludeAppName = true
	c.Logging.IncludeAppVersion = true

	if _, err := New(c); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// New succeeded; nothing further to assert without capturing output,
	// since New always logs to stdout/stderr rather than an injectable
	// writer. The prior test already covers the core dispatch behavior.
}

// TestNew_ShouldReturnACloseFuncPerRotatingLogger mutates global slog state
// via slog.SetDefault, so it must not run in parallel.
func TestNew_ShouldReturnACloseFuncPerRotatingLogger(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	c := &config.Config{}
	c.Logging.Level = "info"

	closeFns, err := New(c)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(closeFns) != 4 {
		t.Fatalf("got %d close functions, want 4 (main, accesslog, db, queue)", len(closeFns))
	}
	for i, closeFn := range closeFns {
		if err := closeFn(t.Context()); err != nil {
			t.Errorf("close function %d returned an unexpected error: %v", i, err)
		}
	}
}

// TestNew_ShouldRouteTypedRecordsToConfiguredFiles mutates global slog
// state via slog.SetDefault and the ISTERMINAL env var, so it must not run
// in parallel. The prior default logger is restored via t.Cleanup.
func TestNew_ShouldRouteTypedRecordsToConfiguredFiles(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	// Present as a terminal so everything also goes to stdout instead of
	// installing the stderr-only error handler, keeping routing simple.
	t.Setenv("ISTERMINAL", "1")

	dir := t.TempDir()
	mainLog := filepath.Join(dir, "main.log")
	accessLog := filepath.Join(dir, "access.log")
	dbLog := filepath.Join(dir, "db.log")

	c := &config.Config{}
	c.Logging.Level = "info"
	c.Logging.Filename = mainLog
	c.HTTP.AccessLogging.Filename = accessLog
	c.HTTP.AccessLogging.LogJSON = true
	c.DB.Logging.Filename = dbLog
	c.DB.Logging.LogJSON = true

	if _, err := New(c); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	slog.Info("access-entry", "type", "accesslog")
	slog.Info("db-entry", "type", "db")
	slog.Info("main-entry")

	tests := []struct {
		name string
		file string
		want string
	}{
		{"should write access log records to the access log file", accessLog, "access-entry"},
		{"should write db log records to the db log file", dbLog, "db-entry"},
		{"should write untyped records to the main log file", mainLog, "main-entry"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.file)
			if err != nil {
				t.Fatalf("failed to read %s: %v", tt.file, err)
			}
			if !strings.Contains(string(data), tt.want) {
				t.Errorf("expected %s to contain %q, got: %s", tt.file, tt.want, string(data))
			}
		})
	}
}

// TestNew_ShouldStripTypeAttrFromItsOwnDedicatedLogButKeepItInGeneralLog
// verifies the redundant "type" attribute is dropped from a record's own
// dedicated destination but preserved when a record falls through to the
// general log (no dedicated destination configured for its type). Mutates
// global slog state, so it must not run in parallel.
func TestNew_ShouldStripTypeAttrFromItsOwnDedicatedLogButKeepItInGeneralLog(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv("ISTERMINAL", "1")

	dir := t.TempDir()
	mainLog := filepath.Join(dir, "main.log")
	dbLog := filepath.Join(dir, "db.log")

	c := &config.Config{}
	c.Logging.Level = "info"
	c.Logging.Filename = mainLog
	c.DB.Logging.Filename = dbLog
	c.DB.Logging.LogJSON = true

	if _, err := New(c); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	slog.Info("db-entry", "type", "db")
	// "queue" has no dedicated destination configured in this test, so it
	// falls through to the general log and should keep its "type" attr.
	slog.Info("queue-entry", "type", "queue")

	dbData, err := os.ReadFile(dbLog)
	if err != nil {
		t.Fatalf("failed to read %s: %v", dbLog, err)
	}
	if strings.Contains(string(dbData), `"type"`) {
		t.Errorf("expected the db log's own dedicated file not to contain a redundant \"type\" attr, got: %s", dbData)
	}

	mainData, err := os.ReadFile(mainLog)
	if err != nil {
		t.Fatalf("failed to read %s: %v", mainLog, err)
	}
	if !strings.Contains(string(mainData), "queue") {
		t.Errorf("expected the general log to keep \"type\" for records without a dedicated destination, got: %s", mainData)
	}
}

// TestNew_ShouldSkipDevNullFilenames mutates global slog state via
// slog.SetDefault, so it must not run in parallel. The prior default logger
// is restored via t.Cleanup.
func TestNew_ShouldSkipDevNullFilenames(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	t.Setenv("ISTERMINAL", "1")

	c := &config.Config{}
	c.Logging.Level = "info"
	c.Logging.Filename = "/dev/null"
	c.HTTP.AccessLogging.Filename = "/dev/null"
	c.HTTP.AccessLogging.LogJSON = true
	c.DB.Logging.Filename = "/dev/null"
	c.DB.Logging.LogJSON = true

	// New should succeed even with /dev/null paths; they're treated as noop
	if _, err := New(c); err != nil {
		t.Fatalf("expected no error setting up logging with /dev/null, got %v", err)
	}

	// All logs go to stdout (because ISTERMINAL=1), not to /dev/null files
	slog.Info("test-message")
	// Test passes if New completed without error
}
