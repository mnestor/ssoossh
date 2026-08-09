package config

type Config struct {
	Server        string `mapstructure:"server"`
	CAPubkey      string `mapstructure:"capubkey"`
	SkipVerifySSL bool   `mapstructure:"insecure_skip_verify"`

	SSHKey SSHKeyOptions `mapstructure:"sshkey"`

	// this is the default and recommended way to use ssoossh
	UseAgent bool `mapstructure:"use_agent"`
	// use files if agent is not available
	FallbackFileAgent bool   `mapstructure:"fallback_file_agent"`
	Filename          string `mapstructure:"key_filename"`

	// default is user.Current().Username
	Username string `mapstructure:"username"`

	TryOpenBrowser bool `mapstructure:"try_open_browser"`
}

type SSHKeyOptions struct {
	KeySize int        `mapstructure:"size"`
	Type    SSHKeyType `mapstructure:"type"`
}

type SSHKeyType int

const (
	SSHKeyTypeRSA SSHKeyType = iota + 1
	SSHKeyTypeEC25519
	SSHKeyTypeECDSA
)
