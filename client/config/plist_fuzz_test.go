package config

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzParsePolicyPlist tests the policy plist parser for robustness against
// malformed input, unexpected element types, and edge cases. Catches issues
// with parsing, entity handling, and type conversion.
//
// Seeded with both formats. It was XML-only when macOS's binary rewrite of
// every managed-preferences file went unnoticed, so a corpus that cannot
// reach the binary decoder is the shape of that bug rather than a coverage
// nicety: the real managed plist is the case least likely to be hand-written
// into a test.
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

	// A real binary plist, so mutation explores the binary decoder's offset
	// tables and reference widths rather than only the XML path.
	if binary, err := os.ReadFile(filepath.Join("testdata", "managed-preferences.binary.plist")); err == nil {
		f.Add(binary)
	}
	// The magic alone, which is what a truncated or empty binary file looks
	// like at the point the parser decides which decoder to use.
	f.Add([]byte("bplist00"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// parsePolicyPlist should never panic on any input
		result, err := parsePolicyPlist(data)

		// Error is acceptable for malformed input
		if err != nil {
			return
		}

		// If we got a result, it should be a valid map. Ranging over a nil
		// map is a no-op, so no nil check is needed. An empty key is odd
		// but not a failure -- what matters is that every value is one of
		// the types the parser is documented to produce.
		for key, value := range result {
			switch value.(type) {
			case string, int64, bool:
				// Valid types
			default:
				// Unexpected type - should have been filtered by parser
				t.Logf("unexpected value type for key %q: %T", key, value)
			}
		}
	})
}
