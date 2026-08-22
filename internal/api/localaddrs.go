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
// docs/ssoossh-context.md's "Certificate lifetime policy" and
// docs/certificate-audit-metadata-plan.md.
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
		return nil // excluded from coverage: net.Interfaces() failing isn't reproducible without mocking the OS network stack, see exclude-from-coverage.txt
	}

	var addrs []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		ifaceAddrs, err := iface.Addrs()
		if err != nil {
			continue // excluded from coverage: a single interface's Addrs() failing isn't reproducible without mocking the OS network stack, see exclude-from-coverage.txt
		}
		for _, a := range ifaceAddrs {
			ipNet, ok := a.(*net.IPNet)
			if !ok || ipNet.IP.IsLoopback() {
				continue // excluded from coverage: every real interface address is a *net.IPNet, and a loopback address surviving the FlagLoopback filter above isn't reproducible on a real test machine, see exclude-from-coverage.txt
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
