package cmd

import (
	"context"
	"strings"
	"testing"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
)

// notACommander is a Commander that doesn't implement offlineCommander, to
// pin the default: an undeclared command is online.
type notACommander struct{}

func (notACommander) Name() string                                      { return "plain" }
func (notACommander) Commands() []simplecobra.Commander                 { return nil }
func (notACommander) Init(cd *simplecobra.Commandeer) error             { return nil }
func (notACommander) PreRun(this, runner *simplecobra.Commandeer) error { return nil }
func (notACommander) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return nil
}

func TestIsOffline(t *testing.T) {
	tests := []struct {
		name string
		cmd  simplecobra.Commander
		want bool
	}{
		{name: "should report offline when the command declares it", cmd: &simpleCommand{offline: true}, want: true},
		{name: "should report online when the command declares offline false", cmd: &simpleCommand{}, want: false},
		{name: "should report online when the command does not implement offlineCommander", cmd: notACommander{}, want: false},
		{name: "should report online for a nil command", cmd: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOffline(tt.cmd); got != tt.want {
				t.Fatalf("expected isOffline to be %v, got %v", tt.want, got)
			}
		})
	}
}

// TestOfflineCommandsDeclareOffline pins which commands are offline. A
// command reaching the server when it shouldn't is the defect this seam
// exists to prevent, so the surface is asserted rather than assumed.
func TestOfflineCommandsDeclareOffline(t *testing.T) {
	tests := []struct {
		name string
		cmd  simplecobra.Commander
		want bool
	}{
		{name: "host principals answers from local state only", cmd: newHostPrincipalsCommand(), want: true},
		{name: "host mapping manages local state only", cmd: newHostMappingCommand(), want: true},
		{name: "version prints build-time constants only", cmd: newVersionCommand(), want: true},
		{name: "ca fetches the CA from the server", cmd: newCACommand(), want: false},
		{name: "ssh login obtains a certificate from the server", cmd: newSSHLoginCommand(), want: false},
		{name: "service retrieve redeems an enrollment code", cmd: newServiceRetrieveCommand(), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOffline(tt.cmd); got != tt.want {
				t.Fatalf("expected isOffline to be %v, got %v", tt.want, got)
			}
		})
	}
}

// TestRootCommandPreRunSkipsNetworkForOfflineCommands is the actual
// guarantee: for an offline command, PreRun builds nothing that could reach
// the server, and reports no error for having skipped it.
func TestRootCommandPreRunSkipsNetworkForOfflineCommands(t *testing.T) {
	root := &RootCommand{
		newConfig: func(cmd *cobra.Command) (*config.Config, error) {
			return &config.Config{Server: "https://example.com", UseAgent: true}, nil
		},
		newAPIClient: func(cfg *config.Config) (api.Client, error) {
			t.Fatal("newAPIClient should not be called for an offline command")
			return nil, nil
		},
		newSSHAgent: func() (agent.Agent, error) {
			t.Fatal("newSSHAgent should not be called for an offline command")
			return nil, nil
		},
		newFileAgent: func(path string) (agent.Agent, error) {
			t.Fatal("newFileAgent should not be called for an offline command")
			return nil, nil
		},
	}

	cd := &simplecobra.Commandeer{
		Command:      &simpleCommand{name: "principals", offline: true},
		CobraCommand: &cobra.Command{Use: "principals"},
	}

	if err := root.PreRun(cd, cd); err != nil {
		t.Fatalf("PreRun returned an error, expected it to be captured in InitErr instead: %v", err)
	}
	if root.InitErr() != nil {
		t.Fatalf("expected InitErr to be nil, got %v", root.InitErr())
	}
}

