package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
)

// debugCommand builds a command carrying the persistent --debug flag, the
// way RootCommand.Init registers it.
func debugCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "ssoossh"}
	cmd.PersistentFlags().Bool(debugFlagName, false, "")
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}
	return cmd
}

func TestDebugEnabled(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  string
		want bool
	}{
		{name: "should be off when neither the flag nor the variable is set", want: false},
		{name: "should be on when the flag is passed", args: []string{"--debug"}, want: true},
		{name: "should be on when the variable is truthy", env: "1", want: true},
		{name: "should be on for a spelled-out variable", env: "true", want: true},
		{name: "should be off when the variable is falsy", env: "0", want: false},
		// A diagnostic aid must never be the reason a login fails, so junk
		// in the variable reads as off rather than as an error.
		{name: "should be off when the variable is not a boolean", env: "yes-please", want: false},
		{name: "should be on when the flag is passed and the variable is falsy", args: []string{"--debug"}, env: "0", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv(debugEnvVar, tt.env)
			}
			if got := debugEnabled(debugCommand(t, tt.args...)); got != tt.want {
				t.Errorf("debugEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

// The report runs from a deferred call in PreRun, which fires on the paths
// where config loading itself failed.
func TestWriteDebugReport_ShouldTolerateAnUnloadedConfig(t *testing.T) {
	var out bytes.Buffer
	writeDebugReport(&out, nil, nil, "ssoossh ssh login", nil)

	if !strings.Contains(out.String(), "configuration never loaded") {
		t.Errorf("report does not say the config was never loaded: %s", out.String())
	}
}

func TestWriteDebugReport_ShouldNameTheInitError(t *testing.T) {
	var out bytes.Buffer
	writeDebugReport(&out, nil, nil, "ssoossh ssh login", errDebugStub)

	if !strings.Contains(out.String(), errDebugStub.Error()) {
		t.Errorf("report does not carry the init error: %s", out.String())
	}
}

// The whole point of the sources section: a file that is present and failed
// to parse has to be distinguishable from one that is simply not there.
func TestWriteDebugReport_ShouldShowWhyAConfigFileWasSkipped(t *testing.T) {
	cfg := &config.Config{Sources: []config.ConfigSource{
		{Label: "user file", Path: "/home/me/.config/ssoossh.yaml", Status: config.SourceError, Err: "yaml: line 4: bad"},
	}}

	var out bytes.Buffer
	writeDebugReport(&out, cfg, nil2Agent(), "ssoossh ssh login", nil)

	if !strings.Contains(out.String(), "yaml: line 4: bad") {
		t.Errorf("report does not explain the skipped file: %s", out.String())
	}
}

func TestWriteDebugReport_ShouldDistinguishAnAbsentFileFromAFailedOne(t *testing.T) {
	cfg := &config.Config{Sources: []config.ConfigSource{
		{Label: "system file", Path: "/etc/ssoossh/ssoossh.yaml", Status: config.SourceAbsent},
	}}

	var out bytes.Buffer
	writeDebugReport(&out, cfg, nil2Agent(), "ssoossh ssh login", nil)

	if !strings.Contains(out.String(), "absent") {
		t.Errorf("report does not mark the missing file absent: %s", out.String())
	}
}

// key_filename does not mean what it looks like: a bare name resolves
// against ~/.ssh, and "~/..." is expanded. Reporting the configured string
// and stat'ing it would call files missing that the agent is using, which
// is the failure that prompted this. The report must show what the agent
// resolved to.
func TestWriteDebugReport_ShouldShowWhatABareKeyFilenameResolvesTo(t *testing.T) {
	cfg := &config.Config{Filename: "id_ssoossh"}

	var out bytes.Buffer
	writeDebugReport(&out, cfg, nil2Agent(), "ssoossh ssh login", nil)

	want, err := agent.ResolveKeyPath("id_ssoossh")
	if err != nil {
		t.Skipf("no home directory available: %v", err)
	}
	if !strings.Contains(out.String(), "resolves to") || !strings.Contains(out.String(), want) {
		t.Errorf("report does not show the resolved key path %s: %s", want, out.String())
	}
}

func TestWriteDebugReport_ShouldExpandATildeKeyFilename(t *testing.T) {
	cfg := &config.Config{Filename: "~/.ssh/id_ssoossh"}

	var out bytes.Buffer
	writeDebugReport(&out, cfg, nil2Agent(), "ssoossh ssh login", nil)

	want, err := agent.ResolveKeyPath("~/.ssh/id_ssoossh")
	if err != nil {
		t.Skipf("no home directory available: %v", err)
	}
	if !strings.Contains(out.String(), want) {
		t.Errorf("report does not expand the tilde to %s: %s", want, out.String())
	}
}

// An absolute path is already what the agent will use, so repeating it as a
// "resolves to" line is noise.
func TestWriteDebugReport_ShouldNotRestateAnAbsoluteKeyFilename(t *testing.T) {
	cfg := &config.Config{Filename: filepath.Join(t.TempDir(), "id_ssoossh")}

	var out bytes.Buffer
	writeDebugReport(&out, cfg, nil2Agent(), "ssoossh ssh login", nil)

	if strings.Contains(out.String(), "resolves to") {
		t.Errorf("report restates an already-absolute key_filename: %s", out.String())
	}
}

// Agent resolution is one of the things that can fail before the report
// runs, so a missing backend must read as unresolved rather than panic.
func TestWriteDebugReport_ShouldReportAnUnresolvedBackend(t *testing.T) {
	var out bytes.Buffer
	writeDebugReport(&out, &config.Config{}, nil2Agent(), "ssoossh ssh login", nil)

	// The same wording `ssh config` uses, since both now go through
	// storageDescription.
	if !strings.Contains(out.String(), "(not initialized)") {
		t.Errorf("report does not mark the backend unresolved: %s", out.String())
	}
}

func TestWriteDebugReport_ShouldNameTheResolvedBackend(t *testing.T) {
	var out bytes.Buffer
	writeDebugReport(&out, &config.Config{}, stubDescriber{kind: "file", backend: "/home/me/.ssh/id"}, "ssoossh ssh login", nil)

	if !strings.Contains(out.String(), "file") || !strings.Contains(out.String(), "/home/me/.ssh/id") {
		t.Errorf("report does not name the resolved backend: %s", out.String())
	}
}

// The report is now the only place resolved settings are answered, so
// everything `ssh config` used to print has to be in it. These moved here
// from ssh_config_test.go along with the printing.

// The whole point of reporting settings: key type and FIPS steering both
// have defaults that depend on the environment, so echoing the file back
// would answer a different question than the one being asked.
func TestWriteDebugSettings_ShouldPrintResolvedValuesNotConfiguredOnes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{
			name: "should resolve an unset key type to its default",
			cfg:  &config.Config{Server: "https://ssh.example.com"},
			want: "ecdsa (384)",
		},
		{
			name: "should report the curve alongside an ecdsa key",
			cfg: &config.Config{
				Server: "https://ssh.example.com",
				SSHKey: config.SSHKeyOptions{Type: config.SSHKeyTypeECDSA, Size: 521},
			},
			want: "ecdsa (521)",
		},
		{
			name: "should say so rather than omit an unusable combination",
			cfg:  &config.Config{SSHKey: config.SSHKeyOptions{Type: config.SSHKeyTypeECDSA, Size: 111}},
			want: "unresolvable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var out bytes.Buffer
			writeDebugSettings(&out, tt.cfg, stubDescriber{kind: "ssh-agent", backend: "stub"})
			if !strings.Contains(out.String(), tt.want) {
				t.Errorf("got %q, want it to contain %q", out.String(), tt.want)
			}
		})
	}
}

