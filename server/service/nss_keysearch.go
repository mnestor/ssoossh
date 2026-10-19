package service

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

// NSSKeyAddressSearch is a key list search read as an address or a range:
// the usage rows to match are those whose source_ip_key lies between First
// and Last, both inclusive (equal for one address).
type NSSKeyAddressSearch struct {
	// Display is the search as the server read it, normalised: an IPv4
	// address written IPv4-mapped is shown as IPv4, and a range has its
	// host bits cleared ("10.0.4.7/24" is "10.0.4.0/24").
	Display string
	// Range is whether the search is a range rather than one address.
	Range bool

	First []byte
	Last  []byte
}

// ParseNSSKeyAddressSearch reads q as an address search. It answers nil
// when q does not look like an address, so the caller searches the text
// columns instead, and a 400 naming the problem when q looks like an
// address or a range but is not one: a typo there must never turn into a
// text search that quietly finds nothing.
func ParseNSSKeyAddressSearch(q string) (*NSSKeyAddressSearch, error) {
	q = strings.TrimSpace(q)
	if !looksLikeAddress(q) {
		return nil, nil
	}
	if strings.Contains(q, "/") {
		return parseNSSKeyRange(q)
	}
	addr, err := netip.ParseAddr(q)
	if err != nil {
		return nil, &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("%s is not a valid address: %s.", q, addressProblem(q))}
	}
	addr = addr.WithZone("").Unmap()
	key := addr.As16()
	return &NSSKeyAddressSearch{Display: addr.String(), First: key[:], Last: key[:]}, nil
}

// parseNSSKeyRange reads q, which holds a "/", as a CIDR range.
func parseNSSKeyRange(q string) (*NSSKeyAddressSearch, error) {
	invalid := func(problem string) error {
		return &errorresponses.InvalidRequestError{Reason: fmt.Sprintf("%s is not a valid range: %s.", q, problem)}
	}
	addrPart, bitsPart, _ := strings.Cut(q, "/")
	addr, err := netip.ParseAddr(addrPart)
	if err != nil {
		return nil, invalid(addressProblem(addrPart))
	}
	if addr.Zone() != "" {
		return nil, invalid("a range cannot carry a zone")
	}
	maxBits := addr.BitLen()
	family := "an IPv4"
	if addr.Is6() {
		family = "an IPv6"
	}
	bits, err := strconv.Atoi(bitsPart)
	if err != nil || bitsPart == "" || bits < 0 || bits > maxBits {
		return nil, invalid(fmt.Sprintf("%s prefix is 0 to %d", family, maxBits))
	}

	prefix := netip.PrefixFrom(addr, bits).Masked()
	keyBits := bits
	if addr.Is4() {
		keyBits += 96
	}
	first := prefix.Addr().As16()
	last := first
	for i := keyBits; i < 128; i++ {
		last[i/8] |= 1 << (7 - i%8)
	}

	// An IPv4-mapped range at least as narrow as the mapped space reads as
	// the IPv4 range it is.
	display := prefix
	if prefix.Addr().Is4In6() && bits >= 96 {
		display = netip.PrefixFrom(prefix.Addr().Unmap(), bits-96)
	}
	return &NSSKeyAddressSearch{Display: display.String(), Range: true, First: first[:], Last: last[:]}, nil
}

// looksLikeAddress reports whether q is shaped like an IP address or a CIDR
// range, whether or not it is a valid one: digits and dots with at least
// one dot or a "/" (IPv4, "10/8" included, which is invalid but plainly
// meant as a range), or hex digits, colons and dots with at least two
// colons (IPv6, an embedded IPv4 included), with an optional zone and an
// optional "/" and digits after it. A key name, a group or a username
// carries some other letter and so never reads as one.
func looksLikeAddress(q string) bool {
	body, bits, hasBits := strings.Cut(q, "/")
	if !onlyRunes(bits, isDigit) {
		return false
	}
	if strings.Contains(body, ":") {
		if zone := strings.IndexByte(body, '%'); zone >= 0 {
			body = body[:zone]
		}
		return strings.Count(body, ":") >= 2 &&
			onlyRunes(body, func(r rune) bool { return isHexDigit(r) || r == ':' || r == '.' })
	}
	if body == "" || (!strings.Contains(body, ".") && !hasBits) {
		return false
	}
	return onlyRunes(body, func(r rune) bool { return isDigit(r) || r == '.' })
}

// onlyRunes reports whether every rune of s passes ok; true for "".
func onlyRunes(s string, ok func(rune) bool) bool {
	return strings.IndexFunc(s, func(r rune) bool { return !ok(r) }) < 0
}

// isDigit reports whether r is 0-9.
func isDigit(r rune) bool { return r >= '0' && r <= '9' }

// isHexDigit reports whether r is 0-9, a-f or A-F.
func isHexDigit(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

// addressProblem says, in a reader's words, why s is not an IP address.
// s has already failed netip.ParseAddr.
func addressProblem(s string) string {
	if strings.Contains(s, ":") {
		return "it is not an IPv6 address"
	}
	parts := strings.Split(s, ".")
	if len(parts) != 4 {
		return "an IPv4 address has four parts, like 10.0.4.7"
	}
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n > 255 {
			return "each part of an IPv4 address is 0 to 255"
		}
		if len(part) > 1 && part[0] == '0' {
			return "a part of an IPv4 address has no leading zero"
		}
	}
	// not covered: four parts of 0 to 255 without a leading zero always
	// parse, so ParseAddr refused nothing this function can name.
	return "it is not an IPv4 address"
}
