package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
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
	// retrieveCert/retrieveErr drive RetrieveServiceCertificate, and
	// retrieveCalled records whether it was reached — that is how a test
	// tells `service retrieve` skipping a still-valid certificate from it
	// refreshing one.
	retrieveCert   string
	retrieveErr    error
	retrieveCalled bool
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
	f.retrieveCalled = true
	return f.retrieveCert, f.retrieveErr
}

var _ api.Client = (*fakeAPIClient)(nil)

var _ agent.Agent = (*fakeAgent)(nil)

// newAPIClientFromConfig is the mapping from client config into api.Config.
// internal/ cannot import client/config, so this translation exists only
// here and nothing had run it -- a field dropped on this side would be
// invisible until a deployment behaved differently from its configuration.
func TestNewAPIClientFromConfig_ShouldBuildAClientFromTheResolvedConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  *config.Config
	}{
		{name: "verifying TLS", cfg: &config.Config{Server: "https://ssh.example.test"}},
		{name: "skipping TLS verification", cfg: &config.Config{Server: "https://ssh.example.test", SkipVerifySSL: true}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := newAPIClientFromConfig(tt.cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if client == nil {
				t.Fatal("expected a client")
			}
		})
	}
}

func TestNewAPIClientFromConfig_ShouldFailWhenTheServerIsUnusable(t *testing.T) {
	if _, err := newAPIClientFromConfig(&config.Config{Server: ""}); err == nil {
		t.Error("expected an error for a config with no server")
	}
}

// newExec assembles the real tree with the real seams. Nothing had called it
// -- the tests all build their own root -- so a command dropped from the
// list, or a seam left nil, would have compiled and shipped.
func TestNewExec_ShouldBuildTheRealCommandTree(t *testing.T) {
	x, err := newExec()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if x == nil {
		t.Fatal("expected an Exec")
	}
}

// The root command's own Run just prints help: invoked bare, ssoossh has
// nothing to do, and printing usage is the useful answer.
func TestRootCommand_ShouldPrintHelpWhenInvokedBare(t *testing.T) {
	root, err := newManpageRoot()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var out bytes.Buffer
	root.cobraRoot.SetOut(&out)
	root.cobraRoot.SetErr(&out)

	// A real Commandeer, because Run reaches through it for the cobra
	// command to print help from.
	cd := &simplecobra.Commandeer{CobraCommand: root.cobraRoot}
	if err := root.Run(context.Background(), cd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out.String(), "ssoossh") {
		t.Errorf("expected help output naming the program, got:\n%s", out.String())
	}
}
