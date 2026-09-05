package hostinfo

import (
	"bufio"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// machineID is /etc/machine-id, the same file pam_ssoossh reads on Linux
// (src/hostinfo.c, read_machine_id). systemd writes it once at first boot
// and it survives a rename, which is the whole point: the trail follows a
// machine rather than a name two hosts can both claim.
//
// /var/lib/dbus/machine-id is the older location, and on most systems now a
// symlink to the first; it is read as a fallback for the hosts where it is
// still the only one.
func machineID() string {
	return machineIDFrom([]string{"/etc/machine-id", "/var/lib/dbus/machine-id"})
}

// machineIDFrom is machineID with the paths named, so the "this host has
// neither file" branch is reachable from a test without one.
func machineIDFrom(paths []string) string {
	for _, path := range paths {
		if id := firstLine(path); id != "" {
			return id
		}
	}
	return ""
}

// osName is os-release's PRETTY_NAME followed by the kernel, matching what
// the C module composes: "Debian GNU/Linux 13 (trixie) Linux 6.12.0".
func osName() string {
	parts := make([]string, 0, 2)
	if pretty := prettyName(); pretty != "" {
		parts = append(parts, pretty)
	}
	if kernel := unameString(); kernel != "" {
		parts = append(parts, kernel)
	}
	return strings.Join(parts, " ")
}

// ttyName is the controlling terminal, read through /proc rather than
// guessed: fd 0's link is what ttyname(3) would resolve to.
//
// A client run from cron or a CI job has no terminal and the link points at
// a pipe or /dev/null, which is not a terminal and is reported as none --
// "no tty" is itself worth knowing on an approval page.
func ttyName() string {
	link, err := os.Readlink("/proc/self/fd/0")
	if err != nil {
		// not covered: /proc/self/fd/0 exists on every Linux with /proc
		// mounted, and a test cannot unmount it. A host without /proc
		// falls back to the environment, which is what this does.
		return sshTTY()
	}
	return ttyFromLink(link)
}

// ttyFromLink decides whether fd 0's link names a terminal. Split out
// because the decision is the part worth testing and the readlink is not:
// under `go test` fd 0 is whatever the runner handed us.
func ttyFromLink(link string) string {
	if !strings.HasPrefix(link, "/dev/") || link == "/dev/null" {
		return sshTTY()
	}
	return link
}

// prettyName reads PRETTY_NAME out of os-release, honouring the quoting the
// format allows.
func prettyName() string {
	return prettyNameFrom([]string{"/etc/os-release", "/usr/lib/os-release"})
}

// prettyNameFrom is prettyName with the paths named, for the same reason
// machineIDFrom exists.
func prettyNameFrom(paths []string) string {
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			if value, ok := strings.CutPrefix(scanner.Text(), "PRETTY_NAME="); ok {
				_ = f.Close()
				return strings.Trim(value, `"'`)
			}
		}
		_ = f.Close()
	}
	return ""
}

// unameString is "Linux 6.12.0", uname -s and -r.
func unameString() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		// not covered: uname(2) fails only on a bad pointer, and this one
		// is a live local.
		return ""
	}
	return strings.TrimSpace(nullTerminated(u.Sysname[:]) + " " + nullTerminated(u.Release[:]))
}
