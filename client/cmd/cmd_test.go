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
				return &config.Config{Server: "https://example.com"}, nil
			},
			newAPIClient: defaultNewAPIClient,
			newSSHAgent:  func() (agent.Agent, error) { return &fakeAgent{}, nil },
			newFileAgent: func(path string) (agent.Agent, error) {
				return nil, errors.New("should not be called")
			},
			wantAgent: true,
		},
		{
			name:         "should fall back to file agent when ssh-agent connection fails",
			newConfig:    func(cmd *cobra.Command) (*config.Config, error) { return &config.Config{}, nil },
			newAPIClient: defaultNewAPIClient,
			newSSHAgent:  func() (agent.Agent, error) { return nil, errors.New("no ssh-agent") },
			newFileAgent: func(path string) (agent.Agent, error) {
				return &fakeAgent{}, nil
			},
			wantAgent: true,
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
			name:         "should set initErr when both ssh-agent and file agent fail",
			newConfig:    func(cmd *cobra.Command) (*config.Config, error) { return &config.Config{}, nil },
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
func (f *fakeAgent) AddKeypair(kp *keypair.SshKeypair) error         { return nil }
func (f *fakeAgent) CleanupAgent() error                             { return nil }

// fakeAPIClient is a minimal api.Client stub for tests that only need a
// non-nil value, not real server calls.
type fakeAPIClient struct{}

func (f *fakeAPIClient) GetCA(ctx context.Context) (string, error) { return "", nil }
func (f *fakeAPIClient) RequestUserCertificate(ctx context.Context, publicKey string, opts api.RequestedOptions) (*api.CertificateResult, error) {
	return nil, nil
}
func (f *fakeAPIClient) RequestHostCertificate(ctx context.Context, publicKey, hostname string, opts api.RequestedOptions) (*api.CertificateResult, error) {
	return nil, nil
}
func (f *fakeAPIClient) EnrollService(ctx context.Context, publicKey string, opts api.RequestedOptions) (*api.CertificateResult, error) {
	return nil, nil
}
func (f *fakeAPIClient) RetrieveServiceCertificate(ctx context.Context, code string) (string, error) {
	return "", nil
}

var _ api.Client = (*fakeAPIClient)(nil)

var _ agent.Agent = (*fakeAgent)(nil)
