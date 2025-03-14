// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	api "github.com/mnestor/ssoossh/internal/api/client"
	"github.com/mnestor/ssoossh/internal/ssh"
	verInfo "github.com/mnestor/ssoossh/internal/version"
)

var (
	outWriter io.Writer
	errWriter io.Writer
	// config    Config
)

func NewRootCommand(
	ctx context.Context,
	o io.Writer,
	e io.Writer,
	args []string,
) (*cobra.Command, error) {
	rootCmd := &cobra.Command{
		Use:     "ssoossh",
		Short:   "client for managing ssh certificate retrieval",
		Version: verInfo.Version,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			config, err := loadConfig(cmd, args)
			if err != nil {
				return err
			}
			ctx := context.WithValue(ctx, CONFIG_CTX, config)
			apiClient := api.GetClient(config.Server)
			if ctx.Value(APICLIENT_CTX) == nil {
				ctx = context.WithValue(ctx, APICLIENT_CTX, apiClient)
			}
			if ctx.Value(AGENT_CTX) == nil {
				agent, _ := ssh.GetAgent()
				ctx = context.WithValue(ctx, AGENT_CTX, agent)
			}
			cmd.SetContext(ctx)
			return nil
		},
	}
	rootCmd.SetOut(o)
	rootCmd.SetErr(e)
	rootCmd.SetArgs(args)

	// since cobra doesn't expose the Output and Error writers we set above
	outWriter = o
	errWriter = e

	rootCmd.PersistentFlags().StringP("config", "c", "", "configuration file")
	rootCmd.PersistentFlags().StringP("server", "s", "", "server that signs pubkeys")
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
