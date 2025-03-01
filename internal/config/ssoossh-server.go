// Created by Mike Nestor <me@mikenestor.org>
package config

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mnestor/ssoossh/internal/log"
	"github.com/spf13/viper"
)

type Config struct {
	Logging     log.LogSettings    `mapstructure:"logging"`
	Server      ServerSettings     `mapstructure:"server"`
	CertOptions CertificateOptions `mapstructure:"certoptions"`
	SshKey      string             `mapstructure:"sshkey"`
}

type ServerSettings struct {
	AccessLog    bool          `mapstructure:"accesslog"`
	Address      string        `mapstructure:"address"`
	Port         int           `mapstructure:"port"`
	Domain       string        `mapstructure:"domain"`
	CookieKey    string        `mapstructure:"cookiekey"`
	RateLimit    int           `mapstructure:"ratelimit"`
	RateDuration time.Duration `mapstructure:"rate_duration"`
	Tls          TlsConfig     `mapstructure:"tls"`
	RBAC         RBACConfig    `mapstructure:"rbac"`
	AuthConfig   OAuthConfig   `mapstructure:"oauth"`
}

type TlsConfig struct {
	Cert     string `mapstructure:"cert"`
	CertFile string `mapstructure:"cert_file"`
	Key      string `mapstructure:"key"`
	KeyFile  string `mapstructure:"key_file"`
}

type OAuthConfig struct {
	ClientID     string `mapstructure:"clientid"`
	ClientSecret string `mapstructure:"clientsecret"`
	ProviderUrl  string `mapstructure:"providerurl"`
	Scopes       string `mapstructure:"scopes"`
}

type RBACConfig struct {
	Policy string   `mapstructure:"policy"`
	Model  string   `mapstructure:"model"`
	Roles  RoleInfo `mapstructure:"roles"`
}

type RoleInfo map[string][]string

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
	RequireGroup  string        `mapstructure:"require_group"`
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
	Extensions    []string      `mapstructure:"extensions"`
}

type CertOptionsService struct {
	RequireGroup  string        `mapstructure:"require_group"`
	ValidDuration time.Duration `mapstructure:"valid_duration,string"`
	Extensions    []string      `mapstructure:"extensions"`
}

var (
	config     *Config
	configLock = new(sync.RWMutex)
)

func init() {
	viper.SetConfigName("ssoossh-server") // config file name without extension
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("/etc/")
	viper.AddConfigPath("./config/") // config file path
	viper.SetEnvPrefix("SSOOSSH")
	viper.AutomaticEnv()

	_ = LoadConfig(true)
}

func GetConfig() *Config {
	configLock.RLock()
	defer configLock.RUnlock()
	return config
}

func LoadConfig(fail bool) error {
	configLock.Lock()

	err := viper.ReadInConfig()
	if err != nil {
		if !fail {
			return err
		}
		fmt.Println("fatal error config file: default \n", err)
		os.Exit(1)
	}

	err = viper.Unmarshal(&config)
	if err != nil {
		if !fail {
			return err
		}
		fmt.Println("fatal error config file: default \n", err)
		os.Exit(1)
	}

	// config = temp
	configLock.Unlock()

	log.SetupLogger(config.Logging)

	return nil
}
