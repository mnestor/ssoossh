// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"slices"

	"github.com/spf13/cobra"

	"github.com/mnestor/ssoossh/internal/cmd/ssoossh/ca"
	"github.com/mnestor/ssoossh/internal/cmd/ssoossh/inspect"
	"github.com/mnestor/ssoossh/internal/cmd/ssoossh/ssh"
	"github.com/mnestor/ssoossh/internal/crypto/ssh/agent"

	api "github.com/mnestor/ssoossh/internal/api/client"

	sc "github.com/mnestor/ssoossh/internal/cmd/ssoossh/ssoossh_context"
	config "github.com/mnestor/ssoossh/internal/config/client"
	verInfo "github.com/mnestor/ssoossh/internal/version"
)

//go:embed example.txt
var exampleText string

func init() {
	// I would love it if we didn't need to do this globally
	cobra.EnableTraverseRunHooks = true
}

func NewRootCommand(
	ctx context.Context,
	o io.Writer,
	e io.Writer,
	args []string,
) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:               "ssoossh",
		Short:             "SSO SSH Client",
		Long:              "A client for managing SSH certificates using Single Sign-On (SSO) on Secure Shell (SSH).",
		Example:           exampleText,
		Version:           verInfo.Version,
		PersistentPreRunE: persistentPreRunE,
		SilenceUsage:      true,
	}

	rootCmd.SetContext(ctx)
	rootCmd.SetOut(o)
	rootCmd.SetErr(e)
	rootCmd.SetArgs(args)

	rootCmd.PersistentFlags().StringP("config", "c", "", "configuration file")
	rootCmd.PersistentFlags().StringP("server", "s", "", "server to connect to for SSO SSH certificate retrieval (https://example.com)")
	rootCmd.MarkFlagRequired("server")

	rootCmd.PersistentFlags().String("ca", "", "ssh public key certificate authority (CA) to use for signing ssh keys")
	rootCmd.PersistentFlags().Bool("verify-ssl", true, "verify the SSL trust chain when connecting to the server")
	rootCmd.PersistentFlags().MarkHidden("verify-ssl")
	rootCmd.PersistentFlags().String("ssl-fingerprint", "", "expected SSL fingerprint of the server (SHA256)")
	rootCmd.PersistentFlags().Bool("skip-ca", false, "skip asking the server for the CA, use the one provided in the config file")

	rootCmd.SilenceUsage = true

	rootCmd.AddCommand(
		ssh.NewCommand(),
		ca.NewCommand(),
		inspect.NewCommand(),
		// newProxyCmd(),
	// 	newCaCmd(),
	// 	newInspectCmd(),
	// 	newLogoutCmd(),
	// 	newProxyCmd(),
	// 	newLoginCmd(),
	// 	newHostCmd(),
	// 	newServiceCmd(),
	)

	rootCmd.SetVersionTemplate(
		fmt.Sprintf(`Version: %s
Build Time: %s
Commit: %s
Built By: %s
APIPath: %s
`,
			verInfo.Version,
			verInfo.Date,
			verInfo.Commit,
			verInfo.BuiltBy,
			verInfo.ApiPath,
		))

	return rootCmd
}

func persistentPreRunE(cmd *cobra.Command, args []string) error {
	cfg, err := config.NewConfig(cmd)
	if err != nil {
		return err
	}

	// add config to context
	// dereference the pointer since we don't care about modifying
	// the original config after this point
	ctx := cmd.Context()

	// add apiClient to context if it doesn't exist already
	apiClient := api.NewClient(
		context.WithValue(ctx, sc.ContextKeyConfig, *cfg),
		cfg.Server)
	if ctx.Value(sc.ContextKeyAPIClient) == nil {
		ctx = context.WithValue(ctx, sc.ContextKeyAPIClient, apiClient)
	}

	c := cmd
	for {
		if c.Parent() == nil || c.Parent().Name() == "ssoossh" {
			break
		}
		c = c.Parent()
	}
	if slices.Contains([]string{"inspect", "ssh"}, c.Name()) {
		var ag agent.Agent

		// add ssh-agent to context if it doesn't exist already
		if ctx.Value(sc.ContextKeyAgent) == nil {
			if cfg.UseAgent {
				ag, _ = agent.NewSSHAgent()
				if ag == nil && cfg.FallbackFileAgent {
					ag, _ = agent.NewFileAgent(cfg.SshKeyIdentityFile)
				}
				if ag == nil {
					return fmt.Errorf("unable to create ssh-agent, please ensure you have an ssh-agent running or set the fallback file agent to true")
				}
				ctx = context.WithValue(ctx, sc.ContextKeyAgent, ag)
				cmd.SetContext(ctx)
				ag.SetCA(cfg.CA)
			}
		}
	}
	var ca string = cfg.CA

	if ca == "" && cfg.SkipCa {
		return fmt.Errorf("CA is not set and skip-ca is enabled, please provide a CA or disable skip-ca")
	}

	if ca == "" {
		var err error
		ca, err = apiClient.GetCA()
		if err != nil {
			return err
		}
		cfg.CA = ca
	}

	ctx = context.WithValue(ctx, sc.ContextKeyConfig, *cfg)
	cmd.SetContext(ctx)

	return nil
}
