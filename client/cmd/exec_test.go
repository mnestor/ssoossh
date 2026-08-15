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
	root.commands = []simplecobra.Commander{newCACommand(), newSSHCommand(), newHostCommand(), newServiceCommand()}
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
		{name: "host sign", args: []string{"host", "sign"}},
		{name: "host renew", args: []string{"host", "renew"}},
		{name: "host sync", args: []string{"host", "sync"}},
		{name: "host principals", args: []string{"host", "principals"}},
		{name: "service enroll", args: []string{"service", "enroll"}},
		{name: "service retrieve", args: []string{"service", "retrieve"}},
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
