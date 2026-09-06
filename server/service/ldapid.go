package service

import (
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/go-ldap/ldap/v3"
)

// binaryIDPrefix marks a stored directory ID that was captured from a
// binary attribute and hex-encoded.
//
// A marker is needed because the two forms are otherwise indistinguishable
// on the way back out: "abc123" is both a plausible text identifier and
// valid hex, and guessing wrong turns a filter for one entry into a filter
// that matches nothing. entryUUID and ipaUniqueID are printable UUID
// strings and never carry the prefix; objectGUID is sixteen raw bytes and
// always does. No directory issues a text identifier beginning with "0x",
// so the marker costs nothing to reserve.
const binaryIDPrefix = "0x"

// directoryIDValue reads the re-anchoring identifier off an entry, in the
// form it is stored and later searched by.
//
// Raw bytes rather than the string accessor, because the whole point of
// this value is that it is exact: Active Directory's objectGUID is sixteen
// arbitrary bytes, and reading it as text silently replaces every invalid
// sequence with U+FFFD, producing an "identifier" that matches nothing and
// collides with every other mangled one.
//
// Text values are kept as text so an operator reading user_ldap sees the
// UUID their directory shows them; binary values are hex-encoded behind
// binaryIDPrefix.
//
// Returns empty when attr is unconfigured or absent from the entry, which
// every caller treats as "no ID anchor for this user".
func directoryIDValue(entry *ldap.Entry, attr string) string {
	if attr == "" || entry == nil {
		return ""
	}
	raw := entry.GetRawAttributeValue(attr)
	if len(raw) == 0 {
		return ""
	}
	if isPrintableID(raw) {
		return string(raw)
	}
	return binaryIDPrefix + hex.EncodeToString(raw)
}

// isPrintableID reports whether raw is safe to keep as text: valid UTF-8
// with no control characters. Control bytes are excluded as well as invalid
// ones because a NUL or a newline inside an identifier is a binary value
// that happens to decode, and storing it as text would put it in log lines
// and JSON documents in a form nothing can search back.
func isPrintableID(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	for _, r := range string(raw) {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// directoryIDFilter builds the equality filter that finds an entry by its
// stored identifier, escaped for the form the value is in.
//
// A hex-encoded binary value becomes the backslash-escaped byte sequence
// LDAP requires — (objectGUID=\a1\b2...) — because a binary attribute has
// no text representation a directory will match against. A text value goes
// through the same RFC 4515 escaping every other filter value does, and for
// the same reason: the operator does not get to opt out of escaping, even
// for a value the directory itself produced.
//
// Returns empty when either half is missing, which the caller reads as
// "cannot resolve by ID" and falls through to the DN.
func directoryIDFilter(attr, stored string) string {
	if attr == "" || stored == "" {
		return ""
	}

	if raw, ok := strings.CutPrefix(stored, binaryIDPrefix); ok {
		decoded, err := hex.DecodeString(raw)
		if err != nil || len(decoded) == 0 {
			// A malformed marker is a corrupt row, not a value to guess
			// at: resolution falls back to the DN rather than searching
			// for something arbitrary.
			return ""
		}
		var b strings.Builder
		b.Grow(len(attr) + 3 + len(decoded)*3)
		fmt.Fprintf(&b, "(%s=", ldap.EscapeFilter(attr))
		for _, c := range decoded {
			fmt.Fprintf(&b, "\\%02x", c)
		}
		b.WriteString(")")
		return b.String()
	}

	return fmt.Sprintf("(%s=%s)", ldap.EscapeFilter(attr), ldap.EscapeFilter(stored))
}
