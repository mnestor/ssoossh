import { describe, expect, it } from 'vitest';

import { readKeySearch, usageWindowPhrase } from './nssKeySearch';

// Test methodology: the same table of terms server/service/nss_keysearch_test.go
// pins, so the field catches what the server would refuse, in the same words.

describe('readKeySearch', () => {
	const cases: { name: string; q: string; want: ReturnType<typeof readKeySearch> }[] = [
		{ name: 'nothing is typed', q: '  ', want: { kind: 'empty' } },
		{ name: 'a key name is typed', q: 'web fleet', want: { kind: 'text' } },
		{ name: 'an email is typed', q: 'kim@example.org', want: { kind: 'text' } },
		{ name: 'a key id is typed', q: 'nss_abcd2345', want: { kind: 'text' } },
		{ name: 'a key id is pasted in capitals', q: 'NSS_ABCD2345', want: { kind: 'text' } },
		{ name: 'a key id prefix is typed', q: 'nss_5', want: { kind: 'text' } },
		{ name: 'a bare number is typed', q: '1234', want: { kind: 'text' } },
		{ name: 'a word with one colon is typed', q: 'db:1', want: { kind: 'text' } },
		{ name: 'a dotted name is typed', q: 'web.fleet', want: { kind: 'text' } },
		{ name: 'a DN with a slash is typed', q: 'ou=hosts/web', want: { kind: 'text' } },
		{ name: 'an IPv4 address is typed', q: '10.0.4.7', want: { kind: 'address', range: false } },
		{ name: 'an IPv6 address is typed', q: '2001:db8::1', want: { kind: 'address', range: false } },
		{
			name: 'an IPv4-mapped address is typed',
			q: '::ffff:10.0.4.7',
			want: { kind: 'address', range: false }
		},
		{
			name: 'an address with a zone is typed',
			q: 'fe80::1%eth0',
			want: { kind: 'address', range: false }
		},
		{ name: 'an IPv4 range is typed', q: '10.0.4.0/24', want: { kind: 'address', range: true } },
		{ name: 'a /32 is typed', q: '10.0.4.7/32', want: { kind: 'address', range: true } },
		{ name: 'every IPv4 address is typed', q: '0.0.0.0/0', want: { kind: 'address', range: true } },
		{ name: 'an IPv6 range is typed', q: '2001:db8::/32', want: { kind: 'address', range: true } },
		{
			name: 'an IPv4 prefix is past 32',
			q: '10.0.4.0/33',
			want: {
				kind: 'invalid',
				message: '10.0.4.0/33 is not a valid range: an IPv4 prefix is 0 to 32.'
			}
		},
		{
			name: 'an IPv6 prefix is past 128',
			q: '2001:db8::/129',
			want: {
				kind: 'invalid',
				message: '2001:db8::/129 is not a valid range: an IPv6 prefix is 0 to 128.'
			}
		},
		{
			name: 'the slash has nothing after it',
			q: '10.0.4.0/',
			want: {
				kind: 'invalid',
				message: '10.0.4.0/ is not a valid range: an IPv4 prefix is 0 to 32.'
			}
		},
		{
			name: 'an IPv4 address is short',
			q: '10.0.4',
			want: {
				kind: 'invalid',
				message: '10.0.4 is not a valid address: an IPv4 address has four parts, like 10.0.4.7.'
			}
		},
		{
			name: 'a range is written in shorthand',
			q: '10/8',
			want: {
				kind: 'invalid',
				message: '10/8 is not a valid range: an IPv4 address has four parts, like 10.0.4.7.'
			}
		},
		{
			name: 'a part is past 255',
			q: '10.0.4.300',
			want: {
				kind: 'invalid',
				message: '10.0.4.300 is not a valid address: each part of an IPv4 address is 0 to 255.'
			}
		},
		{
			name: 'a part has a leading zero',
			q: '010.0.4.7',
			want: {
				kind: 'invalid',
				message: '010.0.4.7 is not a valid address: a part of an IPv4 address has no leading zero.'
			}
		},
		{
			name: 'colons do not make an address',
			q: '1:2:3',
			want: { kind: 'invalid', message: '1:2:3 is not a valid address: it is not an IPv6 address.' }
		},
		{
			name: 'a range carries a zone',
			q: 'fe80::1%eth0/64',
			want: {
				kind: 'invalid',
				message: 'fe80::1%eth0/64 is not a valid range: a range cannot carry a zone.'
			}
		}
	];

	for (const { name, q, want } of cases) {
		it(`should read ${q.trim() || 'nothing'} as ${want.kind} when ${name}`, () => {
			expect(readKeySearch(q)).toEqual(want);
		});
	}
});

describe('usageWindowPhrase', () => {
	const cases = [
		{ seconds: 7776000, want: 'in the last 90 days' },
		{ seconds: 86400, want: 'in the last day' },
		{ seconds: 3600, want: 'in the last day' },
		{ seconds: 0, want: 'in all the usage the server keeps' }
	];
	for (const { seconds, want } of cases) {
		it(`should say ${want} when the retention is ${seconds} seconds`, () => {
			expect(usageWindowPhrase(seconds)).toBe(want);
		});
	}
});