// TestRootCommandPreRunLoadsConfigForOfflineCommands guards the other half:
// skipping the network must not skip config, since an offline command still
// has to know where its local files live.
func TestRootCommandPreRunLoadsConfigForOfflineCommands(t *testing.T) {
	root := &RootCommand{
		newConfig: func(cmd *cobra.Command) (*config.Config, error) {
			return &config.Config{Filename: "ssoossh"}, nil
		},
		newAPIClient: func(cfg *config.Config) (api.Client, error) { return &fakeAPIClient{}, nil },
		newSSHAgent:  func() (agent.Agent, error) { return &fakeAgent{}, nil },
		newFileAgent: func(path string) (agent.Agent, error) { return &fakeAgent{}, nil },
	}

	cd := &simplecobra.Commandeer{
		Command:      &simpleCommand{name: "principals", offline: true},
		CobraCommand: &cobra.Command{Use: "principals"},
	}

	if err := root.PreRun(cd, cd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root.Config() == nil {
		t.Fatal("expected Config to be loaded for an offline command, got nil")
	}
}

// TestRootCommandPreRunLeavesNoAgentForOfflineCommands documents that an
// offline command gets no key storage: nothing decides where keys live for
// a command that never obtains one.
func TestRootCommandPreRunLeavesNoAgentForOfflineCommands(t *testing.T) {
	root := &RootCommand{
		newConfig: func(cmd *cobra.Command) (*config.Config, error) {
			return &config.Config{UseAgent: true}, nil
		},
		newAPIClient: func(cfg *config.Config) (api.Client, error) { return &fakeAPIClient{}, nil },
		newSSHAgent:  func() (agent.Agent, error) { return &fakeAgent{}, nil },
		newFileAgent: func(path string) (agent.Agent, error) { return &fakeAgent{}, nil },
	}

	cd := &simplecobra.Commandeer{
		Command:      &simpleCommand{name: "principals", offline: true},
		CobraCommand: &cobra.Command{Use: "principals"},
	}

	if err := root.PreRun(cd, cd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if root.Agent() != nil {
		t.Fatalf("expected Agent to be nil for an offline command, got %#v", root.Agent())
	}
}

// TestRootCommandPreRunInstallsRefusingClientForOfflineCommands checks the
// backstop: an offline command that later grows a server call gets a named
// error, not a nil-pointer panic and not a silent request.
func TestRootCommandPreRunInstallsRefusingClientForOfflineCommands(t *testing.T) {
	root := &RootCommand{
		newConfig: func(cmd *cobra.Command) (*config.Config, error) {
			return &config.Config{}, nil
		},
		newAPIClient: func(cfg *config.Config) (api.Client, error) { return &fakeAPIClient{}, nil },
		newSSHAgent:  func() (agent.Agent, error) { return &fakeAgent{}, nil },
		newFileAgent: func(path string) (agent.Agent, error) { return &fakeAgent{}, nil },
	}

	cd := &simplecobra.Commandeer{
		Command:      &simpleCommand{name: "principals", offline: true},
		CobraCommand: &cobra.Command{Use: "principals"},
	}

	if err := root.PreRun(cd, cd); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err := root.API().GetCA(context.Background())
	if err == nil {
		t.Fatal("expected GetCA to refuse for an offline command, got nil error")
	}
	if !strings.Contains(err.Error(), "principals") {
		t.Fatalf("expected the refusal to name the offline command, got %q", err)
	}
}

// TestOfflineAPIClientRefusesEveryCall walks the whole api.Client surface:
// one unrefused method is a hole in the guarantee.
func TestOfflineAPIClientRefusesEveryCall(t *testing.T) {
	c := &offlineAPIClient{command: "ssoossh host principals"}
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{name: "should refuse GetCA", call: func() error { _, err := c.GetCA(ctx); return err }},
		{name: "should refuse CreateUserRequest", call: func() error {
			_, err := c.CreateUserRequest(ctx, "key", "user", "host", api.RequestedOptions{})
			return err
		}},
		{name: "should refuse CreateServiceEnrollment", call: func() error {
			_, err := c.CreateServiceEnrollment(ctx, "key", api.RequestedOptions{})
			return err
		}},
		{name: "should refuse CreatePAMRequest", call: func() error {
			_, err := c.CreatePAMRequest(ctx, "key", "user", api.RequestedOptions{})
			return err
		}},
		{name: "should refuse AwaitCertificate", call: func() error {
			_, err := c.AwaitCertificate(ctx, &api.PendingRequest{})
			return err
		}},
		{name: "should refuse RetrieveServiceCertificate", call: func() error {
			_, err := c.RetrieveServiceCertificate(ctx, "code")
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !strings.Contains(err.Error(), "ssoossh host principals") {
				t.Fatalf("expected the refusal to name the offline command, got %q", err)
			}
		})
	}
}

// TestOfflineAPIClientRefusalNamesTheCall keeps the error useful for
// whoever hits it: it has to say which call was attempted.
func TestOfflineAPIClientRefusalNamesTheCall(t *testing.T) {
	c := &offlineAPIClient{command: "ssoossh version"}

	err := c.refuse("GetCA")
	if err == nil || !strings.Contains(err.Error(), "GetCA") {
		t.Fatalf("expected the refusal to name the attempted call, got %v", err)
	}
}
