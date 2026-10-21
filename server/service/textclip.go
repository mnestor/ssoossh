package service

import (
	"strings"
	"unicode/utf8"
)

// clipUTF8 bounds s to at most n bytes of valid UTF-8, for text stored in a
// plain text column. A byte cut can split a multibyte character, and
// Postgres refuses the invalid sequence that leaves ("invalid byte sequence
// for encoding UTF8"), failing the whole update. Invalid bytes already in s
// (a provider's error body is arbitrary) are dropped first, then the cut
// backs off to the start of the character it would split.
func clipUTF8(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
