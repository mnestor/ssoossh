package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/bep/simplecobra"
	"github.com/spf13/cobra"
	xssh "golang.org/x/crypto/ssh"
	xagent "golang.org/x/crypto/ssh/agent"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/keypair"
)

func TestRootCommandPreRun(t *testing.T) {
	defaultNewAPIClient := func(cfg *config.Config) (api.Client, error) { return &fakeAPIClient{}, nil }

	tests := []struct {
		name         string
		newConfig    func(cmd *cobra.Command) (*config.Config, error)
		newAPIClient func(cfg *config.Config) (api.Client, error)
		newSSHAgent  func() (agent.Agent, error)
		newFileAgent func(path string) (agent.Agent, error)
		wantErr      bool
		wantAgent    bool
	}{
		{
			name: "should populate config and agent when ssh-agent connects successfully",
			newConfig: func(cmd *cobra.Command) (*config.Config, error) {
				return &config.Config{Server: "https://example.com", UseAgent: true}, nil
			},
			newAPIClient: defaultNewAPIClient,
			newSSHAgent:  func() (agent.Agent, error) { return &fakeAgent{}, nil },
			newFileAgent: func(path string) (agent.Agent, error) {
				return nil, errors.New("should not be called")
			},
			wantAgent: true,
		},
		{
			name: "should fall back to file agent when ssh-agent connection fails and fallback is enabled",
			newConfig: func(cmd *cobra.Command) (*config.Config, error) {
				return &config.Config{UseAgent: true, FallbackFileAgent: true, Filename: "ssoossh"}, nil
			},
			newAPIClient: defaultNewAPIClient,
			newSSHAgent:  func() (agent.Agent, error) { return nil, errors.New("no ssh-agent") },
			newFileAgent: func(path string) (agent.Agent, error) {
				return &fakeAgent{}, nil
			},
			wantAgent: true,
		},
		{
			name:         "should set initErr when ssh-agent connection fails and fallback is disabled",
			newConfig:    func(cmd *cobra.Command) (*config.Config, error) { return &config.Config{UseAgent: true}, nil },
			newAPIClient: defaultNewAPIClient,
			newSSHAgent:  func() (agent.Agent, error) { return nil, errors.New("no ssh-agent") },
			newFileAgent: func(path string) (agent.Agent, error) {
				return &fakeAgent{}, nil
			},
			wantErr: true,
		},
		{
			// use_agent off means "do not touch my ssh-agent", so a reachable
			// agent must not be consulted — that is the whole setting.
			name: "should use file storage without consulting the agent when use_agent is off",
			newConfig: func(cmd *cobra.Command) (*config.Config, error) {
				return &config.Config{UseAgent: false, Filename: "ssoossh"}, nil
			},
			newAPIClient: defaultNewAPIClient,
			newSSHAgent: func() (agent.Agent, error) {
				return nil, errors.New("should not be called")
			},
			newFileAgent: func(path string) (agent.Agent, error) { return &fakeAgent{}, nil },
			wantAgent:    true,
		},
		{
			name: "should set initErr when use_agent is off and file storage fails",
			newConfig: func(cmd *cobra.Command) (*config.Config, error) {
				return &config.Config{UseAgent: false, Filename: "ssoossh"}, nil
			},
			newAPIClient: defaultNewAPIClient,
			newSSHAgent: func() (agent.Agent, error) {
				return nil, errors.New("should not be called")
			},
			newFileAgent: func(path string) (agent.Agent, error) {
				return nil, errors.New("no file agent")
			},
			wantErr: true,
		},
		{
			name:      "should set initErr when config loading fails",
			newConfig: func(cmd *cobra.Command) (*config.Config, error) { return nil, errors.New("bad config") },
			newAPIClient: func(cfg *config.Config) (api.Client, error) {
				return nil, errors.New("should not be called")
			},
			newSSHAgent: func() (agent.Agent, error) {
				return nil, errors.New("should not be called")
			},
			newFileAgent: func(path string) (agent.Agent, error) {
				return nil, errors.New("should not be called")
			},
			wantErr: true,
		},
		{
			name:      "should set initErr when API client construction fails",
			newConfig: func(cmd *cobra.Command) (*config.Config, error) { return &config.Config{}, nil },
			newAPIClient: func(cfg *config.Config) (api.Client, error) {
				return nil, errors.New("bad server URL")
			},
			newSSHAgent: func() (agent.Agent, error) {
				return nil, errors.New("should not be called")
			},
			newFileAgent: func(path string) (agent.Agent, error) {
				return nil, errors.New("should not be called")
			},
			wantErr: true,
		},
		{
			name: "should set initErr when both ssh-agent and file agent fail",
			newConfig: func(cmd *cobra.Command) (*config.Config, error) {
				return &config.Config{UseAgent: true, FallbackFileAgent: true, Filename: "ssoossh"}, nil
			},
			newAPIClient: defaultNewAPIClient,
			newSSHAgent:  func() (agent.Agent, error) { return nil, errors.New("no ssh-agent") },
			newFileAgent: func(path string) (agent.Agent, error) {
				return nil, errors.New("no file agent")
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &RootCommand{
				newConfig:    tt.newConfig,
				newAPIClient: tt.newAPIClient,
				newSSHAgent:  tt.newSSHAgent,
				newFileAgent: tt.newFileAgent,
			}

			cd := &simplecobra.Commandeer{CobraCommand: &cobra.Command{}}
			if err := root.PreRun(cd, cd); err != nil {
				t.Fatalf("PreRun returned an error, expected it to be captured in InitErr instead: %v", err)
			}

			if tt.wantErr && root.InitErr() == nil {
				t.Fatalf("expected InitErr to be set, got nil")
			}
			if !tt.wantErr && root.InitErr() != nil {
				t.Fatalf("expected InitErr to be nil, got %v", root.InitErr())
			}
			if tt.wantAgent && root.Agent() == nil {
				t.Fatalf("expected Agent to be set, got nil")
			}
		})
	}
}

