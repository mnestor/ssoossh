package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// captureWarnings points slog at a buffer for the test and returns it.
// Not parallel-safe: slog's default is process-wide.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	saved := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(saved) })
	return &buf
}

// TestNewConfig_UnknownSettingWarnings covers settings the client does not
// read, which used to be ignored silently.
func TestNewConfig_UnknownSettingWarnings(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "should warn about a misspelt setting and suggest the real one",
			config: "serverr: https://ssh.example.com\n",
			want:   `unknown setting \"serverr\"`,
		},
		{
			name:   "should suggest the setting a misspelling is closest to",
			config: "serverr: https://ssh.example.com\n",
			want:   `did you mean \"server\"?`,
		},
		{
			name:   "should warn that forbidden extensions are ignored outside the enforce file",
			config: "forbidden_certificate_extensions:\n  - permit-pty\n",
			want:   "forbidden_certificate_extensions in ",
		},
		{
			name:   "should warn about an unknown key inside a known section",
			config: "sshkey:\n  tpye: rsa\n",
			want:   `unknown setting \"sshkey.tpye\"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logged := captureWarnings(t)
			cmd := newConfigCommand(t, "--config", writeConfig(t, tt.config))

			if _, err := newConfig(cmd, testPaths(t.TempDir()), noPolicy); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(logged.String(), tt.want) {
				t.Errorf("logged %q, want it to contain %q", logged.String(), tt.want)
			}
		})
	}
}

func TestNewConfig_ShouldNotWarnAboutKnownSettings(t *testing.T) {
	logged := captureWarnings(t)
	cmd := newConfigCommand(t, "--config", writeConfig(t,
		"server: https://ssh.example.com\nsshkey:\n  type: ecdsa\ncertificate_extensions:\n  no_pty: true\nrequest_timeout: 30s\n"))

	if _, err := newConfig(cmd, testPaths(t.TempDir()), noPolicy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logged.Len() != 0 {
		t.Errorf("expected no warnings, got %q", logged.String())
	}
}

func TestNewConfig_ShouldAcceptForbiddenExtensionsInTheEnforceFile(t *testing.T) {
	logged := captureWarnings(t)
	sysDir := writeSystemConfig(t, map[string]string{
		"ssoossh.yaml": "enforce: locked.yaml\n",
		"locked.yaml":  "forbidden_certificate_extensions:\n  - permit-pty\n",
	})

	if _, err := newConfig(newConfigCommand(t), testPaths(sysDir), noPolicy); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if logged.Len() != 0 {
		t.Errorf("expected no warnings, got %q", logged.String())
	}
}

func TestEditDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"server", "server", 0},
		{"serverr", "server", 1},
		{"sevrer", "server", 2},
		{"", "abc", 3},
	}
	for _, tt := range tests {
		t.Run("should measure "+tt.a+" against "+tt.b, func(t *testing.T) {
			if got := editDistance(tt.a, tt.b); got != tt.want {
				t.Errorf("editDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestClosestSetting_ShouldSuggestNothingForAFarOffKey(t *testing.T) {
	if got := closestSetting("completely_unrelated", knownSettings()); got != "" {
		t.Errorf("got %q, want no suggestion", got)
	}
}
