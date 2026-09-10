package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParsePolicyPlist_ShouldDecodeEachSupportedScalarType(t *testing.T) {
	data := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>server</key>
	<string>https://ssh.example.com</string>
	<key>sshkey.size</key>
	<integer>384</integer>
	<key>insecure_skip_verify</key>
	<true/>
	<key>use_agent</key>
	<false/>
</dict>
</plist>
`)

	values, err := parsePolicyPlist(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := map[string]any{
		"server":               "https://ssh.example.com",
		"sshkey.size":          int64(384),
		"insecure_skip_verify": true,
		"use_agent":            false,
	}
	if len(values) != len(want) {
		t.Fatalf("got %d values, want %d: %v", len(values), len(want), values)
	}
	for k, v := range want {
		if values[k] != v {
			t.Errorf("key %q: got %v, want %v", k, values[k], v)
		}
	}
}

func TestParsePolicyPlist_ShouldReturnAnEmptyMapForAnEmptyDict(t *testing.T) {
	data := []byte(`<plist version="1.0"><dict></dict></plist>`)

	values, err := parsePolicyPlist(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(values) != 0 {
		t.Errorf("got %v, want an empty map", values)
	}
}

func TestParsePolicyPlist_ShouldParseArraysOfStringsAndSkipOthers(t *testing.T) {
	data := []byte(`<plist version="1.0">
<dict>
	<key>forbidden_list</key>
	<array><string>a</string><string>b</string></array>
	<key>ignored_dict</key>
	<dict><key>nested</key><string>x</string></dict>
	<key>server</key>
	<string>https://ssh.example.com</string>
</dict>
</plist>`)

	values, err := parsePolicyPlist(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Arrays of strings are now parsed
	if arr, ok := values["forbidden_list"]; ok {
		if arrSlice, ok := arr.([]any); ok {
			if len(arrSlice) != 2 || arrSlice[0] != "a" || arrSlice[1] != "b" {
				t.Errorf("got forbidden_list %v, want [a b]", arrSlice)
			}
		} else {
			t.Errorf("got forbidden_list type %T, want []any", arr)
		}
	} else {
		t.Error("expected the <array> of strings to be parsed")
	}

	// Dicts are still skipped
	if _, ok := values["ignored_dict"]; ok {
		t.Error("expected the <dict> value to be skipped")
	}
	if values["server"] != "https://ssh.example.com" {
		t.Errorf("got server %v, want the key after the skipped ones to still be read", values["server"])
	}
}

func TestParsePolicyPlist_ShouldRejectMalformedXML(t *testing.T) {
	data := []byte(`<plist version="1.0"><dict><key>server</key><string>unterminated</dict>`)

	if _, err := parsePolicyPlist(data); err == nil {
		t.Fatal("expected an error for malformed XML")
	}
}

func TestParsePolicyPlist_ShouldRejectADocumentWithNoRootDict(t *testing.T) {
	data := []byte(`<plist version="1.0"></plist>`)

	if _, err := parsePolicyPlist(data); err == nil {
		t.Fatal("expected an error for a document with no root <dict>")
	}
}

func TestParsePolicyPlist_ShouldRejectAnInvalidInteger(t *testing.T) {
	data := []byte(`<plist version="1.0"><dict><key>sshkey.size</key><integer>not-a-number</integer></dict></plist>`)

	if _, err := parsePolicyPlist(data); err == nil {
		t.Fatal("expected an error for an unparsable <integer>")
	}
}

func TestParsePolicyPlist_ShouldParseArraysSkippingNonStringElements(t *testing.T) {
	data := []byte(`<plist version="1.0">
<dict>
	<key>mixed_array</key>
	<array><string>a</string><integer>1</integer><string>b</string></array>
</dict>
</plist>`)

	values, err := parsePolicyPlist(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if arr, ok := values["mixed_array"].([]any); ok {
		if len(arr) != 2 || arr[0] != "a" || arr[1] != "b" {
			t.Errorf("got mixed_array %v, want [a b] (integer skipped)", arr)
		}
	} else {
		t.Errorf("got mixed_array type %T, want []any", values["mixed_array"])
	}
}

// TestParsePolicyPlist_BinaryFormat is the regression test for the bug an
// XML-only fixture hid: macOS rewrites every file under /Library/Managed
// Preferences as an Apple binary property list, so on a real managed Mac
// the parser saw bplist00, reported it as "XML syntax error ... invalid
// UTF-8", and failed the whole config load. Device-scoped policy had never
// loaded on managed hardware.
//
// The fixture is a genuine binary plist written by CPython's plistlib, not
// by the library under test: a round trip through one implementation's own
// encoder would not have caught this and would not catch its successor.
func TestParsePolicyPlist_ShouldDecodeABinaryPlist(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("testdata", "managed-preferences.binary.plist"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if !bytes.HasPrefix(data, []byte("bplist00")) {
		t.Fatalf("fixture is not a binary plist; magic = %q", data[:8])
	}

	got, err := parsePolicyPlist(data)
	if err != nil {
		t.Fatalf("parsePolicyPlist() error = %v", err)
	}

	tests := []struct {
		key  string
		want any
	}{
		{"server", "https://ssoossh.nasa.gov"},
		{"insecure_skip_verify", false},
		{"use_agent", true},
		{"fallback_file_agent", false},
		{"try_open_browser", true},
		// The flat dotted key, confirmed to survive the MDM round trip on
		// a real managed Mac.
		{"sshkey.type", "ecdsa"},
		{"sshkey.size", int64(384)},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got[tt.key] != tt.want {
				t.Errorf("got %#v (%T), want %#v", got[tt.key], got[tt.key], tt.want)
			}
		})
	}

	ext, ok := got["forbidden_certificate_extensions"].([]any)
	if !ok {
		t.Fatalf("forbidden_certificate_extensions = %#v, want a list", got["forbidden_certificate_extensions"])
	}
	if len(ext) != 2 || ext[0] != "permit-agent-forwarding" || ext[1] != "permit-port-forwarding" {
		t.Errorf("got extensions %#v, want the two configured names in order", ext)
	}
}

// Which on-disk format macOS happened to write must not change which
// settings apply, so the binary path skips exactly what the XML path skips.
func TestParsePolicyPlist_ShouldSkipUnsupportedTypesInABinaryPlist(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("testdata", "managed-preferences.binary.plist"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	got, err := parsePolicyPlist(data)
	if err != nil {
		t.Fatalf("parsePolicyPlist() error = %v", err)
	}

	for _, key := range []string{"ignored_nested_dict", "ignored_real", "ignored_data"} {
		if v, present := got[key]; present {
			t.Errorf("%s should have been skipped, got %#v", key, v)
		}
	}
}

// A truncated binary plist is an error, not a panic and not a silent empty
// policy: an unreadable file must never quietly mean "no settings locked".
func TestParsePolicyPlist_ShouldErrorOnATruncatedBinaryPlist(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join("testdata", "managed-preferences.binary.plist"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	if _, err := parsePolicyPlist(data[:len(data)/2]); err == nil {
		t.Error("parsePolicyPlist() error = nil, want an error for a truncated binary plist")
	}
}
