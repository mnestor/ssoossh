//go:build e2e

package e2e

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mnestor/ssoossh/test/e2e/harness"
)

// No test anywhere had ever written a client config file. Every invocation
// in every suite passed --server in an empty temp directory, so the merge
// chain documented at client/config/config.go:28-44 -- defaults, system
// file, user file, local file, --config, flags, enforce, platform policy --
// was proven only by unit tests calling newConfig directly with injected
// search paths. What a real binary does with real files in real locations
// was unverified.
//
// Two layers stay out of reach here, deliberately. defaultSearchPaths
// hardcodes /etc/ssoossh with no environment override, and that must not
// change: `enforce` is the administrator's mechanism for locking settings a
// user cannot alter, so an env var relocating the system directory would be
// a one-line bypass of the whole control. Testing those two needs a
// container with a disposable /etc, not a seam in the product.

// resolvedValue pulls one field out of `ssh config`'s aligned output. The
// command pads labels with %-22s, so the value is everything after the
// label on its line.
func resolvedValue(t *testing.T, out, label string) string {
	t.Helper()

	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, label) {
			return strings.TrimSpace(strings.TrimPrefix(line, label))
		}
	}
	t.Fatalf("no %q line in ssh config output:\n%s", label, out)
	return ""
}

// showConfig runs `ssh config` with the given options and returns its
// stdout, failing the test if the command did not succeed.
func showConfig(t *testing.T, bin string, o harness.ClientOptions) string {
	t.Helper()

	o.Args = append([]string{"ssh", "config"}, o.Args...)
	res := harness.RunClient(t, bin, o)
	if res.ExitCode != 0 {
		t.Fatalf("ssh config failed with exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	return res.Stdout
}

// The chain in one test, each layer adding a file that overrides the one
// below it. Asserting each layer separately would pass even if precedence
// were reversed, as long as each file was read at all.
func TestConfigPrecedence_ShouldLetTheMoreSpecificFileWin(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name string
		opts harness.ClientOptions
		want string
	}{
		{
			name: "user file alone",
			opts: harness.ClientOptions{UserYAML: "key_filename: from-user\n"},
			want: "from-user",
		},
		{
			name: "local file beats user file",
			opts: harness.ClientOptions{
				UserYAML:  "key_filename: from-user\n",
				LocalYAML: "key_filename: from-local\n",
			},
			want: "from-local",
		},
		{
			name: "--config beats both",
			opts: harness.ClientOptions{
				UserYAML:   "key_filename: from-user\n",
				LocalYAML:  "key_filename: from-local\n",
				ConfigYAML: "key_filename: from-flag-file\n",
			},
			want: "from-flag-file",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := tt.opts
			// The server has to come from somewhere for PreRun to reach the
			// CA; it is not the setting under test here.
			opts.Args = []string{"--server", f.Server.BaseURL}
			opts.Env = map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket}

			out := showConfig(t, f.SsoosshBin, opts)

			if got := resolvedValue(t, out, "Key file"); got != tt.want {
				t.Errorf("got key file %q, want %q", got, tt.want)
			}
		})
	}
}

// A flag outranks every file. This is the one layer the suite already
// depended on without ever asserting it: every existing test passes
// --server, and none had a file for it to beat.
func TestConfigPrecedence_ShouldLetFlagsBeatEveryFile(t *testing.T) {
	f := newFixture(t)

	out := showConfig(t, f.SsoosshBin, harness.ClientOptions{
		Args:       []string{"--server", f.Server.BaseURL},
		UserYAML:   "server: https://user.example.invalid\n",
		LocalYAML:  "server: https://local.example.invalid\n",
		ConfigYAML: "server: https://flagfile.example.invalid\n",
		Env:        map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})

	if got := resolvedValue(t, out, "Server"); got != f.Server.BaseURL {
		t.Errorf("got server %q, want the flag value %q", got, f.Server.BaseURL)
	}
}

// A config file with no flag at all: the whole point of having config
// files, and something no test had exercised because every invocation
// carried --server.
func TestConfigPrecedence_ShouldReachTheServerNamedOnlyInAConfigFile(t *testing.T) {
	f := newFixture(t)

	// No --server anywhere on the command line. If the file is not read,
	// PreRun has no server to fetch the CA from and the command fails.
	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:     []string{"ssh", "config"},
		UserYAML: fmt.Sprintf("server: %q\n", f.Server.BaseURL),
		Env:      map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})

	if res.ExitCode != 0 {
		t.Fatalf("ssh config failed with only a config file for the server: exit %d\nstderr:\n%s",
			res.ExitCode, res.Stderr)
	}
	if got := resolvedValue(t, res.Stdout, "Server"); got != f.Server.BaseURL {
		t.Errorf("got server %q, want %q", got, f.Server.BaseURL)
	}
	// Having actually reached the server is what distinguishes this from
	// merely parsing the file: the CA is fetched during PreRun.
	if got := resolvedValue(t, res.Stdout, "CA public key"); got == "(none)" || got == "" {
		t.Errorf("expected a CA fetched from the configured server, got %q", got)
	}
}

