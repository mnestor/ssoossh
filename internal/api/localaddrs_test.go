package api

import (
	"net"
	"testing"
)

// TestLocalInterfaceAddresses_ShouldReturnParseableNonLoopbackAddresses runs
// against the real network stack (no mock — net.Interfaces() has no
// interface seam in the standard library worth introducing for this), so it
// can't assert a fixed count: what's available depends on the machine
// running the test. It asserts the two properties that must always hold —
// every returned string is a valid IP, and none of them is loopback, which
// is exactly what LocalInterfaceAddresses is documented to filter out.
func TestLocalInterfaceAddresses_ShouldReturnParseableNonLoopbackAddresses(t *testing.T) {
	t.Parallel()

	addrs := LocalInterfaceAddresses()
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			t.Errorf("got %q, want a parseable IP address", a)
			continue
		}
		if ip.IsLoopback() {
			t.Errorf("got loopback address %q, want it filtered out", a)
		}
	}
}

// TestLocalInterfaceAddresses_ShouldNotPanicWhenCalledRepeatedly is a
// light smoke test: this is called once per certificate request, so it
// should behave the same way on every call.
func TestLocalInterfaceAddresses_ShouldNotPanicWhenCalledRepeatedly(t *testing.T) {
	t.Parallel()

	for range 3 {
		_ = LocalInterfaceAddresses()
	}
}

// TestLocalInterfaceAddresses_ShouldNotRepeatAnAddress guards the case that
// crashed the approval page: net.IP.String() drops an IPv6 zone, so one
// link-local address can be reported by several interfaces as the same
// string. The result is documented as a set, so no value may repeat.
//
// Like the tests above this runs against the real stack, so it asserts the
// property rather than a fixed list — a machine with a single interface
// still exercises it, just trivially.
func TestLocalInterfaceAddresses_ShouldNotRepeatAnAddress(t *testing.T) {
	t.Parallel()

	addrs := LocalInterfaceAddresses()
	seen := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		if seen[a] {
			t.Errorf("got %q more than once, want each address at most once in %v", a, addrs)
		}
		seen[a] = true
	}
}

// TestLocalInterfaceAddresses_ShouldNotReturnLinkLocalAddresses covers the
// other half of the filter: fe80::/10 and 169.254.0.0/16 are reachable only
// on one link, so they can neither carry a source-address restriction nor
// identify this machine to a host the certificate is presented to.
func TestLocalInterfaceAddresses_ShouldNotReturnLinkLocalAddresses(t *testing.T) {
	t.Parallel()

	for _, a := range LocalInterfaceAddresses() {
		ip := net.ParseIP(a)
		if ip == nil {
			continue // covered by the parseability test above
		}
		if ip.IsLinkLocalUnicast() {
			t.Errorf("got link-local address %q, want it filtered out", a)
		}
	}
}
