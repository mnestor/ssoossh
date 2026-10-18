//go:build linux

// Command ssoosshd-nss is the ssoossh NSS daemon, a systemd userdb service
// that serves directory users and groups to nss-systemd. Linux only: it
// needs systemd and SO_PEERCRED. All the code is in nssd/.
package main

import (
	"os"

	"github.com/mnestor/ssoossh/nssd/cmd"
)

// main runs the command tree and exits with its status.
func main() {
	os.Exit(cmd.Main())
}
