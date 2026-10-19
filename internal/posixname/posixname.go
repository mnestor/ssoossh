// Package posixname holds the POSIX account name rules and the gecos
// sanitiser shared by ssoosshd and ssoossh-nss, so the server never stores
// or serves a name a host would refuse, and never serves a gecos field a
// host would have to mangle.
//
// The name rules are systemd's relaxed ones, valid_user_group_name() with
// VALID_USER_RELAX in src/basic/user-util.c, because nss-systemd applies
// exactly those before it asks any userdb service. On top of them a name is
// capped at MaxNameBytes, which systemd's relaxed mode does not do.
package posixname

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxNameBytes is the longest name accepted, in bytes: the width of utmp's
// ut_user field.
const MaxNameBytes = 32

// MaxGECOSBytes is the longest gecos value SanitizeGECOS returns, in bytes.
const MaxGECOSBytes = 256

// ErrInvalid wraps every error Valid returns, so callers can tell a
// rejected name from other failures with errors.Is.
var ErrInvalid = errors.New("invalid POSIX name")

// Valid reports whether name may be a POSIX user or group name, returning
// an error wrapping ErrInvalid that names the rule it breaks. Names are
// compared byte for byte everywhere, so nothing here folds case or trims.
func Valid(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty", ErrInvalid)
	}
	if len(name) > MaxNameBytes {
		return fmt.Errorf("%w: %d bytes, more than %d", ErrInvalid, len(name), MaxNameBytes)
	}
	// systemd rejects only a plain space here, not every whitespace rune.
	if strings.HasPrefix(name, " ") || strings.HasSuffix(name, " ") {
		return fmt.Errorf("%w: leading or trailing space", ErrInvalid)
	}
	if !utf8.ValidString(name) {
		return fmt.Errorf("%w: not valid UTF-8", ErrInvalid)
	}
	if hasASCIIControl(name) {
		return fmt.Errorf("%w: contains a control character", ErrInvalid)
	}
	if strings.ContainsAny(name, ":/") {
		return fmt.Errorf("%w: contains ':' or '/'", ErrInvalid)
	}
	if allDigits(name) {
		return fmt.Errorf("%w: all digits, which reads as a uid", ErrInvalid)
	}
	// "-" alone is caught here too: systemd's in_charset() is true for the
	// empty rest of the string.
	if name[0] == '-' && allDigits(name[1:]) {
		return fmt.Errorf("%w: a negative number, which reads as a uid", ErrInvalid)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("%w: '.' or '..'", ErrInvalid)
	}
	return nil
}

// hasASCIIControl reports a byte below space or DEL, systemd's
// char_is_cc(). Bytes of multi-byte UTF-8 sequences are all 0x80 or above,
// so they never match.
func hasASCIIControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < ' ' || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// allDigits reports whether s holds only ASCII digits. It is true for the
// empty string, as systemd's in_charset() is.
func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// SanitizeGECOS returns s made safe for the gecos field of a passwd entry,
// and whether it had to change anything. In order: invalid UTF-8 is
// replaced by fallback; every control character and every ':' (the passwd
// field separator; a newline is the record separator) becomes a space; runs
// of whitespace collapse to one space and the ends are trimmed; the result
// is cut to MaxGECOSBytes on a rune boundary; an empty result is replaced
// by fallback. The fallback is normally the account name, already Valid.
func SanitizeGECOS(s, fallback string) (string, bool) {
	out := s
	if !utf8.ValidString(out) {
		out = fallback
	}
	out = strings.Map(func(r rune) rune {
		if r == ':' || unicode.IsControl(r) {
			return ' '
		}
		return r
	}, out)
	out = strings.Join(strings.Fields(out), " ")
	out = truncateRunes(out, MaxGECOSBytes)
	if out == "" {
		out = fallback
	}
	return out, out != s
}

// truncateRunes cuts s to at most limit bytes without splitting a rune, and
// trims a space the cut leaves at the end.
func truncateRunes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " ")
}
