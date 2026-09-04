package principalsmap

import "testing"

// FuzzPrincipalsMapParse tests the hand-rolled parser that replaced
// gopkg.in/yaml.v3. Catches malformed input, unexpected shapes, null
// values, and edge cases. An error is always an acceptable answer — what
// must never happen is a panic, or a map that parsed "successfully" while
// carrying something the file did not say.
func FuzzPrincipalsMapParse(f *testing.F) {
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
	f.Add([]byte(`alice: ~`))
	f.Add([]byte(`alice: ""`))
	f.Add([]byte(`alice: [admin, ops]`))
	f.Add([]byte("alice:   # trailing comment\n  - admin # and another"))
	f.Add([]byte("# only a comment"))
	f.Add([]byte("'alice':\n  - \"admin\""))
	f.Add([]byte("alice:\r\n  - admin\r\n")) // CRLF
	f.Add([]byte("alice:\n- admin"))         // zero-indent list

	// Malformed
	f.Add([]byte(`not yaml: [unclosed`))
	f.Add([]byte("alice:\n\t- tabbed"))
	f.Add([]byte("alice:\n  admin:\n    - ops")) // nested mapping
	f.Add([]byte("  - orphaned item"))
	f.Add([]byte("alice:\n  - admin\nalice:\n  - ops")) // duplicate account

	// Type confusion
	f.Add([]byte(`alice: admin`)) // string instead of list

	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := parse(data)

		// Error is acceptable for malformed input.
		if err != nil {
			if m != nil {
				t.Errorf("parse() returned both a map (%#v) and an error (%v)", m, err)
			}
			return
		}

		// A parsed map must be sound: no account or principal may be
		// empty, and every principal it lists must be one Allowed agrees
		// with — a parser that dropped or mangled an entry would show up
		// as a disagreement between the two.
		for account, principals := range m {
			if account == "" {
				t.Errorf("parse(%q) accepted an empty account name", data)
			}
			for _, p := range principals {
				if p == "" {
					t.Errorf("parse(%q) accepted an empty principal for account %q", data, account)
				}
			}
			if len(principals) > 0 && !m.Allowed(account, principals) {
				t.Errorf("parse(%q) produced a map whose own principals %v are not Allowed for %q", data, principals, account)
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
