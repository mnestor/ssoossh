package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

// listenFDsStart is the first descriptor systemd passes, SD_LISTEN_FDS_START.
const listenFDsStart = 3

// Activation is the socket-activation environment: what LISTEN_PID and
// LISTEN_FDS say, and how a descriptor becomes a file. The zero value
// reads the real environment.
type Activation struct {
	// Getenv reads the environment; nil means os.Getenv.
	Getenv func(string) string
	// PID is this process's id; 0 means os.Getpid().
	PID int
	// File wraps a descriptor; nil means os.NewFile.
	File func(fd uintptr, name string) *os.File
}

// ErrNotActivated is a process systemd did not start with sockets.
var ErrNotActivated = errors.New("not socket-activated: LISTEN_FDS is unset or names another process")

// Listener returns the userdb socket systemd passed, the one socket the
// unit's ListenStream= names. Socket activation lets lookups queue while
// the daemon restarts instead of failing (README check 2), so this is the
// production path; --listen's self-bind is for development and tests.
//
// The variables are left in the environment: the daemon starts no child
// that could mistake them for its own.
func (a Activation) Listener() (net.Listener, error) {
	getenv, pid, file := a.Getenv, a.PID, a.File
	if getenv == nil {
		getenv = os.Getenv
	}
	if pid == 0 {
		pid = os.Getpid()
	}
	if file == nil {
		file = os.NewFile
	}

	if getenv("LISTEN_PID") != strconv.Itoa(pid) || getenv("LISTEN_FDS") == "" {
		return nil, ErrNotActivated
	}
	n, err := strconv.Atoi(getenv("LISTEN_FDS"))
	if err != nil || n != 1 {
		return nil, fmt.Errorf("socket activation passed LISTEN_FDS=%q; the socket unit must listen on exactly one socket", getenv("LISTEN_FDS"))
	}
	f := file(listenFDsStart, "userdb")
	if f == nil {
		return nil, fmt.Errorf("socket activation: descriptor %d is not open", listenFDsStart)
	}
	defer func() { _ = f.Close() }() // FileListener dups it
	l, err := net.FileListener(f)
	if err != nil {
		return nil, fmt.Errorf("socket activation: descriptor %d is not a listening socket: %w", listenFDsStart, err)
	}
	return l, nil
}

// Listen binds an AF_UNIX socket at path with mode, replacing a stale
// socket left by an earlier run. Anything else at path is left alone and
// refused, so a typo cannot delete a file.
func Listen(path string, mode os.FileMode) (net.Listener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode().Type() != fs.ModeSocket {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			// not covered: removing a socket in a directory this process
			// may write fails only on an I/O error.
			return nil, fmt.Errorf("remove the stale socket %s: %w", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create the socket directory: %w", err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		// not covered: chmod of a socket this process just bound cannot
		// be refused.
		_ = l.Close()
		return nil, fmt.Errorf("set the mode of %s: %w", path, err)
	}
	return l, nil
}
