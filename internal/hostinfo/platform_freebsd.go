package hostinfo

import (
	"strings"

	"golang.org/x/sys/unix"
)

// machineID is kern.hostuuid, the same sysctl pam_ssoossh reads on FreeBSD
// (src/hostinfo.c, read_machine_id).
func machineID() string {
	uuid, err := unix.Sysctl("kern.hostuuid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(uuid)
}

// osName is uname -s and -r, which on FreeBSD already names the release:
// "FreeBSD 14.1-RELEASE".
func osName() string {
	var u unix.Utsname
	if err := unix.Uname(&u); err != nil {
		// not covered: uname(2) fails only on a bad pointer.
		return ""
	}
	return strings.TrimSpace(nullTerminated(u.Sysname[:]) + " " + nullTerminated(u.Release[:]))
}

// ttyName: no /proc by default, and ttyname(3) is a libc call this cgo-free
// build cannot reach. An SSH session still names its terminal.
func ttyName() string { return sshTTY() }
