// Created by Mike Nestor <me@mikenestor.org>

package ssoossh

import (
	"github.com/mnestor/ssoossh/pkg/crypto/sshutil"
)

func init() {
	rootCmd.PersistentFlags().BoolVar(&sshutil.NoPageant,
		"no-pageant",
		false,
		"Use openssh agent from cigwyn or linux subsystem (WSL) instead of Pageant")
}
