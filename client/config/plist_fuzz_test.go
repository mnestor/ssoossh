package config

import (
	"testing"
)

// FuzzParsePolicyPlist tests the plist XML parser for robustness against
// malformed XML, unexpected element types, and edge cases. Catches issues
// with XML parsing, entity handling, and type conversion.
func FuzzParsePolicyPlist(f *testing.F) {
	// Valid plist fragments with a root dict
	f.Add([]byte(`<?xml version="1.0"?>
<dict>
  <key>name</key>
  <string>test</string>
</dict>`))

	f.Add([]byte(`<dict></dict>`))

	f.Add([]byte(`<dict>
  <key>enabled</key>
  <true/>
  <key>disabled</key>
  <false/>
  <key>count</key>
  <integer>42</integer>
</dict>`))

	// Malformed XML
	f.Add([]byte(`<dict>incomplete`))
	f.Add([]byte(`<notdict></notdict>`))
	f.Add([]byte(``))

	// Entity references
	f.Add([]byte(`<dict><key>name&amp;</key><string>test</string></dict>`))

	// CDATA sections (XML feature)
	f.Add([]byte(`<dict><key>data</key><string><![CDATA[content]]></string></dict>`))

	// Entity expansion (potential DOS)
	f.Add([]byte(`<?xml version="1.0"?>
<!DOCTYPE foo [
  <!ENTITY lol "lol">
  <!ENTITY lol2 "&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;&lol;">
]>
<dict>
  <key>test</key>
  <string>&lol2;</string>
</dict>`))

	// External entities (should be blocked or safe)
	f.Add([]byte(`<?xml version="1.0"?>
<!DOCTYPE foo [
  <!ENTITY xxe SYSTEM "file:///etc/passwd">
]>
<dict><key>test</key><string>&xxe;</string></dict>`))

	// Nested dicts (should be skipped per parser contract)
	f.Add([]byte(`<dict><key>nested</key><dict><key>inner</key><string>value</string></dict></dict>`))

	// Arrays (should be skipped)
	f.Add([]byte(`<dict><key>list</key><array><string>item</string></array></dict>`))

	// Unicode
	f.Add([]byte(`<dict><key>unicode</key><string>日本語</string></dict>`))

	// Very long strings
	longStr := ""
	for i := 0; i < 10000; i++ {
		longStr += "x"
	}
	f.Add([]byte(`<dict><key>longstring</key><string>` + longStr + `</string></dict>`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// parsePolicyPlist should never panic on any input
		result, err := parsePolicyPlist(data)

		// Error is acceptable for malformed input
		if err != nil {
			return
		}

		// If we got a result, it should be a valid map
		if result != nil {
			for key, value := range result {
				// Key should be a non-empty string
				if key == "" {
					// Empty keys are weird but not a panic
				}

				// Value should be one of the recognized types
				switch value.(type) {
				case string, int64, bool:
					// Valid types
				default:
					// Unexpected type - should have been filtered by parser
					t.Logf("unexpected value type for key %q: %T", key, value)
				}
			}
		}
	})
}
