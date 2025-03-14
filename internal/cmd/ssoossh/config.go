// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"errors"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type contextKey string

const CONFIG_CTX string = "config"

type Config struct {
	ConfigFile string `mapstructure:"config"`
	Server     string `mapstructure:"server"`
	KeyTypeRSA bool   `mapstructure:"type-rsa"`
	KeyTypeEC  bool   `mapstructure:"type-ec"`
	KeySize    int    `mapstructure:"key-size"`
	HostPubkey string `mapstructure:"host-pubkey"`
	WriteCert  bool   `mapstructure:"write-cert"`
	WriteFile  string `mapstructure:"write-file"`
	Username   string `mapstructure:"username"`
}

func loadConfig(cmd *cobra.Command, args []string) (*Config, error) {
	v := viper.New()
	v.BindPFlags(cmd.Flags())
	config := &Config{}
	_ = v.Unmarshal(&config)

	v.SetDefault("key-size", 4096)
	v.SetDefault("type-rsa", true)
	v.SetDefault("type-ec", false)
	v.SetDefault("write-only", true)
	v.SetDefault("host-pubkey", "/etc/ssh/ssh_host_rsa_key.pub")

	if config.ConfigFile != "" {
		v.SetConfigFile(config.ConfigFile)
	} else {
		v.SetTypeByDefaultValue(true)
		v.SetConfigName("ssoossh")
		v.SetConfigType("yaml")
		v.AddConfigPath("/etc")
		v.AddConfigPath(".")
		v.AddConfigPath("~/.config")
	}

	v.SetEnvPrefix("SSOOSSH")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	v.BindPFlags(cmd.PersistentFlags())
	v.BindPFlags(cmd.Flags())

	// we do not require a configfile as we can be fully configured
	// from env or arguments passed in
	_ = v.MergeInConfig()

	_ = v.Unmarshal(&config)

	config.KeyTypeRSA = !config.KeyTypeEC

	if config.Server == "" {
		return nil, errors.New("server is required")
	}

	return config, nil
}
