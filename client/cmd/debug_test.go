package cmd

import (
	"bytes"
	"errors"
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

	if !strings.Contains(out.String(), "not resolved") {
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

// stubDescriber is the narrow view of the agent the report takes.
type stubDescriber struct{ kind, backend string }

func (s stubDescriber) Type() string    { return s.kind }
func (s stubDescriber) Backend() string { return s.backend }

// nil2Agent returns a nil agentDescriber. Written as a helper so the
// argument reads as deliberate rather than as an omission.
func nil2Agent() agentDescriber { return nil }

// errDebugStub stands in for a startup failure.
var errDebugStub = errors.New("test: build API client failed")