// Insecure TLS is the one setting someone might leave on by accident after
// testing against a self-signed server, so it has to read plainly.
func TestWriteDebugSettings_ShouldReportInsecureTLSPlainly(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	writeDebugSettings(&out, &config.Config{Server: "https://ssh.example.com", SkipVerifySSL: true}, nil2Agent())

	if !strings.Contains(out.String(), "TLS verification       disabled") {
		t.Errorf("got %q, want TLS verification reported as disabled", out.String())
	}
}

// An unusable key configuration must not swallow the rest of the block: a
// failed startup is when this report is most wanted, so the settings after
// the bad one still have to print.
func TestWriteDebugSettings_ShouldKeepReportingPastAnUnresolvableKey(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	writeDebugSettings(&out, &config.Config{
		Server: "https://ssh.example.com",
		SSHKey: config.SSHKeyOptions{Type: config.SSHKeyTypeECDSA, Size: 111},
	}, nil2Agent())

	if !strings.Contains(out.String(), "CA public key") {
		t.Errorf("expected the rest of the settings after an unresolvable key, got:\n%s", out.String())
	}
}

func TestStorageDescription_ShouldReportTheResolvedBackend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ssh  agentDescriber
		want string
	}{
		{name: "no agent resolved", ssh: nil, want: "(not initialized)"},
		{name: "an agent resolved", ssh: stubDescriber{kind: "agent", backend: "ssh-agent"}, want: "agent (ssh-agent)"},
		{name: "file storage resolved", ssh: stubDescriber{kind: "file", backend: "/home/u/.ssh/id_ssoossh"}, want: "file (/home/u/.ssh/id_ssoossh)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := storageDescription(tt.ssh); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The truncation is the part worth pinning: the summary exists so two
// deployments can be compared at a glance, which stops working if it prints
// the whole blob or too little of it to tell two CAs apart.
func TestCASummary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ca   string
		want string
	}{
		{name: "should render nothing when unset", ca: "", want: ""},
		{
			name: "should keep the algorithm and shorten the key material",
			ca:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJirRcsGXT31qUGNbgTkbI6sxq1SbSLN++XEr705S8ko ca@example",
			want: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA…",
		},
		{
			name: "should pass a short value through untouched",
			ca:   "ssh-ed25519 AAAA",
			want: "ssh-ed25519 AAAA",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := caSummary(tt.ca); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// stubDescriber is the narrow view of the agent the report takes.
type stubDescriber struct{ kind, backend string }

func (s stubDescriber) Type() string    { return s.kind }
func (s stubDescriber) Backend() string { return s.backend }

// nil2Agent returns a nil agentDescriber. Written as a helper so the
// argument reads as deliberate rather than as an omission.
func nil2Agent() agentDescriber { return nil }

// errDebugStub stands in for a startup failure.
var errDebugStub = errors.New("test: build API client failed")

// fileState answers "why can this not read my key". Flattening a permission
// problem to "missing" would send the reader looking in the wrong place, so
// the three outcomes have to stay distinguishable.
func TestFileState_ShouldDistinguishPresentMissingAndUnreadable(t *testing.T) {
	dir := t.TempDir()

	present := filepath.Join(dir, "present")
	if err := os.WriteFile(present, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// A path whose parent is not a directory: stat fails with something
	// other than "not exist", which is the third branch.
	notADir := filepath.Join(present, "child")

	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "present reports size and mode", path: present, want: "exists, 5 bytes, mode 600"},
		{name: "missing says so", path: filepath.Join(dir, "absent"), want: "(missing)"},
		{name: "any other error is shown as-is", path: notADir, want: "unreadable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fileState(tt.path); !strings.Contains(got, tt.want) {
				t.Errorf("got %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

// agentForDebug exists so the report never calls Type() on a nil agent. A
// plain r.ssh would hand it a non-nil interface holding a nil pointer
// whenever agent resolution failed -- which is exactly when the report is
// most wanted.
func TestAgentForDebug_ShouldReturnNilWhenNoAgentResolved(t *testing.T) {
	root := &RootCommand{}

	if got := root.agentForDebug(); got != nil {
		t.Errorf("got %v, want nil when no agent was resolved", got)
	}
}

func TestAgentForDebug_ShouldReturnTheAgentWhenOneResolved(t *testing.T) {
	root := &RootCommand{ssh: &fakeAgent{}}

	if got := root.agentForDebug(); got == nil {
		t.Error("expected the resolved agent to be returned")
	}
}