// fakeAgent is a minimal agent.Agent stub for tests that only need a
// non-nil value, not real ssh-agent behavior.
type fakeAgent struct{}

func (f *fakeAgent) Type() string                                    { return "" }
func (f *fakeAgent) Backend() string                                 { return "" }
func (f *fakeAgent) List(filterByCA bool) ([]*xssh.PublicKey, error) { return nil, nil }
func (f *fakeAgent) Add(key any) error                               { return nil }
func (f *fakeAgent) Remove(key xssh.PublicKey) error                 { return nil }
func (f *fakeAgent) RemoveAll() (int, error)                         { return 0, nil }
func (f *fakeAgent) Signers() ([]xssh.Signer, error)                 { return nil, nil }
func (f *fakeAgent) Close() error                                    { return nil }
func (f *fakeAgent) Agent() xagent.Agent                             { return nil }
func (f *fakeAgent) SetCA(cas ...string) error                       { return nil }
func (f *fakeAgent) Certificates() ([]*xssh.Certificate, error)      { return nil, nil }
func (f *fakeAgent) AddKeypair(kp *keypair.SSHKeypair) error         { return nil }
func (f *fakeAgent) CleanupAgent() error                             { return nil }

// fakeAPIClient is an api.Client stub. Its zero value satisfies tests that
// only need a non-nil client; the function fields let a test drive a
// specific create/wait outcome without a server.
type fakeAPIClient struct {
	pending     *api.PendingRequest
	createErr   error
	result      *api.CertificateResult
	awaitErr    error
	createdWith []string
	// createdWithOpts tracks RequestedOptions for each CreateUserRequest call,
	// so tests can verify the correct extensions are requested.
	createdWithOpts []api.RequestedOptions
	// awaitCalled records whether the wait was reached, so a test can assert
	// a reused certificate short-circuited before any server call.
	awaitCalled bool
	// onAwait runs at the moment the wait begins, which is how a test
	// observes what had already been printed by then.
	onAwait func()
}

func (f *fakeAPIClient) GetCA(ctx context.Context) (string, error) { return "", nil }
func (f *fakeAPIClient) CreateUserRequest(ctx context.Context, publicKey, localUsername, localHostname string, opts api.RequestedOptions) (*api.PendingRequest, error) {
	f.createdWith = append(f.createdWith, publicKey)
	f.createdWithOpts = append(f.createdWithOpts, opts)
	if f.createErr != nil {
		return nil, f.createErr
	}
	if f.pending == nil {
		return &api.PendingRequest{RequestID: "fake", ApprovalURL: "https://ssh.example.test/approve/fake"}, nil
	}
	return f.pending, nil
}
func (f *fakeAPIClient) CreateHostRequest(ctx context.Context, publicKey, hostname string, opts api.RequestedOptions) (*api.PendingRequest, error) {
	return f.CreateUserRequest(ctx, publicKey, "", "", opts)
}
func (f *fakeAPIClient) CreateServiceEnrollment(ctx context.Context, publicKey string, opts api.RequestedOptions) (*api.PendingRequest, error) {
	return f.CreateUserRequest(ctx, publicKey, "", "", opts)
}
func (f *fakeAPIClient) CreatePAMRequest(ctx context.Context, publicKey, username string, opts api.RequestedOptions) (*api.PendingRequest, error) {
	return f.CreateUserRequest(ctx, publicKey, "", "", opts)
}
func (f *fakeAPIClient) AwaitCertificate(ctx context.Context, req *api.PendingRequest) (*api.CertificateResult, error) {
	f.awaitCalled = true
	if f.onAwait != nil {
		f.onAwait()
	}
	return f.result, f.awaitErr
}
func (f *fakeAPIClient) RetrieveServiceCertificate(ctx context.Context, code string) (string, error) {
	return "", nil
}

var _ api.Client = (*fakeAPIClient)(nil)

var _ agent.Agent = (*fakeAgent)(nil)
