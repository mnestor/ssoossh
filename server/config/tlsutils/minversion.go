package tlsutils

import (
	"crypto/tls"
	"fmt"
	"log/slog"
	"strings"
)

// tlsVersions is the source of truth for supported TLS versions: add a new
// tls.VersionTLS* constant here and MinVersion picks it up automatically.
var tlsVersions = map[uint16]string{
	tls.VersionTLS10: "10",
	tls.VersionTLS11: "11",
	tls.VersionTLS12: "12",
	tls.VersionTLS13: "13",
}

// deprecatedTLSVersions are versions MinVersion still resolves but warns
// about: RFC 8996 deprecates TLS 1.0/1.1 as insufficiently secure.
var deprecatedTLSVersions = map[uint16]bool{
	tls.VersionTLS10: true,
	tls.VersionTLS11: true,
}

// minVersionByDigits is the reverse lookup, built once from tlsVersions.
var minVersionByDigits = func() map[string]uint16 {
	m := make(map[string]uint16, len(tlsVersions))
	for v, digits := range tlsVersions {
		m[digits] = v
	}
	return m
}()

// minVersionTokenStripper removes the known prefix words and separators
// from a TLS version name, in any case, leaving only what should be a bare
// digit code (e.g. "12") if the input is well-formed. Unlike stripping
// every non-digit character, anything left over after this — any
// unrecognized word or symbol — makes the lookup in MinVersion fail, so
// input that merely contains the right digits somewhere isn't silently
// accepted.
var minVersionTokenStripper = strings.NewReplacer(
	"version", "",
	"tls", "",
	".", "",
	"-", "",
	"_", "",
	" ", "",
)

// MinVersion resolves a TLS version name (e.g. "TLS1.2", "tls12",
// "VersionTLS12", "tls-1-2") into the numeric value used by
// tls.Config.MinVersion. It tolerates case and the "tls"/"version" prefix
// and "."/"-"/"_"/" " separators, but requires everything else in n to
// reduce to exactly one of the known version digit codes — unrecognized
// text is rejected rather than ignored.
func MinVersion(n string) (uint16, error) {
	normalized := minVersionTokenStripper.Replace(strings.ToLower(n))

	if v, ok := minVersionByDigits[normalized]; ok {
		if deprecatedTLSVersions[v] {
			slog.Warn("configured TLS minimum version is deprecated (RFC 8996)", "input", n)
		}
		return v, nil
	}
	return 0, fmt.Errorf("unknown minversion string (%s) should be one of (TLS1.0, TLS1.1, TLS1.2, TLS1.3)", n)
}
