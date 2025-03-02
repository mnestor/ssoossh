// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	verInfo "github.com/mnestor/ssoossh/internal/version"
)

var rootCmd = &cobra.Command{
	Use:     "ssoossh",
	Short:   "client for managing ssh certificate retrieval",
	Version: verInfo.Version,
}

var (
	outWriter io.Writer
	errWriter io.Writer
	config    Config
	debug     bool
)

func GetCommand(
	ctx context.Context,
	o io.Writer,
	e io.Writer,
	args []string,
) *cobra.Command {
	rootCmd.SetOut(o)
	rootCmd.SetErr(e)

	// since cobra doesn't expose the Output and Error writers we set above
	outWriter = o
	errWriter = e

	rootCmd.PersistentFlags().StringP("server", "s", "", "server that signs pubkeys")
	_ = rootCmd.MarkFlagRequired("server")

	rootCmd.AddCommand(caCmd)
	rootCmd.AddCommand(inspectCmd)
	rootCmd.AddCommand(logoutCmd)

	// these 2 need to add some extra parameters
	rootCmd.AddCommand(proxyCmd)
	proxyCmd.Flags().Int("key-size", 4096, "Key Size to generate (2048, 4096)")
	proxyCmd.Flags().Bool("type-rsa", false, "Generate RSA SSH keypair (default)")
	proxyCmd.Flags().Bool("type-ec", false, "Generate EC SSH keypair")
	proxyCmd.MarkFlagsMutuallyExclusive("type-rsa", "type-ec")

	rootCmd.AddCommand(loginCmd)
	loginCmd.Flags().Int("key-size", 4096, "Key Size to generate (2048, 4096)")
	loginCmd.Flags().Bool("type-rsa", false, "Generate RSA SSH keypair (default)")
	loginCmd.Flags().Bool("type-ec", false, "Generate EC SSH keypair")
	loginCmd.MarkFlagsMutuallyExclusive("type-rsa", "type-ec")

	var v *viper.Viper = getViper()
	v.SetTypeByDefaultValue(true)
	v.SetConfigName("ssoossh")
	v.SetConfigType("yaml")
	v.AddConfigPath("/etc/ssoosh")
	v.AddConfigPath(".")
	v.AddConfigPath("~/.config")

	v.SetEnvPrefix("SSOOSSH")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	if err := v.BindPFlags(rootCmd.PersistentFlags()); err != nil {
		fmt.Fprint(errWriter, "unable to load config")
		os.Exit(1)
	}

	if err := v.MergeInConfig(); err != nil {
		fmt.Fprint(errWriter, "unable to load config")
		os.Exit(1)
	}

	v.Unmarshal(&config)

	return rootCmd
}
