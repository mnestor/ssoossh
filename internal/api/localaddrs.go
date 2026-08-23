package api

import (
	"net"
	"slices"
)

// LocalInterfaceAddresses returns the caller's own non-loopback IP
// addresses, gathered from every up network interface. Used to populate
// RequestedOptions.SourceAddresses so ssoosshd can union them with the
// address it observes the request coming from — a client behind NAT has a
// private local address that downstream hosts see when it connects, which
// is not the address ssoosshd sees when it mints the certificate. See
// docs/dev/ssoossh-context.md's "Certificate lifetime policy" and
// docs/dev/changes-next.md.
//
// Link-local addresses (fe80::/10, 169.254.0.0/16) are left out along with
// loopback. They are meaningful only within a single link, so they can
// neither support a source-address restriction nor identify this machine to
// anything a certificate would be presented to — and because net.IP.String()
// drops the IPv6 zone, one link-local address derived from a single MAC
// arrives identically from every interface carrying it (a bridge and its
// member, docker0 and its veths).
//
// The result is a set: the server treats SourceAddresses as a union when it
// folds in the observed source IP, and a repeat would be stored, displayed,
// and matched against for no gain.
//
// Best-effort: any error enumerating interfaces or addresses is swallowed
// and yields however much of the list was gathered before the failure
// (possibly empty) — this is audit/policy-support metadata, never a
// precondition for issuance.
func LocalInterfaceAddresses() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		// not covered: net.Interfaces() reads the OS network stack
		// directly, and this function takes no seam to substitute it, so a
		// failure cannot be induced from a test.
		return nil
	}

	var addrs []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			// not covered: as above, one interface's Addrs() failing needs
			// the OS network stack to fail.
			continue
		}
		for _, a := range ifaceAddrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() {
				// not covered: every address a real interface reports is a
				// *net.IPNet, and a loopback address surviving the
				// FlagLoopback filter above needs an interface no test
				// machine has.
				continue
			}
			if ipNet.IP.IsLinkLocalUnicast() {
				continue
			}
			addr := ipNet.IP.String()
			if !slices.Contains(addrs, addr) {
				addrs = append(addrs, addr)
			}
		}
	}
	return addrs
}
