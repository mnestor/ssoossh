package tlsutils

// Test methodology: Table-driven tests with t.Parallel() for parallelization.
// Tests verify TLS minimum version name resolution with tolerant parsing
// (e.g. "TLS1.2", "tls12", "VersionTLS12" all work). Tests also verify
// deprecation warnings for old TLS versions per RFC 8996.

import (
	"bytes"
	"crypto/tls"
	"log/slog"
	"testing"
)

func TestMinVersion_ShouldResolveKnownNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  uint16
	}{
		{"should resolve tls10", "tls10", tls.VersionTLS10},
		{"should resolve tls1.0", "tls1.0", tls.VersionTLS10},
		{"should resolve versiontls10", "versiontls10", tls.VersionTLS10},
		{"should resolve tls11", "tls11", tls.VersionTLS11},
		{"should resolve tls1.1", "tls1.1", tls.VersionTLS11},
		{"should resolve tls12", "tls12", tls.VersionTLS12},
		{"should resolve tls1.2", "tls1.2", tls.VersionTLS12},
		{"should resolve tls13", "tls13", tls.VersionTLS13},
		{"should resolve tls1.3", "tls1.3", tls.VersionTLS13},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := MinVersion(tt.input)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMinVersion_ShouldIgnoreCaseAndNonDigitCharacters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
	}{
		{"should resolve uppercase TLS1.2", "TLS1.2"},
		{"should resolve mixed case VersionTLS12", "VersionTLS12"},
		{"should resolve bare digits", "12"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := MinVersion(tt.input)
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got != tls.VersionTLS12 {
				t.Errorf("got %d, want %d", got, tls.VersionTLS12)
			}
		})
	}
}

func TestMinVersion_ShouldErrorWhenNameUnknown(t *testing.T) {
	t.Parallel()

	_, err := MinVersion("tls99")
	if err == nil {
		t.Fatal("expected an error for unknown version name, got nil")
	}
}

func TestMinVersion_ShouldErrorWhenInputEmpty(t *testing.T) {
	t.Parallel()

	_, err := MinVersion("")
	if err == nil {
		t.Fatal("expected an error for empty version name, got nil")
	}
}

func TestMinVersion_ShouldWarnWhenVersionDeprecated(t *testing.T) {
	// Reads/writes the global slog default logger, so it cannot run in
	// parallel with other tests that also depend on it.
	tests := []struct {
		name        string
		input       string
		wantWarning bool
	}{
		{"should warn when tls10 selected", "tls10", true},
		{"should warn when tls11 selected", "tls11", true},
		{"should not warn when tls12 selected", "tls12", false},
		{"should not warn when tls13 selected", "tls13", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			restore := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
			t.Cleanup(func() { slog.SetDefault(restore) })

			if _, err := MinVersion(tt.input); err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			gotWarning := bytes.Contains(buf.Bytes(), []byte("level=WARN"))
			if gotWarning != tt.wantWarning {
				t.Errorf("got warning=%v, want %v (log output: %s)", gotWarning, tt.wantWarning, buf.String())
			}
		})
	}
}

func TestMinVersion_ShouldErrorWhenDigitsSpanMultipleVersions(t *testing.T) {
	t.Parallel()

	// The leftover "and" isn't a known token, so this must not silently
	// coalesce into a match for either TLS 1.2 or TLS 1.3.
	_, err := MinVersion("tls1-2-and-1-3")
	if err == nil {
		t.Fatal("expected an error for ambiguous version name, got nil")
	}
}

func TestMinVersion_ShouldErrorWhenUnrecognizedTextSurroundsValidDigits(t *testing.T) {
	t.Parallel()

	// Only known tokens ("tls", "version", and separators) are stripped, so
	// unrelated words containing a valid version's digits must not be
	// accepted as if they were that version.
	tests := []string{
		"insecure-downgrade-1-2",
		"definitely-not-tls-1-2",
		"atls12",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			t.Parallel()

			_, err := MinVersion(input)
			if err == nil {
				t.Fatalf("expected an error for %q, got nil", input)
			}
		})
	}
}
