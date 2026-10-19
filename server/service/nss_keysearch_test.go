package service

import (
	"bytes"
	"errors"
	"testing"

	"github.com/mnestor/ssoossh/server/utils/errorresponses"
)

func TestParseNSSKeyAddressSearch_ShouldReadTheAddressOrRangeWhenQLooksLikeOne(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		q           string
		wantDisplay string
		wantRange   bool
		wantFirst   string
		wantLast    string
	}{
		{name: "should match one address when q is an IPv4 address", q: "10.0.4.7", wantDisplay: "10.0.4.7", wantFirst: "::ffff:10.0.4.7", wantLast: "::ffff:10.0.4.7"},
		{name: "should read it as IPv4 when q is an IPv4-mapped address", q: "::ffff:10.0.4.7", wantDisplay: "10.0.4.7", wantFirst: "::ffff:10.0.4.7", wantLast: "::ffff:10.0.4.7"},
		{name: "should match one address when q is an IPv6 address", q: "2001:db8::1", wantDisplay: "2001:db8::1", wantFirst: "2001:db8::1", wantLast: "2001:db8::1"},
		{name: "should normalise the case when q is an upper-case IPv6 address", q: "2001:DB8::A", wantDisplay: "2001:db8::a", wantFirst: "2001:db8::a", wantLast: "2001:db8::a"},
		{name: "should drop the zone when q is an IPv6 address with one", q: "fe80::1%eth0", wantDisplay: "fe80::1", wantFirst: "fe80::1", wantLast: "fe80::1"},
		{name: "should trim blanks when q has them", q: "  10.0.4.7 ", wantDisplay: "10.0.4.7", wantFirst: "::ffff:10.0.4.7", wantLast: "::ffff:10.0.4.7"},
		{name: "should span the range when q is an IPv4 /24", q: "10.0.4.0/24", wantDisplay: "10.0.4.0/24", wantRange: true, wantFirst: "::ffff:10.0.4.0", wantLast: "::ffff:10.0.4.255"},
		{name: "should clear the host bits when q is a range written from a host", q: "10.0.4.7/24", wantDisplay: "10.0.4.0/24", wantRange: true, wantFirst: "::ffff:10.0.4.0", wantLast: "::ffff:10.0.4.255"},
		{name: "should match one address when q is an IPv4 /32", q: "10.0.4.7/32", wantDisplay: "10.0.4.7/32", wantRange: true, wantFirst: "::ffff:10.0.4.7", wantLast: "::ffff:10.0.4.7"},
		{name: "should span every IPv4 address and nothing else when q is 0.0.0.0/0", q: "0.0.0.0/0", wantDisplay: "0.0.0.0/0", wantRange: true, wantFirst: "::ffff:0.0.0.0", wantLast: "::ffff:255.255.255.255"},
		{name: "should span every address when q is ::/0", q: "::/0", wantDisplay: "::/0", wantRange: true, wantFirst: "::", wantLast: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"},
		{name: "should span the range when q is an IPv6 /32", q: "2001:db8::/32", wantDisplay: "2001:db8::/32", wantRange: true, wantFirst: "2001:db8::", wantLast: "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff"},
		{name: "should match one address when q is an IPv6 /128", q: "2001:db8::1/128", wantDisplay: "2001:db8::1/128", wantRange: true, wantFirst: "2001:db8::1", wantLast: "2001:db8::1"},
		{name: "should read it as the IPv4 range when q is an IPv4-mapped /120", q: "::ffff:10.0.4.0/120", wantDisplay: "10.0.4.0/24", wantRange: true, wantFirst: "::ffff:10.0.4.0", wantLast: "::ffff:10.0.4.255"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseNSSKeyAddressSearch(tt.q)
			if err != nil || got == nil {
				t.Fatalf("ParseNSSKeyAddressSearch(%q) = %+v, %v; want an address search", tt.q, got, err)
			}
			if got.Display != tt.wantDisplay || got.Range != tt.wantRange {
				t.Errorf("display, range = %q, %v; want %q, %v", got.Display, got.Range, tt.wantDisplay, tt.wantRange)
			}
			if !bytes.Equal(got.First, key16(t, tt.wantFirst)) || !bytes.Equal(got.Last, key16(t, tt.wantLast)) {
				t.Errorf("bounds = %x..%x, want %s..%s", got.First, got.Last, tt.wantFirst, tt.wantLast)
			}
		})
	}
}

