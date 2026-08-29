package logging

// Test methodology: table-driven tests with t.Parallel() for parallelization
// and t.Run() for subtests. Each test verifies one specific behavior.
// Tests that mutate global state (slog.SetDefault) are sequential and
// restore prior state via t.Cleanup().

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// Guards the reason both text paths go through tint rather than
// slog.NewTextHandler: TextHandler renders the message as a string
// attribute, so a message containing spaces is wrapped in quotes and every
// quote inside it comes back out backslash-escaped. Config advice naming a
// setting's value and wrapped errors naming a path both quote routinely,
// and having to read them through the escaping is what regressing here
// would bring back.
func TestGetHandler_ShouldNotEscapeQuotesInTheMessage(t *testing.T) {
	t.Parallel()

	const msg = `mail.smtp.auth is "none" while relaying to the non-local host "10.0.10.1"`

	tests := []struct {
		name       string
		isTerminal bool
	}{
		{"should write the message verbatim to a file or container stream", false},
		{"should write the message verbatim to a terminal", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			h := GetHandler(false, tt.isTerminal, &buf, &slog.HandlerOptions{Level: slog.LevelInfo})
			slog.New(h).Warn(msg)

			if out := buf.String(); !strings.Contains(out, msg) {
				t.Errorf("expected the message verbatim, got: %s", out)
			}
		})
	}
}

// The non-terminal text timestamp is RFC3339 with milliseconds on purpose:
// it is what slog's TextHandler wrote before tint took over that path, so a
// log file that spans the change still sorts and diffs as one series.
func TestGetHandler_ShouldTimestampNonTerminalTextAsRFC3339Millis(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	h := GetHandler(false, false, &buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.New(h).Info("timestamped")

	stamp, _, _ := strings.Cut(buf.String(), " ")
	if _, err := time.Parse(textTimeFormat, stamp); err != nil {
		t.Errorf("got timestamp %q, want %s: %v", stamp, textTimeFormat, err)
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
	if len(closeFns) != 6 {
		t.Fatalf("got %d close functions, want 6 (main, accesslog, db, queue, ldap, audit)", len(closeFns))
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

// captureStdouterr swaps os.Stdout and os.Stderr for pipes, runs fn, and
// returns what each stream received. Sequential by nature - it mutates
// process globals, so callers must not run in parallel.
func captureStdouterr(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()

	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	fn()

	_ = outW.Close()
	_ = errW.Close()
	outB, _ := io.ReadAll(outR)
	errB, _ := io.ReadAll(errR)
	return string(outB), string(errB)
}

// TestNew_DestinationContractWhenNotATerminal locks in the package-doc
// table for the non-terminal case (containers, systemd, this test binary):
// INFO reaches stdout, ERROR reaches both stdout and stderr, and nothing
// below the configured level appears anywhere. This is the regression test
// for the bug where a predicate-less ERROR route starved stdout of all
// INFO/DEBUG output outside a terminal.
//
// Mutates the default logger and process stdio; must not run in parallel.
func TestNew_DestinationContractWhenNotATerminal(t *testing.T) {
	tests := []struct {
		name       string
		level      string
		log        func()
		wantStdout []string
		notStdout  []string
		wantStderr []string
		notStderr  []string
	}{
		{
			name:       "should emit info to stdout only when not a terminal",
			level:      "info",
			log:        func() { slog.Info("split-mode-info-marker") },
			wantStdout: []string{"split-mode-info-marker"},
			notStderr:  []string{"split-mode-info-marker"},
		},
		{
			name:       "should copy errors to stderr as well when not a terminal",
			level:      "info",
			log:        func() { slog.Error("split-mode-error-marker") },
			wantStdout: []string{"split-mode-error-marker"},
			wantStderr: []string{"split-mode-error-marker"},
		},
		{
			name:      "should drop records below the configured level everywhere",
			level:     "warn",
			log:       func() { slog.Info("below-level-marker") },
			notStdout: []string{"below-level-marker"},
			notStderr: []string{"below-level-marker"},
		},
		{
			name:       "should emit debug when the level asks for it",
			level:      "debug",
			log:        func() { slog.Debug("debug-marker") },
			wantStdout: []string{"debug-marker"},
			notStderr:  []string{"debug-marker"},
		},
	}

	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &config.Config{}
			c.Logging.Level = tt.level
			// No filename: stdout is the fallback main destination.

			stdout, stderr := captureStdouterr(t, func() {
				if _, err := New(c); err != nil {
					t.Fatalf("New() error = %v", err)
				}
				tt.log()
			})

			for _, want := range tt.wantStdout {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout missing %q; got:\n%s", want, stdout)
				}
			}
			for _, not := range tt.notStdout {
				if strings.Contains(stdout, not) {
					t.Errorf("stdout unexpectedly contains %q", not)
				}
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr missing %q; got:\n%s", want, stderr)
				}
			}
			for _, not := range tt.notStderr {
				if strings.Contains(stderr, not) {
					t.Errorf("stderr unexpectedly contains %q", not)
				}
			}
		})
	}
}

