//go:build linux

package server

import (
	"errors"
	"fmt"
	"net"
	"syscall"

	"golang.org/x/sys/unix"
)

// errNotUnix is a connection that is not an AF_UNIX socket.
var errNotUnix = errors.New("peer credentials need an AF_UNIX connection")

// Credentials implements PeerInspector with SO_PEERCRED.
func (ProcInspector) Credentials(conn net.Conn) (Credentials, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return Credentials{}, errNotUnix
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		// not covered: SyscallConn fails only on a connection already
		// closed, and the server asks before it closes anything.
		return Credentials{}, fmt.Errorf("peer credentials: %w", err)
	}
	var cred *unix.Ucred
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		// not covered: as above, Control fails only on a closed socket.
		return Credentials{}, fmt.Errorf("peer credentials: %w", err)
	}
	if sockErr != nil {
		return Credentials{}, fmt.Errorf("peer credentials: %w", sockErr)
	}
	return Credentials{UID: cred.Uid, GID: cred.Gid, PID: cred.Pid}, nil
}
