// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type Config struct {
	ConfigFile string `mapstructure:"config"`
	Server     string `mapstructure:"server"`
	KeyTypeRSA bool   `mapstructure:"type-rsa"`
	KeyTypeEC  bool   `mapstructure:"type-ec"`
	KeySize    int    `mapstructure:"key-size"`
}

func loadConfig(cmd *cobra.Command, args []string) error {
	viper.BindPFlags(cmd.Flags())
	_ = viper.Unmarshal(&config)

	viper.SetDefault("key-size", 4096)
	viper.SetDefault("type-rsa", true)
	viper.SetDefault("write-only", true)
	viper.SetDefault("host-pubkey", "/etc/ssh/ssh_host_rsa_key.pub")

	if config.ConfigFile != "" {
		viper.SetConfigFile(config.ConfigFile)
	} else {
		viper.SetTypeByDefaultValue(true)
		viper.SetConfigName("ssoossh")
		viper.SetConfigType("yaml")
		viper.AddConfigPath("/etc")
		viper.AddConfigPath(".")
		viper.AddConfigPath("~/.config")
	}

	viper.SetEnvPrefix("SSOOSSH")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	viper.BindPFlags(cmd.PersistentFlags())
	viper.BindPFlags(cmd.Flags())

	// we do not require a configfile as we can be fully configured
	// from env or arguments passed in
	_ = viper.MergeInConfig()

	_ = viper.Unmarshal(&config)

	if config.Server == "" {
		return errors.New("server is required")
	}

	return nil
}
