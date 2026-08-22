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
