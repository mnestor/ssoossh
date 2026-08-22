package principalsmap

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// FuzzPrincipalsMapYAML tests YAML unmarshalling of principals map data.
// Catches malformed YAML, unexpected types, null values, and edge cases.
func FuzzPrincipalsMapYAML(f *testing.F) {
	// Valid maps
	f.Add([]byte(`alice:
  - alice
  - admin`))

	f.Add([]byte(`bob:
  - bob`))

	f.Add([]byte(``)) // empty

	// Edge cases
	f.Add([]byte(`alice: []`))
	f.Add([]byte(`alice: null`))
	f.Add([]byte(`alice: ""`))

	// Malformed YAML
	f.Add([]byte(`not yaml: [unclosed`))

	// Type confusion
	f.Add([]byte(`alice: admin`)) // string instead of list

	f.Fuzz(func(t *testing.T, data []byte) {
		var m PrincipalsMap

		// Unmarshal should not panic
		err := yaml.Unmarshal(data, &m)

		// Error is acceptable for malformed input
		if err != nil {
			return
		}

		// If we got a map, structure should be sound
		for _, principals := range m {
			if principals != nil {
				for _, p := range principals {
					_ = p // verify no panic on each principal
				}
			}
		}
	})
}

// FuzzPrincipalsMapAllowed tests the Allowed method.
func FuzzPrincipalsMapAllowed(f *testing.F) {
	f.Add("alice")
	f.Add("bob")
	f.Add("")
	f.Add("unknown")

	f.Fuzz(func(t *testing.T, account string) {
		m := PrincipalsMap{
			"alice": []string{"alice", "admin"},
			"bob":   []string{"bob"},
		}

		// Allowed should never panic with any account name
		_ = m.Allowed(account, []string{})
		_ = m.Allowed(account, []string{"alice"})
		_ = m.Allowed(account, []string{"admin"})
	})
}
