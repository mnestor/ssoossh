// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

type Config struct {
	Server     string `mapstructure:"server"`
	KeyTypeRSA bool   `mapstructure:"type-rsa"`
	KeyTypeEC  bool   `mapstructure:"type-ec"`
	KeySize    int    `mapstructure:"key-size"`
}