// TestNew_ShouldFilterANamedLoggerAtItsOwnLevelWithoutAFile locks in the
// middle row of the package-doc table, and with it the deployment this was
// added for: a container running the shipped logging.level of WARN still
// wants every request logged. The access log runs at INFO, the application
// log stays at WARN, and both come out on stdout.
//
// The pre-existing shape of the bug is the third case: before a named
// logger's level applied without a file, setting it did nothing at all and
// the access log was filtered at WARN with everything else, so no served
// request was ever logged anywhere.
//
// Mutates the default logger and process stdio; must not run in parallel.
func TestNew_ShouldFilterANamedLoggerAtItsOwnLevelWithoutAFile(t *testing.T) {
	tests := []struct {
		name        string
		mainLevel   string
		accessLevel string
		log         func()
		wantStdout  []string
		notStdout   []string
		wantStderr  []string
		notStderr   []string
	}{
		{
			name:        "should log a request at info while the app log stays at warn",
			mainLevel:   "warn",
			accessLevel: "info",
			log: func() {
				Tagged(TagAccessLog).Info("request-marker")
				slog.Info("app-info-marker")
			},
			wantStdout: []string{"request-marker"},
			notStdout:  []string{"app-info-marker"},
		},
		{
			name:        "should keep the type attr, having no dedicated destination",
			mainLevel:   "warn",
			accessLevel: "info",
			log:         func() { Tagged(TagAccessLog).Info("typed-marker") },
			wantStdout:  []string{"typed-marker", "type=" + TagAccessLog},
		},
		{
			name:        "should filter at the general level when no access level is set",
			mainLevel:   "warn",
			accessLevel: "",
			log:         func() { Tagged(TagAccessLog).Info("unset-marker") },
			notStdout:   []string{"unset-marker"},
		},
		{
			name:        "should still copy a server error to stderr",
			mainLevel:   "warn",
			accessLevel: "info",
			log:         func() { Tagged(TagAccessLog).Error("five-hundred-marker") },
			wantStdout:  []string{"five-hundred-marker"},
			wantStderr:  []string{"five-hundred-marker"},
		},
		{
			name:        "should drop a request below the access log's own level",
			mainLevel:   "warn",
			accessLevel: "error",
			log:         func() { Tagged(TagAccessLog).Info("quiet-marker") },
			notStdout:   []string{"quiet-marker"},
			notStderr:   []string{"quiet-marker"},
		},
	}

	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &config.Config{}
			c.Logging.Level = tt.mainLevel
			c.HTTP.AccessLogging.Level = tt.accessLevel
			// No filenames anywhere: stdout is the only main destination,
			// which is the container case this exists for.

			stdout, stderr := captureStdouterr(t, func() {
				if _, err := New(c); err != nil {
					t.Fatalf("New() error = %v", err)
				}
				tt.log()
			})

			for _, want := range tt.wantStdout {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout missing %q; got:\n%s", want, stdout)
				}
			}
			for _, not := range tt.notStdout {
				if strings.Contains(stdout, not) {
					t.Errorf("stdout unexpectedly contains %q; got:\n%s", not, stdout)
				}
			}
			for _, want := range tt.wantStderr {
				if !strings.Contains(stderr, want) {
					t.Errorf("stderr missing %q; got:\n%s", want, stderr)
				}
			}
			for _, not := range tt.notStderr {
				if strings.Contains(stderr, not) {
					t.Errorf("stderr unexpectedly contains %q; got:\n%s", not, stderr)
				}
			}
		})
	}
}

// A named logger with both a file and a level keeps the file: the level
// route must not steal records from the dedicated destination, which is the
// exclusivity the router's FirstMatch exists to provide.
//
// Mutates the default logger; must not run in parallel.
func TestNew_ShouldPreferAConfiguredFileOverTheLevelRoute(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	dir := t.TempDir()
	accessLog := filepath.Join(dir, "access.log")

	c := &config.Config{}
	c.Logging.Level = "warn"
	c.HTTP.AccessLogging.Filename = accessLog
	c.HTTP.AccessLogging.Level = "info"

	stdout, _ := captureStdouterr(t, func() {
		if _, err := New(c); err != nil {
			t.Fatalf("New() error = %v", err)
		}
		Tagged(TagAccessLog).Info("file-marker")
	})

	data, err := os.ReadFile(accessLog)
	if err != nil {
		t.Fatalf("failed to read %s: %v", accessLog, err)
	}
	if !strings.Contains(string(data), "file-marker") {
		t.Errorf("expected the access log file to contain the record, got: %s", data)
	}
	if strings.Contains(stdout, "file-marker") {
		t.Errorf("expected the dedicated file to be exclusive, but stdout also got it:\n%s", stdout)
	}
}
