// Package cmd wires up the ssoossh client's cobra command tree using
// bep/simplecobra, mirroring the structure Hugo uses for its own CLI: a
// single flat package, a root Commander holding shared state (config, API
// client, SSH agent), and leaf/group commands reaching that state at
// runtime via cd.Root.Command.(*RootCommand) rather than an import-time
// dependency back to this package.
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/bep/simplecobra"
	"github.com/italypaleale/go-kit/signals"
	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/client/config"
	"github.com/mnestor/ssoossh/internal/api"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"
)

var _ simplecobra.Commander = (*RootCommand)(nil)

// RootCommand is the root `ssoossh` command. Config, the API client, and the
// SSH agent are held here; PreRun populates them once per process (a CLI
// process calls Execute exactly once, so unlike the earlier sync.Once-based
// version, no extra guard is needed), and other commands reach them via
// cd.Root.Command.(*RootCommand).
type RootCommand struct {
	cfg     *config.Config
	api     api.Client
	ssh     agent.Agent
	initErr error

	// newConfig, newAPIClient, newSSHAgent, and newFileAgent are overridable
	// seams for PreRun, so tests can inject fakes instead of hitting a real
	// server/ssh-agent/config file on disk.
	newConfig    func(cmd *cobra.Command) (*config.Config, error)
	newAPIClient func(cfg *config.Config) (api.Client, error)
	newSSHAgent  func() (agent.Agent, error)
	newFileAgent func(path string) (agent.Agent, error)

	commands []simplecobra.Commander
}

// Name implements simplecobra.Commander.
func (r *RootCommand) Name() string { return "ssoossh" }

// Commands implements simplecobra.Commander.
func (r *RootCommand) Commands() []simplecobra.Commander { return r.commands }

// Init implements simplecobra.Commander.
func (r *RootCommand) Init(cd *simplecobra.Commandeer) error {
	cmd := cd.CobraCommand
	cmd.Short = "The ssoossh client — turns an OIDC login into a short-lived SSH certificate, from your ssh_config."
	cmd.Long = "The ssoossh client wires SSO into your existing SSH workflow. Configured " +
		"as a ProxyCommand or Match exec in ssh_config, it generates a fresh keypair, " +
		"hands the public key to the ssoossh server, opens your browser for OIDC " +
		"authentication, and loads the signed certificate into your ssh-agent — or writes " +
		"key and certificate files when no agent is available. Private keys never leave " +
		"the machine. Valid certificates are reused until they expire, so authenticating " +
		"once could cover a workday rather than every connection. Runs on macOS, Linux, " +
		"and Windows, and also handles host enrollment, per-host principal mapping for " +
		"AuthorizedPrincipalsCommand, and service-account certificates for unattended " +
		"jobs."

	cmd.PersistentFlags().StringP("config", "c", "", "path to the ssoossh config file")
	cmd.PersistentFlags().String("server", "", "server address including scheme (e.g. \"https://example.com\") assumes https if omited.")

	return nil
}

// PreRun implements simplecobra.Commander. It loads config and constructs
// the API client and SSH agent. Failures are captured in initErr rather
// than returned, so cobra still prints the invoked subcommand's usage
// instead of the root's; leaf commands check InitErr() before doing
// anything that needs Config/API/Agent.
func (r *RootCommand) PreRun(this, runner *simplecobra.Commandeer) error {
	cfg, err := r.newConfig(this.CobraCommand)
	if err != nil {
		r.initErr = fmt.Errorf("load config: %w", err)
		return nil
	}
	r.cfg = cfg

	apiClient, err := r.newAPIClient(cfg)
	if err != nil {
		r.initErr = fmt.Errorf("build API client: %w", err)
		return nil
	}
	r.api = apiClient
	if cfg.CAPubkey == "" {
		cfg.CAPubkey, err = apiClient.GetCA(runner.CobraCommand.Context())
		if err != nil {
			r.initErr = fmt.Errorf("get CA public key: %w", err)
			return nil
		}
	}

	a, err := r.resolveAgent(cfg)
	if err != nil {
		r.initErr = err
		return nil
	}
	r.ssh = a
	r.initErr = r.ssh.SetCA(cfg.CAPubkey)

	return nil
}

// resolveAgent decides where keys and certificates are kept, honoring the
// two settings that describe it:
//
//   - use_agent states the preference. Turning it off means "do not touch my
//     ssh-agent", so a running agent is not consulted at all — quietly using
//     one anyway would be exactly the thing the setting exists to prevent.
//   - fallback_file_agent decides what happens when an agent was wanted but
//     is unreachable: key files, or fail closed.
func (r *RootCommand) resolveAgent(cfg *config.Config) (agent.Agent, error) {
	if !cfg.UseAgent {
		a, err := r.newFileAgent(cfg.Filename)
		if err != nil {
			return nil, fmt.Errorf("open key file storage: %w", err)
		}
		return a, nil
	}

	a, err := r.newSSHAgent()
	if err == nil {
		return a, nil
	}
	if !cfg.FallbackFileAgent {
		return nil, fmt.Errorf("connect to ssh-agent: %w", err)
	}

	slog.Warn("failed to connect to ssh-agent, falling back to file-based storage", "error", err)
	a, err = r.newFileAgent(cfg.Filename)
	if err != nil {
		return nil, fmt.Errorf("open ssh agent: %w", err)
	}
	return a, nil
}

// Run implements simplecobra.Commander. The root command has no action of
// its own — invoked bare, it just prints help — only its subcommands do
// real work.
func (r *RootCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	return cd.CobraCommand.Help()
}

// Config returns the loaded client configuration. Only valid once InitErr()
// is nil.
func (r *RootCommand) Config() *config.Config { return r.cfg }

// API returns the configured API client. Only valid once InitErr() is nil.
func (r *RootCommand) API() api.Client { return r.api }

// Agent returns the configured SSH agent (live agent or file-backed
// fallback). Only valid once InitErr() is nil.
func (r *RootCommand) Agent() agent.Agent { return r.ssh }

// InitErr reports whether PreRun failed. Subcommand Run functions must
// check this before using Config/API/Agent.
func (r *RootCommand) InitErr() error { return r.initErr }

// newAPIClientFromConfig builds the default api.Client from the loaded
// client config, mapping config.Config's TLS options into api.Config
// (internal/ can't import client/config back, see root CLAUDE.md, so the
// mapping has to happen on this side).
func newAPIClientFromConfig(cfg *config.Config) (api.Client, error) {
	return api.NewClient(api.Config{
		ServerURL:     cfg.Server,
		SkipVerifySSL: cfg.SkipVerifySSL,
	})
}

func newExec() (*simplecobra.Exec, error) {
	root := &RootCommand{
		newConfig:    config.NewConfig,
		newAPIClient: newAPIClientFromConfig,
		newSSHAgent:  agent.NewSSHAgent,
		newFileAgent: agent.NewFileAgent,
	}
	root.commands = []simplecobra.Commander{
		newCACommand(),
		newSSHCommand(),
		newHostCommand(),
		newServiceCommand(),
		newVersionCommand(),
	}
	return simplecobra.New(root)
}

// Execute runs the ssoossh client CLI.
func Execute() {
	ctx := signals.SignalContext(context.Background())

	x, err := newExec()
	if err != nil {
		slog.Error("failed to initialize command tree", "error", err)
		os.Exit(1)
	}

	if _, err := x.Execute(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
