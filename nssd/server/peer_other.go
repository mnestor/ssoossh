//go:build !linux

package server

import (
	"errors"
	"net"
)

// Credentials implements PeerInspector. SO_PEERCRED is Linux's; the
// daemon is Linux-only and this exists so the package still compiles
// elsewhere.
func (ProcInspector) Credentials(net.Conn) (Credentials, error) {
	return Credentials{}, errors.New("peer credentials are only implemented on Linux")
}
