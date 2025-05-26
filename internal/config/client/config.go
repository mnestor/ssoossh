// Created By Mike Nestor <me@mikenestor.org>
package config

import (
	"bytes"
	_ "embed"
	"os/user"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/thediveo/enumflag"
)

//go:embed defaults.yaml
var defaultconfig string

type Config struct {
	ConfigFile string `mapstructure:"config"`

	SshKeyIdentityFile string `mapstructure:"ssh-key-identity-file"`

	// certificate authority that is signing ssh keys
	CA string `mapstructure:"ca"`
	// skip asking the server for the CA
	SkipCa bool `mapstructure:"skip-ca"`

	// Disble check ssl trust chain
	VerifySsl bool `mapstructure:"verify-ssl"`

	// verify server name in the ssl certificate
	VerifyServerName bool `mapstructure:"verify-server-name"`

	// Verify the server we are talking to is the one we expect
	SslFingerprint string `mapstructure:"ssl-fingerprint"`
	Server         string `mapstructure:"server"`

	// -----
	// ssh command specific configuration
	// use the ssh-agent to manage keys
	// this is the default and recommended way to use ssoossh
	UseAgent bool `mapstructure:"use-agent"`
	// use files if agent is not available
	FallbackFileAgent bool `mapstructure:"fallback-file-agent"`

	KeyType string `mapstructure:"key-type"`
	KeySize int    `mapstructure:"key-size"`

	// HostPubkey string `mapstructure:"host-pubkey"`
	// WriteCert  bool   `mapstructure:"write-cert"`
	// WriteFile  string `mapstructure:"write-file"`

	// -----
	// service command specific configuration
	// default is user.Current().Username
	Username string `mapstructure:"username"`
}

type KeyType enumflag.Flag

const (
	KeyTypeEC KeyType = iota
	KeyTypeRSA
)

var KeyTypeNames = map[KeyType][]string{
	KeyTypeEC:  {"ed25519"},
	KeyTypeRSA: {"rsa"},
}

func NewConfig(cmd *cobra.Command) (*Config, error) {
	v := viper.New()

	// set defaults
	v.SetConfigType("yaml")
	v.MergeConfig(bytes.NewBufferString(defaultconfig))

	u, _ := user.Current()
	v.SetDefault("username", u.Username)

	// bind flags to viper
	v.BindPFlags(cmd.Flags())
	config := &Config{}
	_ = v.Unmarshal(&config)

	// set up viper to read from config file if provided
	if config.ConfigFile != "" {
		v.SetConfigFile(config.ConfigFile)
	} else {
		v.SetTypeByDefaultValue(true)
		v.SetConfigName("ssoossh")
		v.AddConfigPath("/etc")
		v.AddConfigPath(".")
		v.AddConfigPath("~/.config")
	}

	// set up viper to read from environment variables
	v.SetEnvPrefix("SSOOSSH")
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()

	v.BindPFlags(cmd.PersistentFlags())
	v.BindPFlags(cmd.Flags())

	// we do not require a configfile as we can be fully configured
	// from env or arguments passed in
	_ = v.MergeInConfig()
	_ = v.Unmarshal(&config)

	// config.KeyTypeRSA = !config.KeyTypeEC

	// if config.Server == "" {
	// 	return nil, errors.New("server is required")
	// }

	return config, nil
}
