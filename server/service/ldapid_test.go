package service

import (
	"testing"

	"github.com/go-ldap/ldap/v3"
)

// entryWithAttr builds a one-attribute entry the way the directory library
// hands one back, with the raw bytes preserved.
func entryWithAttr(name string, value []byte) *ldap.Entry {
	return &ldap.Entry{
		DN: "uid=alice,ou=People,dc=example,dc=net",
		Attributes: []*ldap.EntryAttribute{
			{Name: name, Values: []string{string(value)}, ByteValues: [][]byte{value}},
		},
	}
}

func TestDirectoryIDValue_ShouldKeepAPrintableIdentifierAsText(t *testing.T) {
	t.Parallel()

	entry := entryWithAttr("entryUUID", []byte("8f14e45f-ea8f-4f2d-9c1b-3a7b5d2e6c40"))

	if got := directoryIDValue(entry, "entryUUID"); got != "8f14e45f-ea8f-4f2d-9c1b-3a7b5d2e6c40" {
		t.Errorf("got %q, want the UUID as the directory shows it", got)
	}
}

func TestDirectoryIDValue_ShouldHexEncodeABinaryIdentifier(t *testing.T) {
	t.Parallel()

	// A plausible objectGUID: sixteen arbitrary bytes, including ones that
	// are not valid UTF-8 at all.
	guid := []byte{0xa1, 0xb2, 0xc3, 0xd4, 0x00, 0xff, 0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70, 0x80, 0x90, 0xf0}

	got := directoryIDValue(entryWithAttr("objectGUID", guid), "objectGUID")

	if got != "0xa1b2c3d400ff102030405060708090f0" {
		t.Errorf("got %q, want the hex form behind the binary marker", got)
	}
}

func TestDirectoryIDValue_ShouldBeEmptyWhenTheAttributeIsUnconfigured(t *testing.T) {
	t.Parallel()

	if got := directoryIDValue(entryWithAttr("entryUUID", []byte("x")), ""); got != "" {
		t.Errorf("got %q, want empty: an unconfigured attribute anchors nothing", got)
	}
}

func TestDirectoryIDValue_ShouldBeEmptyWhenTheAttributeIsAbsent(t *testing.T) {
	t.Parallel()

	if got := directoryIDValue(entryWithAttr("cn", []byte("Alice")), "entryUUID"); got != "" {
		t.Errorf("got %q, want empty: the entry does not carry the attribute", got)
	}
}

func TestDirectoryIDValue_ShouldRejectAControlCharacterAsText(t *testing.T) {
	t.Parallel()

	// Valid UTF-8, but a NUL inside an identifier means it is binary that
	// happens to decode. Keeping it as text would put an unsearchable value
	// in the database.
	got := directoryIDValue(entryWithAttr("nsuniqueid", []byte("ab\x00cd")), "nsuniqueid")

	if got != "0x6162006364" {
		t.Errorf("got %q, want the hex form", got)
	}
}

func TestDirectoryIDFilter_ShouldBuildAnEqualityFilterForATextIdentifier(t *testing.T) {
	t.Parallel()

	got := directoryIDFilter("entryUUID", "8f14e45f-ea8f-4f2d-9c1b-3a7b5d2e6c40")

	if got != "(entryUUID=8f14e45f-ea8f-4f2d-9c1b-3a7b5d2e6c40)" {
		t.Errorf("got %q, want the plain equality filter", got)
	}
}

func TestDirectoryIDFilter_ShouldEscapeATextIdentifier(t *testing.T) {
	t.Parallel()

	// The operator does not get to opt out of escaping, even for a value
	// the directory itself produced.
	got := directoryIDFilter("entryUUID", "a)b*c")

	if got != `(entryUUID=a\29b\2ac)` {
		t.Errorf("got %q, want the RFC 4515 escaped form", got)
	}
}

func TestDirectoryIDFilter_ShouldEmitByteEscapesForABinaryIdentifier(t *testing.T) {
	t.Parallel()

	got := directoryIDFilter("objectGUID", "0xa1b200ff")

	if got != `(objectGUID=\a1\b2\00\ff)` {
		t.Errorf("got %q, want the backslash-escaped byte sequence AD requires", got)
	}
}

func TestDirectoryIDFilter_ShouldBeEmptyForAMalformedBinaryMarker(t *testing.T) {
	t.Parallel()

	// A corrupt row is not a value to guess at: the caller falls back to
	// the DN rather than searching for something arbitrary.
	if got := directoryIDFilter("objectGUID", "0xnothex"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestDirectoryIDFilter_ShouldBeEmptyWhenEitherHalfIsMissing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		attr   string
		stored string
	}{
		{name: "should be empty when the attribute is unconfigured", attr: "", stored: "abc"},
		{name: "should be empty when nothing is stored", attr: "entryUUID", stored: ""},
		{name: "should be empty when both are missing", attr: "", stored: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := directoryIDFilter(tt.attr, tt.stored); got != "" {
				t.Errorf("got %q, want empty", got)
			}
		})
	}
}