func TestParseNSSKeyAddressSearch_ShouldLeaveItToTheTextSearchWhenQIsNotAnAddress(t *testing.T) {
	t.Parallel()

	// A key id or a prefix of one ("nss_" and base32, in either case) always
	// carries letters and an underscore, so it is text, never an address.
	for _, q := range []string{"", "web fleet", "kim", "kim@example.org", "nss_abcd2345", "NSS_ABCD2345", "nss_", "nss_5", "1234", "db:1", "cafe", "web.fleet", "ou=hosts/web", "/24", "10.0.4.7 web"} {
		t.Run("should not read "+q+" as an address when it is searched", func(t *testing.T) {
			t.Parallel()

			if got, err := ParseNSSKeyAddressSearch(q); got != nil || err != nil {
				t.Errorf("ParseNSSKeyAddressSearch(%q) = %+v, %v; want a text search", q, got, err)
			}
		})
	}
}

func TestParseNSSKeyAddressSearch_ShouldNameTheProblemWhenQIsAnInvalidAddressOrRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		q    string
		want string
	}{
		{name: "should name the prefix limit when an IPv4 prefix is past 32", q: "10.0.4.0/33", want: "10.0.4.0/33 is not a valid range: an IPv4 prefix is 0 to 32."},
		{name: "should name the prefix limit when an IPv6 prefix is past 128", q: "2001:db8::/129", want: "2001:db8::/129 is not a valid range: an IPv6 prefix is 0 to 128."},
		{name: "should ask for the prefix when the slash has nothing after it", q: "10.0.4.0/", want: "10.0.4.0/ is not a valid range: an IPv4 prefix is 0 to 32."},
		{name: "should ask for four parts when an IPv4 address is short", q: "10.0.4", want: "10.0.4 is not a valid address: an IPv4 address has four parts, like 10.0.4.7."},
		{name: "should ask for four parts when a range is written in shorthand", q: "10/8", want: "10/8 is not a valid range: an IPv4 address has four parts, like 10.0.4.7."},
		{name: "should bound the parts when one is past 255", q: "10.0.4.300", want: "10.0.4.300 is not a valid address: each part of an IPv4 address is 0 to 255."},
		{name: "should bound the parts when one is empty", q: "10..4.7", want: "10..4.7 is not a valid address: each part of an IPv4 address is 0 to 255."},
		{name: "should refuse a leading zero when a part has one", q: "010.0.4.7", want: "010.0.4.7 is not a valid address: a part of an IPv4 address has no leading zero."},
		{name: "should say it is not IPv6 when colons do not make an address", q: "1:2:3", want: "1:2:3 is not a valid address: it is not an IPv6 address."},
		{name: "should say it is not IPv6 when a range's address is malformed", q: "2001:db8:::/48", want: "2001:db8:::/48 is not a valid range: it is not an IPv6 address."},
		{name: "should refuse a zone when a range carries one", q: "fe80::1%eth0/64", want: "fe80::1%eth0/64 is not a valid range: a range cannot carry a zone."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseNSSKeyAddressSearch(tt.q)
			var invalid *errorresponses.InvalidRequestError
			if !errors.As(err, &invalid) {
				t.Fatalf("ParseNSSKeyAddressSearch(%q) = %+v, %v; want a 400", tt.q, got, err)
			}
			if invalid.Reason != tt.want {
				t.Errorf("reason = %q, want %q", invalid.Reason, tt.want)
			}
		})
	}
}
