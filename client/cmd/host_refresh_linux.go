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
		// The usual case on a host without ssoosshd-nss: no socket.
		return
	}
	defer func() { _ = c.Close() }()
	_ = c.CallOneway(ctx, nssRefreshMethod, nssRefreshParams{UserName: user})
}
