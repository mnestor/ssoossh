// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	api "github.com/mnestor/ssoossh/internal/api/client"
	"github.com/mnestor/ssoossh/internal/ssh"
	ssha "github.com/mnestor/ssoossh/internal/ssh"
	verInfo "github.com/mnestor/ssoossh/internal/version"
)

func NewRootCommand(
	ctx context.Context,
	o io.Writer,
	e io.Writer,
	args []string,
) (*cobra.Command, error) {
	rootCmd := &cobra.Command{
		Use:               "ssoossh",
		Short:             "client for managing ssh certificate retrieval",
		Version:           verInfo.Version,
		PersistentPreRunE: persistentPreRunE,
	}

	rootCmd.SetContext(ctx)
	rootCmd.SetOut(o)
	rootCmd.SetErr(e)
	rootCmd.SetArgs(args)

	rootCmd.PersistentFlags().StringP("config", "c", "", "configuration file")
	rootCmd.PersistentFlags().StringP("server", "s", "", "server that signs pubkeys (eg: https://ssoossh.example.com)")
	rootCmd.MarkFlagRequired("server")
	rootCmd.SilenceUsage = true

	rootCmd.AddCommand(newCaCmd())
	rootCmd.AddCommand(newInspectCmd())
	rootCmd.AddCommand(newLogoutCmd())
	rootCmd.AddCommand(newProxyCmd())
	rootCmd.AddCommand(newLoginCmd())
	rootCmd.AddCommand(newHostCmd())
	rootCmd.AddCommand(newServiceCmd())

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

	return rootCmd, nil
}

func persistentPreRunE(cmd *cobra.Command, args []string) error {
	config, err := loadConfig(cmd)
	if err != nil {
		return err
	}

	// add config to context
	ctx := context.WithValue(cmd.Context(), CONFIG_CTX, config)

	// add apiClient to context if it doesn't exist already
	apiClient := api.NewClient(cmd.Context(), config.Server)
	if ctx.Value(APICLIENT_CTX) == nil {
		ctx = context.WithValue(ctx, APICLIENT_CTX, apiClient)
	}

	// add ssh-agent to context if it doesn't exist already
	if ctx.Value(AGENT_CTX) == nil {
		agent, _ := ssh.NewAgent()
		ctx = context.WithValue(ctx, AGENT_CTX, agent)
	}

	cmd.SetContext(ctx)
	return nil
}

func getApiClient(ctx context.Context) api.ClientI {
	return ctx.Value(APICLIENT_CTX).(api.ClientI)
}

func getAgent(ctx context.Context) *ssha.Agent {
	return ctx.Value(AGENT_CTX).(*ssha.Agent)
}

func getConfig(ctx context.Context) *Config {
	return ctx.Value(CONFIG_CTX).(*Config)
}

func preRun(cmd *cobra.Command, args []string) error {
	config := getConfig(cmd.Context())
	agent := getAgent(cmd.Context())
	apiClient := getApiClient(cmd.Context())

	var ca string = config.CA
	if ca == "" {
		var err error
		ca, err = apiClient.GetCA()
		if err != nil {
			return err
		}
		config.CA = ca
	}

	agent.LoadCA(ca)

	return nil
}
