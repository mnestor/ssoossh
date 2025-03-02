//go:build !DEV
// +build !DEV

// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import "github.com/spf13/viper"

const DEBUG = false

func getViper() *viper.Viper {
	return viper.New()
}
