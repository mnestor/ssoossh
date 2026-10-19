//go:build !linux

package cmd

import (
	"context"
)

// send does nothing: ssoossh-nss is a systemd userdb service, so there is
// no daemon to refresh off Linux.
func (r nssRefresh) send(context.Context, string) {}
