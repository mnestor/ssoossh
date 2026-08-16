// Package principalsmap reads a local, hand-authored file mapping a local
// account name to the certificate principals allowed to assume it. It is
// standalone: nothing about it depends on or coordinates with the server's
// host-mapping/sync system — a host admin owns and edits this file directly
// on the machine it applies to.
package principalsmap

import (
	"fmt"
	"os"
	"slices"

	"gopkg.in/yaml.v3"
)

// PrincipalsMap maps a local account name to the certificate principals
// allowed to assume it, e.g.:
//
//	alice:
//	  - alice
//	  - admin
type PrincipalsMap map[string][]string

// LoadFromFile reads and parses a principals map file in YAML format.
func LoadFromFile(path string) (PrincipalsMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read principals map: %w", err)
	}

	var m PrincipalsMap
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse principals map: %w", err)
	}
	return m, nil
}

// Allowed reports whether any of certPrincipals is listed as allowed to
// assume account. An account with no entry in the map is never allowed,
// even if a certificate principal happens to match its name — callers that
// want an exact-match fallback do that themselves when no map applies.
func (m PrincipalsMap) Allowed(account string, certPrincipals []string) bool {
	allowed, ok := m[account]
	if !ok {
		return false
	}
	for _, p := range certPrincipals {
		if slices.Contains(allowed, p) {
			return true
		}
	}
	return false
}
