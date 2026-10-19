package server

import (
	"bufio"
	"bytes"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path"
	"strconv"
	"strings"
)

// Credentials are what the kernel says about a connected peer.
type Credentials struct {
	// UID and GID are the peer's ids at connect time.
	UID, GID uint32
	// PID is the peer's process id at connect time.
	PID int32
}

// PeerInspector reads a peer's credentials and its process's cgroup. The
// real one is ProcInspector; tests fake it to play sshd.
type PeerInspector interface {
	// Credentials returns the SO_PEERCRED of a connected AF_UNIX socket.
	Credentials(conn net.Conn) (Credentials, error)
	// Cgroup returns the contents of /proc/<pid>/cgroup.
	Cgroup(pid int32) ([]byte, error)
}

// ProcInspector is the production PeerInspector: SO_PEERCRED and /proc.
type ProcInspector struct {
	// Proc is the proc filesystem's mount point, "/proc" when empty.
	Proc string
}

var _ PeerInspector = ProcInspector{}

// Cgroup implements PeerInspector.
func (p ProcInspector) Cgroup(pid int32) ([]byte, error) {
	proc := p.Proc
	if proc == "" {
		proc = "/proc"
	}
	data, err := os.ReadFile(path.Join(proc, strconv.FormatInt(int64(pid), 10), "cgroup"))
	if err != nil {
		return nil, fmt.Errorf("read the cgroup of pid %d: %w", pid, err)
	}
	return data, nil
}

// sshdCheck decides whether a peer is sshd (README R5): uid 0 and a
// systemd cgroup whose last path component is one of units. A process's
// comm is settable by anyone and is never trusted.
//
// The executable path is not used. readlink(/proc/<pid>/exe) of another
// root process needs every capability that process holds, or
// CAP_SYS_PTRACE, and the daemon's unit runs with an empty bounding set:
// on AlmaLinux 9 and 10 it failed with EACCES on every call, the
// pre-authentication lookup included. /proc/<pid>/cgroup needs no
// capability and names sshd.service before authentication.
type sshdCheck struct {
	units []string
	peers PeerInspector
	log   *slog.Logger
}

// isSSHD applies the check. Every failure (an exited peer, a /proc the
// daemon may not read, a cgroup file it cannot parse) is quietly "not
// sshd": the cost is a login refresh skipped, never an answer.
func (c sshdCheck) isSSHD(cred Credentials) bool {
	if cred.UID != 0 {
		return false
	}
	data, err := c.peers.Cgroup(cred.PID)
	if err != nil {
		c.log.Debug("cannot read a root peer's cgroup; not treating it as sshd", "pid", cred.PID, "error", err)
		return false
	}
	for _, p := range systemdCgroupPaths(data) {
		if unitMatches(c.units, path.Base(p)) {
			return true
		}
	}
	return false
}

// systemdCgroupPaths returns the cgroup paths systemd manages from a
// /proc/<pid>/cgroup file: the cgroup v2 line ("0::/path") and, on a
// hybrid or legacy host, the name=systemd hierarchy's line.
func systemdCgroupPaths(data []byte) []string {
	var paths []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		id, rest, ok := strings.Cut(scanner.Text(), ":")
		if !ok {
			continue
		}
		controllers, p, ok := strings.Cut(rest, ":")
		if !ok || !strings.HasPrefix(p, "/") {
			continue
		}
		if (id == "0" && controllers == "") || controllers == "name=systemd" {
			paths = append(paths, p)
		}
	}
	return paths
}

// unitMatches reports whether unit is one of units. An entry ending in
// "@.service" is a template and matches any instance of it:
// "sshd@.service" matches "sshd@0-10.0.0.1:22-10.0.0.2:5555.service".
func unitMatches(units []string, unit string) bool {
	for _, u := range units {
		if prefix, ok := strings.CutSuffix(u, "@.service"); ok {
			instance, isService := strings.CutSuffix(unit, ".service")
			if isService && strings.HasPrefix(instance, prefix+"@") && len(instance) > len(prefix)+1 {
				return true
			}
			continue
		}
		if unit == u {
			return true
		}
	}
	return false
}