// Every setting the resolved-config report exposes, driven from a file.
// Individually cheap, and together they are the first proof that these keys
// survive the round trip from YAML to a running binary at all.
func TestConfigPrecedence_ShouldApplyEachSettingFromAConfigFile(t *testing.T) {
	f := newFixture(t)

	tests := []struct {
		name  string
		yaml  string
		label string
		want  string
	}{
		{name: "key filename", yaml: "key_filename: custom-key\n", label: "Key file", want: "custom-key"},
		{name: "try open browser", yaml: "try_open_browser: true\n", label: "Open browser", want: "enabled"},
		{name: "insecure skip verify", yaml: "insecure_skip_verify: true\n", label: "TLS verification", want: "disabled"},
		{name: "key type ed25519", yaml: "sshkey:\n  type: ed25519\n", label: "Key type", want: "ed25519"},
		{name: "key type ecdsa with size", yaml: "sshkey:\n  type: ecdsa\n  size: 521\n", label: "Key type", want: "ecdsa (521)"},
		{name: "key type rsa with size", yaml: "sshkey:\n  type: rsa\n  size: 4096\n", label: "Key type", want: "rsa (4096)"},
		{name: "fips steering", yaml: "fips: true\n", label: "FIPS steering", want: "enabled"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := showConfig(t, f.SsoosshBin, harness.ClientOptions{
				Args:     []string{"--server", f.Server.BaseURL},
				UserYAML: tt.yaml,
				Env:      map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
			})

			if got := resolvedValue(t, out, tt.label); got != tt.want {
				t.Errorf("got %s %q, want %q", tt.label, got, tt.want)
			}
		})
	}
}

// A config file that will not parse is skipped without a word -- long-
// standing behaviour, and the reason ConfigSource exists. From the outside
// it is indistinguishable from the file not being there, so --debug is the
// only thing that makes it visible, and that is what this pins.
func TestConfigPrecedence_ShouldReportAMalformedConfigFileUnderDebug(t *testing.T) {
	f := newFixture(t)

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:     []string{"ssh", "config", "--server", f.Server.BaseURL, "--debug"},
		UserYAML: "key_filename: [unterminated\n",
		Env:      map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})

	// Skipped, not fatal: the command still runs.
	if res.ExitCode != 0 {
		t.Fatalf("a malformed user config should be skipped, not fatal; got exit %d\nstderr:\n%s",
			res.ExitCode, res.Stderr)
	}
	// The debug report goes to stderr, never stdout -- stdout carries
	// certificates and relayed data for other commands.
	if !strings.Contains(res.Stderr, "ssoossh debug") {
		t.Fatalf("expected a debug report on stderr, got:\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "user file") {
		t.Errorf("expected the user file in the source chain, got:\n%s", res.Stderr)
	}
	if !strings.Contains(res.Stderr, "error") {
		t.Errorf("expected the malformed user file to be reported as an error, got:\n%s", res.Stderr)
	}
	// And the setting it would have provided must not have taken effect.
	if got := resolvedValue(t, res.Stdout, "Key file"); got == "[unterminated" {
		t.Error("a value from the malformed file reached the resolved config")
	}
}

// --debug is a diagnostic aid for invocations whose command line is not
// yours to edit -- an ssh_config Match exec line, a cron entry -- which is
// exactly why the environment variable exists alongside the flag. Both
// routes have to work or the one that matters most is the one that does not.
func TestConfigPrecedence_ShouldEnableTheDebugReportFromTheEnvironment(t *testing.T) {
	f := newFixture(t)

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args: []string{"ssh", "config", "--server", f.Server.BaseURL},
		Env: map[string]string{
			"SSH_AUTH_SOCK": f.Agent.Socket,
			"SSOOSSH_DEBUG": "1",
		},
	})

	if res.ExitCode != 0 {
		t.Fatalf("ssh config failed with exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stderr, "ssoossh debug") {
		t.Errorf("expected SSOOSSH_DEBUG to produce a debug report on stderr, got:\n%s", res.Stderr)
	}
	if strings.Contains(res.Stdout, "ssoossh debug") {
		t.Error("the debug report reached stdout, which other commands use for data")
	}
}

// The report has to name the sources in application order, since that order
// is the whole explanation of why a setting has the value it does.
func TestConfigPrecedence_ShouldListEverySourceInApplicationOrder(t *testing.T) {
	f := newFixture(t)

	res := harness.RunClient(t, f.SsoosshBin, harness.ClientOptions{
		Args:       []string{"ssh", "config", "--server", f.Server.BaseURL, "--debug"},
		UserYAML:   "key_filename: from-user\n",
		LocalYAML:  "key_filename: from-local\n",
		ConfigYAML: "key_filename: from-flag-file\n",
		Env:        map[string]string{"SSH_AUTH_SOCK": f.Agent.Socket},
	})
	if res.ExitCode != 0 {
		t.Fatalf("ssh config failed with exit %d\nstderr:\n%s", res.ExitCode, res.Stderr)
	}

	// In order, each appearing after the previous one.
	want := []string{"embedded defaults", "system file", "user file", "local file", "--config", "command-line flags", "enforce", "platform policy"}
	at := 0
	for _, label := range want {
		idx := strings.Index(res.Stderr[at:], label)
		if idx < 0 {
			t.Fatalf("source %q missing or out of order in the debug report:\n%s", label, res.Stderr)
		}
		at += idx + len(label)
	}

	// enforce and platform policy are administrator locks and are marked as
	// such, because "last one wins" describes the mechanism but not that a
	// user cannot argue with those two.
	if !strings.Contains(res.Stderr, "administrator lock") {
		t.Errorf("expected the admin-lock legend in the debug report, got:\n%s", res.Stderr)
	}
}
