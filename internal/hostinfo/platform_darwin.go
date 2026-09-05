package hostinfo

import (
	"strings"

	"golang.org/x/sys/unix"
)

// machineID is the kern.uuid sysctl, macOS's host UUID.
//
// Not /etc/machine-id, which macOS does not have, and not gethostuuid(2),
// which is what pam_ssoossh calls (src/hostinfo.c): that is a libSystem
// entry point with no wrapper in golang.org/x/sys, and the client is built
// with cgo off. kern.uuid is the same identifier reachable as a sysctl.
func machineID() string {
	uuid, err := unix.Sysctl("kern.uuid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(uuid)
}

// osName is the marketing version and the kernel: "macOS 26.5.2 Darwin
// 25.5.0". The C module composes uname -s and -r alone; the product version
// is added because "Darwin 25.5.0" names a release almost nobody can place.
//
// kern.osproductversion is absent before macOS 10.13, and the kernel half
// is composed on its own when it cannot be read.
func osName() string {
	parts := make([]string, 0, 2)
	if product, err := unix.Sysctl("kern.osproductversion"); err == nil && product != "" {
		parts = append(parts, "macOS "+strings.TrimSpace(product))
	}
	if kernel := sysctlPair("kern.ostype", "kern.osrelease"); kernel != "" {
		parts = append(parts, kernel)
	}
	return strings.Join(parts, " ")
}

// ttyName has no cheap answer here: there is no /proc to read fd 0 through,
// and ttyname(3) is another libSystem call this build cannot reach. An SSH
// session still names its terminal in the environment, which covers the
// case an approver most wants to see.
func ttyName() string { return sshTTY() }

// sysctlPair joins two sysctls with a space, or returns empty if either is
// unreadable.
func sysctlPair(first, second string) string {
	a, err := unix.Sysctl(first)
	if err != nil {
		return ""
	}
	b, err := unix.Sysctl(second)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimSpace(a) + " " + strings.TrimSpace(b))
}
