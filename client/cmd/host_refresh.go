package cmd

import (
	"time"
)

// The ssoosshd-nss contract `host principals` uses: the daemon's userdb
// socket here, and the one-way method that asks it to refresh a user in
// host_refresh_linux.go. Spelled out rather than imported, because the
// client never imports the daemon's packages; test/packaging checks both
// against nssd.
const (
	// nssUserdbSocket is where ssoosshd-nss.socket listens.
	nssUserdbSocket = "/run/systemd/userdb/com.github.mnestor.ssoossh"
	// nssRefreshTimeout bounds the whole call, connect and write. sshd
	// waits on this command before every certificate login, so the call
	// may cost a login this much at most and never more.
	nssRefreshTimeout = 50 * time.Millisecond
)

// nssRefresh asks a host's ssoosshd-nss to refresh a user just before
// sshd resolves them, so a group the directory removed is gone at this
// login rather than at cache expiry (docs/proposals/nss-directory, Part 3,
// "Client change"). It is a hint and nothing more: the call is one-way,
// no answer is read, and every failure (no daemon, no socket, a full
// socket, a non-Linux host) is ignored. It talks to a local socket only;
// the command still never touches the network and never listens.
type nssRefresh struct {
	// socket is the daemon's userdb socket.
	socket string
	// timeout bounds the connect and the write together.
	timeout time.Duration
}

// newNSSRefresh returns the refresh for the installed daemon.
func newNSSRefresh() nssRefresh {
	return nssRefresh{socket: nssUserdbSocket, timeout: nssRefreshTimeout}
}
