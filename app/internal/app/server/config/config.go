package config

import (
	"bytes"
	_ "embed"
	"os"
	"strings"
	"time"

	"github.com/mnestor/ssoossh/internal/common/db"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

//go:embed defaults.yml
var defaultConfig string

type Config struct {
	Verbose    bool   `mapstructure:"verbose"`
	Traces     bool   `mapstructure:"traces"`
	Metrics    bool   `mapstructure:"metrics"`
	LogJson    bool   `mapstructure:"log_json"`
	LogLevel   string `mapstructure:"log_level"`
	ConfigFile string `mapstructure:"config"`
	AppUrl     string `mapstructure:"app_url"`

	Db db.DbConfig `mapstructure:"db"`

	Server     ServerSettings `mapstructure:"server"`
	AuthConfig OAuthConfig    `mapstructure:"oauth"`

	SshCaKey     string `mapstructure:"sshcakey"`
	SshCaKeyFile string `mapstructure:"sshcakey_file"`
}

type ServerSettings struct {
	UnixSocket     string        `mapstructure:"unix_socket"`
	UnixSocketMode string        `mapstructure:"unix_socket_mode"`
	Address        string        `mapstructure:"address"`
	Port           int           `mapstructure:"port"`
	Domain         string        `mapstructure:"domain"`
	CookieKey      string        `mapstructure:"cookiekey"`
	RateLimit      int           `mapstructure:"ratelimit"`
	RateDuration   time.Duration `mapstructure:"rate_duration"`
	Hsts           string        `mapstructure:"hsts"`
	TrustedProxies []string      `mapstructure:"trusted_proxies"`
}

type OAuthConfig struct {
	ClientID     string   `mapstructure:"clientid"`
	ClientSecret string   `mapstructure:"clientsecret"`
	ProviderUrl  string   `mapstructure:"providerurl"`
	Scopes       []string `mapstructure:"scopes"`
	// Fields       OAuthFields `mapstructure:"fields"`

	AdminGroup string `mapstructure:"admin_group"`
}

type CertificateOptions struct {
	User    CertOptionsUser    `mapstructure:"user"`
	Service CertOptionsService `mapstructure:"service"`
	Host    CertOptions        `mapstructure:"host"`
}

type CertOptions struct {
	RequireGroup  string        `mapstructure:"require_group"`
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
}

type CertOptionsUser struct {
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
	Extensions    []string      `mapstructure:"extensions"`
}

type CertOptionsService struct {
	RequireGroup  string        `mapstructure:"require_group"`
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
	Extensions    []string      `mapstructure:"extensions"`
}

func InitConfig(cmd *cobra.Command) (cfg *Config) {
	v := viper.New()

	// set defaults
	v.SetConfigType("yml")
	v.MergeConfig(bytes.NewBufferString(defaultConfig))

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
	}

	// set up viper to read from environment variables
	// v.SetEnvPrefix("SSOOSSH")
	prefix := os.Getenv("SSOOSSH_PREFIX")
	if prefix != "" {
		v.SetEnvPrefix(prefix)
	}
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_", ".", "_"))
	v.AutomaticEnv()

	v.BindPFlags(cmd.PersistentFlags())
	v.BindPFlags(cmd.Flags())

	// we do not require a configfile as we can be fully configured
	// from env or arguments passed in
	_ = v.MergeInConfig()
	_ = v.Unmarshal(&config)

	if config.Db.LogLevel == "" {
		config.Db.LogLevel = config.LogLevel
	}

	return config
}
