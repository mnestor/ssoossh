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
)

func GetCommand(
	ctx context.Context,
	o io.Writer,
	e io.Writer,
	args []string,
) *cobra.Command {
	rootCmd.SetOut(o)
	// rootCmd.SetErr(e)

	// since cobra doesn't expose the Output and Error writers we set above
	outWriter = o
	errWriter = e

	rootCmd.PersistentFlags().String("file", "", "configuration file")
	rootCmd.PersistentFlags().StringP("server", "s", "", "server that signs pubkeys")

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

	loadConfig(args)

	return rootCmd
}

func loadConfig(args []string) {
	// I really wish I could figure out a better way of doing this!
	// let the user specify the file as a flag to force a specific config
	var f string
	if len(args) > 1 {
		p := "--file"
		for i, v := range args {
			if v == p {
				f = args[i+1]
				break
			} else if strings.HasPrefix(v, p) {
				f = strings.Split(v, "=")[1]
				break
			}
		}
	}

	var v *viper.Viper = getViper()

	v.SetDefault("key-size", 4096)
	v.SetDefault("type-rsa", true)
	v.SetDefault("write-only", true)
	v.SetDefault("host-pubkey", "/etc/ssh/ssh_host_rsa_key.pub")

	v.SetTypeByDefaultValue(true)
	if f != "" {
		v.SetConfigFile(f)
	} else {
		v.SetConfigName("ssoossh")
		v.SetConfigType("yaml")
		v.AddConfigPath("/etc")
		v.AddConfigPath(".")
		v.AddConfigPath("~/.config")
	}

	v.SetEnvPrefix("SSOOSSH")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	if err := v.BindPFlags(rootCmd.PersistentFlags()); err != nil {
		fmt.Fprint(errWriter, "unable to load config")
		os.Exit(1)
	}

	if err := v.ReadInConfig(); err != nil {
		fmt.Fprint(errWriter, "unable to load config")
		os.Exit(1)
	}

	_ = v.Unmarshal(&config)
}
