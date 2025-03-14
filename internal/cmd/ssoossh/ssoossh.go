// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	verInfo "github.com/mnestor/ssoossh/internal/version"
)

var (
	outWriter io.Writer
	errWriter io.Writer
	config    Config
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
	}
	rootCmd.SetOut(o)
	rootCmd.SetErr(e)
	rootCmd.SetArgs(args)

	// since cobra doesn't expose the Output and Error writers we set above
	outWriter = o
	errWriter = e

	rootCmd.PersistentFlags().StringP("config", "c", "", "configuration file")
	rootCmd.PersistentFlags().StringP("server", "s", "", "server that signs pubkeys")
	rootCmd.SilenceUsage = true

	rootCmd.AddCommand(caCmd)
	rootCmd.AddCommand(inspectCmd)
	rootCmd.AddCommand(logoutCmd)
	rootCmd.AddCommand(proxyCmd)
	rootCmd.AddCommand(loginCmd)
	rootCmd.AddCommand(hostCmd)
	rootCmd.AddCommand(serviceCmd)

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

	cobra.OnInitialize(func() {
		loadConfig(rootCmd, args)
	})

	return rootCmd, nil
}
