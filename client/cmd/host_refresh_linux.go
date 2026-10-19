//go:build linux

package cmd

import (
	"context"

	"github.com/mnestor/ssoossh/internal/varlink"
)

// send sends RefreshUser for user, one-way, and returns once it is written
// or has failed. Every error is dropped on purpose; see nssRefresh.
func (r nssRefresh) send(ctx context.Context, user string) {
	if user == "" {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	c, err := varlink.Dial(ctx, r.socket)
	if err != nil {
		// The usual case on a host without ssoossh-nss: no socket.
		return
	}
	defer func() { _ = c.Close() }()
	if err := c.CallOneway(ctx, nssRefreshMethod, nssRefreshParams{UserName: user}); err != nil {
		// Dropped like the dial error: a refresh that did not go out
		// costs one stale login, never the answer sshd is waiting for.
		return
	}
}

// The ssoossh-nss method `host principals` calls, and its parameters.
// The socket is in host_refresh.go; test/packaging checks both against
// nssd.
const (
	// nssRefreshMethod asks the daemon to refresh one user in the
	// background.
	nssRefreshMethod = "com.github.mnestor.ssoossh.NSS.RefreshUser"
)

// nssRefreshParams are RefreshUser's parameters.
type nssRefreshParams struct {
	// UserName is the account sshd is about to resolve.
	UserName string `json:"userName"`
}
