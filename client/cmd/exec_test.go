package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"github.com/mnestor/ssoossh/internal/errs"
)

// newTestExec builds the real command tree (same as newExec) but with the
// root's config/agent seams stubbed out, so Execute exercises simplecobra's
// actual tree compilation and ancestor-PreRun behavior end-to-end, without
// touching a real ssh-agent or config file on disk.
func newTestExec(t *testing.T) *rootExecFixture {
	t.Helper()
	root := &RootCommand{
		newConfig:    func(cmd *cobra.Command) (*config.Config, error) { return &config.Config{}, nil },
		newAPIClient: func(cfg *config.Config) (api.Client, error) { return &fakeAPIClient{}, nil },
		newSSHAgent:  func() (agent.Agent, error) { return &fakeAgent{}, nil },
		newFileAgent: func(path string) (agent.Agent, error) { return &fakeAgent{}, nil },
	}
	root.commands = []simplecobra.Commander{newCACommand(), newSSHCommand(), newHostCommand(), newServiceCommand(), newVersionCommand()}
	x, err := simplecobra.New(root)
	if err != nil {
		t.Fatalf("failed to build command tree: %v", err)
	}
	return &rootExecFixture{root: root, exec: x}
}

type rootExecFixture struct {
	root *RootCommand
	exec *simplecobra.Exec
}

func TestExecuteEndToEnd(t *testing.T) {
	tests := []struct {
		name string
		args []string
		// wantErr, if set, is checked with errors.Is instead of the default
		// errs.NotImplementedError expectation — for commands that now have
		// a real implementation with its own specific failure modes.
		wantErr error
		// wantNilErr marks a command that's expected to succeed end-to-end.
		wantNilErr bool
	}{
		{name: "ca", args: []string{"ca"}, wantNilErr: true},
		// login reaches its real implementation and fails at the outcome:
		// the stub client resolves the request with nothing in it. What is
		// being asserted here is that the tree runs it, not what it says.
		{name: "ssh login", args: []string{"ssh", "login"}, wantErr: errors.New("the certificate request resolved with no outcome")},
		{name: "ssh logout", args: []string{"ssh", "logout"}, wantNilErr: true},
		{name: "ssh proxycommand with no command", args: []string{"ssh", "proxycommand"}, wantErr: errProxyCommandRequiresArgs},
		{name: "ssh inspect", args: []string{"ssh", "inspect"}, wantNilErr: true},
		{name: "ssh config", args: []string{"ssh", "config"}, wantNilErr: true},
		{name: "host principals with no args", args: []string{"host", "principals"}, wantErr: errors.New("usage: ssoossh host principals <username>")},
		{name: "host mapping list", args: []string{"host", "mapping", "list"}, wantNilErr: true},
		{name: "service enroll with no key source", args: []string{"service", "enroll"}, wantErr: errors.New("--key is required")},
		{name: "service retrieve with no code", args: []string{"service", "retrieve"}, wantErr: errors.New("--code is required")},
		{name: "version", args: []string{"version"}, wantNilErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newTestExec(t)

			_, err := f.exec.Execute(context.Background(), tt.args)

			switch {
			case tt.wantNilErr:
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			case tt.wantErr != nil:
				if err == nil || err.Error() != tt.wantErr.Error() {
					t.Fatalf("expected error %q, got %v", tt.wantErr, err)
				}
			default:
				if !errors.Is(err, &errs.NotImplementedError{}) {
					t.Fatalf("expected a errs.NotImplementedError, got %v", err)
				}
			}
		})
	}
}

