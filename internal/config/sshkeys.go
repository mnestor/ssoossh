// Created By Mike Nestor <me@mikenestor.org>
package config

import (
	"log/slog"

	"golang.org/x/crypto/ssh"
)

func (k *Config) SshPubKey() string {
	pkey, err := ssh.ParsePrivateKey([]byte(k.SshKey))
	if err != nil {
		slog.Error("unable to parse private key", slog.Any("error", err))
		return ""
	}
	pubKey := ssh.MarshalAuthorizedKey(pkey.PublicKey())
	return string(pubKey)
}
