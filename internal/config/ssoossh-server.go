// Created by Mike Nestor <me@mikenestor.org>
package config

import (
	"fmt"
	"os"
	"strings"
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
	Hsts         string        `mapstructure:"hsts"`
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
	ClientID     string      `mapstructure:"clientid"`
	ClientSecret string      `mapstructure:"clientsecret"`
	ProviderUrl  string      `mapstructure:"providerurl"`
	Scopes       string      `mapstructure:"scopes"`
	Fields       OAuthFields `mapstructure:"fields"`
}

type OAuthFields struct {
	Username string `mapstructure:"username"`
	Groups   string `mapstructure:"groups"`
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

func Init() {
	viper.SetConfigName("ssoossh-server") // config file name without extension
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("/etc/")

	viper.SetEnvPrefix("SSOOSSH")
	viper.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	viper.AutomaticEnv()

	setDefaults()

	_ = LoadConfig(true)
}

func GetConfig() *Config {
	configLock.RLock()
	defer configLock.RUnlock()
	return config
}

func LoadConfig(fail bool) error {
	configLock.Lock()

	err := viper.MergeInConfig()
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

func setDefaults() {
	viper.SetDefault("logging.level", 4)
	viper.SetDefault("logging.color", false)
	viper.SetDefault("logging.json", true)

	viper.SetDefault("sshkey", "")

	viper.SetDefault("certoptions.host.require_group", "")
	viper.SetDefault("certoptions.host.valid_duration", "")

	viper.SetDefault("certoptions.service.require_group", "")
	viper.SetDefault("certoptions.service.valid_duration", "")

	viper.SetDefault("certoptions.user.valid_duration", "")
	viper.SetDefault("certoptions.user.extensions", []string{
		"permit-X11-forwarding",
		"permit-agent-forwarding",
		"permit-port-forwarding",
		"permit-pty",
		"permit-user-rc",
	})

	viper.SetDefault("server.accesslog", true)
	viper.SetDefault("server.domain", "localhost")
	viper.SetDefault("server.ratelimit", 10)
	viper.SetDefault("server.rate_duration", "1m")
	viper.SetDefault("server.address", "0.0.0.0")
	viper.SetDefault("server.port", "443")

	viper.SetDefault("server.oauth.clientid", "CHANGEME")
	viper.SetDefault("server.oauth.clientsecret", "CHANGEME")
	viper.SetDefault("server.oauth.providerurl", "CHANGEME")
	viper.SetDefault("server.oauthscopes", "profile email")
	viper.SetDefault("server.fields.username", "preferred_username")

	viper.SetDefault("server.tls.cert_file", "")
	viper.SetDefault("server.tls.key_file", "")
}
