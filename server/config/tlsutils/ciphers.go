package tlsutils

import (
	"crypto/tls"
	"fmt"
)

// cipherSuiteByName builds a lookup from crypto/tls cipher suite name to ID,
// covering only the suites the stdlib considers secure -- insecure suite
// names are deliberately unresolvable so a config typo or legacy-interop
// attempt can't silently downgrade the server's TLS security.
func cipherSuiteByName() map[string]uint16 {
	m := make(map[string]uint16)
	for _, cs := range tls.CipherSuites() {
		m[cs.Name] = cs.ID
	}
	return m
}

// CipherSuites resolves configCipherSuites, a list of crypto/tls cipher
// suite names, into the numeric IDs used by tls.Config.CipherSuites,
// deduplicating while preserving the first occurrence's order. It returns
// an error if any name isn't recognized. An empty or nil list resolves to
// nil, leaving tls.Config.CipherSuites unset so Go's defaults apply:
// net/http's HTTP/2 setup rejects any non-nil list that lacks the suites
// HTTP/2 requires, so "no restriction" must be nil, not an empty slice.
func CipherSuites(configCipherSuites []string) ([]uint16, error) {
	if len(configCipherSuites) == 0 {
		return nil, nil
	}

	byName := cipherSuiteByName()

	suites := make([]uint16, 0, len(configCipherSuites))
	seen := make(map[uint16]bool, len(configCipherSuites))
	for _, name := range configCipherSuites {
		id, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("tlsconfig: unknown cipher suite name %q", name)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		suites = append(suites, id)
	}
	return suites, nil
}