// TestExecuteBindsLeafLocalFlagsThroughToNewConfig is the regression guard
// for PreRun passing runner.CobraCommand rather than this.CobraCommand:
// --key-type/--key-size are local to `ssh login`, registered on that leaf's
// own cobra.Command, not root's. If PreRun ever went back to reading
// this.CobraCommand (root's own), cmd.Flags().Lookup below would find
// nothing and this test would fail.
func TestExecuteBindsLeafLocalFlagsThroughToNewConfig(t *testing.T) {
	var gotCmd *cobra.Command
	root := &RootCommand{
		newConfig: func(cmd *cobra.Command) (*config.Config, error) {
			gotCmd = cmd
			return &config.Config{}, nil
		},
		newAPIClient: func(cfg *config.Config) (api.Client, error) { return &fakeAPIClient{}, nil },
		newSSHAgent:  func() (agent.Agent, error) { return &fakeAgent{}, nil },
		newFileAgent: func(path string) (agent.Agent, error) { return &fakeAgent{}, nil },
	}
	root.commands = []simplecobra.Commander{newCACommand(), newSSHCommand(), newHostCommand(), newServiceCommand(), newVersionCommand()}
	x, err := simplecobra.New(root)
	if err != nil {
		t.Fatalf("failed to build command tree: %v", err)
	}

	_, _ = x.Execute(context.Background(), []string{"ssh", "login", "--key-type", "ecdsa", "--key-size", "384"})

	if gotCmd == nil {
		t.Fatal("newConfig was never called")
	}
	keyType := gotCmd.Flags().Lookup("key-type")
	if keyType == nil {
		t.Fatal("expected the cmd passed to newConfig to carry ssh login's local --key-type flag")
	}
	if keyType.Value.String() != "ecdsa" {
		t.Errorf("got --key-type %q, want %q", keyType.Value.String(), "ecdsa")
	}
	keySize := gotCmd.Flags().Lookup("key-size")
	if keySize == nil {
		t.Fatal("expected the cmd passed to newConfig to carry ssh login's local --key-size flag")
	}
	if keySize.Value.String() != "384" {
		t.Errorf("got --key-size %q, want %q", keySize.Value.String(), "384")
	}
}

func TestExecuteSurfacesInitErr(t *testing.T) {
	root := &RootCommand{
		newConfig:    func(cmd *cobra.Command) (*config.Config, error) { return nil, errors.New("bad config") },
		newAPIClient: func(cfg *config.Config) (api.Client, error) { return &fakeAPIClient{}, nil },
		newSSHAgent:  func() (agent.Agent, error) { return &fakeAgent{}, nil },
		newFileAgent: func(path string) (agent.Agent, error) { return &fakeAgent{}, nil },
	}
	root.commands = []simplecobra.Commander{newCACommand()}
	x, err := simplecobra.New(root)
	if err != nil {
		t.Fatalf("failed to build command tree: %v", err)
	}

	_, err = x.Execute(context.Background(), []string{"ca"})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if errors.Is(err, &errs.NotImplementedError{}) {
		t.Fatal("expected root InitErr to be surfaced instead of a errs.NotImplementedError")
	}
}

// TestVersionCommandIgnoresInitErr asserts version keeps working when
// config/API/agent setup fails -- unlike every other leaf command (see
// TestExecuteSurfacesInitErr for the same failing config against ca), since
// that's exactly the situation a bug reporter needs `ssoossh version` to
// still answer in.
func TestVersionCommandIgnoresInitErr(t *testing.T) {
	root := &RootCommand{
		newConfig:    func(cmd *cobra.Command) (*config.Config, error) { return nil, errors.New("bad config") },
		newAPIClient: func(cfg *config.Config) (api.Client, error) { return &fakeAPIClient{}, nil },
		newSSHAgent:  func() (agent.Agent, error) { return &fakeAgent{}, nil },
		newFileAgent: func(path string) (agent.Agent, error) { return &fakeAgent{}, nil },
	}
	root.commands = []simplecobra.Commander{newVersionCommand()}
	x, err := simplecobra.New(root)
	if err != nil {
		t.Fatalf("failed to build command tree: %v", err)
	}

	if _, err := x.Execute(context.Background(), []string{"version"}); err != nil {
		t.Fatalf("expected version to succeed despite a failing root init, got %v", err)
	}
}
