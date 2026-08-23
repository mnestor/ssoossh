package tracelog

import (
	"log/slog"
	"strings"
	"testing"
)

func TestLevelFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		verbosity int
		want      slog.Level
	}{
		// The default must be exactly what it was before the flag existed:
		// a user who passes nothing sees warnings and errors and no more.
		{name: "should emit only warnings and errors when not asked for", verbosity: 0, want: slog.LevelWarn},
		{name: "should emit only warnings and errors for a negative count", verbosity: -1, want: slog.LevelWarn},
		{name: "should emit the steps at one v", verbosity: 1, want: slog.LevelInfo},
		{name: "should emit requests and file operations at two vs", verbosity: 2, want: slog.LevelDebug},
		{name: "should emit bodies at three vs", verbosity: 3, want: LevelTrace},
		{name: "should clamp beyond three vs rather than going quiet", verbosity: 9, want: LevelTrace},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := LevelFor(tt.verbosity); got != tt.want {
				t.Errorf("LevelFor(%d) = %v, want %v", tt.verbosity, got, tt.want)
			}
		})
	}
}

func TestLevelName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		level any
		want  string
	}{
		{name: "should name the trace level rather than printing DEBUG-4", level: LevelTrace, want: "TRACE"},
		{name: "should name the debug level", level: slog.LevelDebug, want: "DEBUG"},
		{name: "should name the warn level", level: slog.LevelWarn, want: "WARN"},
		{name: "should fall back when handed something that is not a level", level: "nonsense", want: "INFO"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := LevelName(tt.level); got != tt.want {
				t.Errorf("LevelName(%v) = %q, want %q", tt.level, got, tt.want)
			}
		})
	}
}

func TestHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		header     string
		value      string
		wantRedact bool
	}{
		{name: "should withhold an authorization header", header: "Authorization", wantRedact: true},
		{name: "should withhold a cookie", header: "Cookie", wantRedact: true},
		{name: "should withhold a set-cookie", header: "Set-Cookie", wantRedact: true},
		// Canonicalization is not guaranteed by the time these are read, so
		// the match cannot depend on casing.
		{name: "should withhold regardless of casing", header: "AUTHORIZATION", wantRedact: true},
		{name: "should print an ordinary header", header: "Accept", value: "application/json"},
		{name: "should print the user agent", header: "User-Agent", value: "ssoossh/0.1.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Header(tt.header, tt.value)
			if tt.wantRedact {
				if got != Redacted {
					t.Errorf("Header(%q) = %q, want it withheld", tt.header, got)
				}
				return
			}
			if got != tt.value {
				t.Errorf("Header(%q) = %q, want %q", tt.header, got, tt.value)
			}
		})
	}
}

func TestBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		body        string
		wantAbsent  string
		wantPresent string
	}{
		{
			name:        "should withhold an enrollment code",
			body:        `{"code":"6138869d-863f-499f-9d21-6c75f66e4ac1"}`,
			wantAbsent:  "6138869d",
			wantPresent: Redacted,
		},
		{
			// The reader has to see which field was withheld, or the output
			// is a mystery rather than a redaction.
			name:        "should keep the field name it withheld",
			body:        `{"code":"secret-value"}`,
			wantAbsent:  "secret-value",
			wantPresent: `"code"`,
		},
		{
			name:        "should leave neighbouring fields alone",
			body:        `{"code":"secret-value","public_key":"ssh-ed25519 AAAA"}`,
			wantAbsent:  "secret-value",
			wantPresent: "ssh-ed25519 AAAA",
		},
		{
			// A near miss must not be redacted, or the output becomes
			// useless through over-scrubbing.
			name:        "should not withhold a field that merely contains the word",
			body:        `{"encoded":"keep me"}`,
			wantPresent: "keep me",
		},
		{
			name:        "should not withhold a differently-suffixed field",
			body:        `{"code_challenge":"keep me"}`,
			wantPresent: "keep me",
		},
		{
			// Bodies that are not JSON are exactly the ones worth seeing.
			name:        "should pass through a body that is not json",
			body:        `<html>gateway timeout</html>`,
			wantPresent: "gateway timeout",
		},
		{
			name:        "should withhold a token",
			body:        `{"token":"abc123"}`,
			wantAbsent:  "abc123",
			wantPresent: Redacted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Body(tt.body)
			if tt.wantAbsent != "" && strings.Contains(got, tt.wantAbsent) {
				t.Errorf("Body(%q) = %q, still carries %q", tt.body, got, tt.wantAbsent)
			}
			if tt.wantPresent != "" && !strings.Contains(got, tt.wantPresent) {
				t.Errorf("Body(%q) = %q, want it to carry %q", tt.body, got, tt.wantPresent)
			}
		})
	}
}
