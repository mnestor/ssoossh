import { isIPAddress } from '#lib/audit.js';

// How the admin key list reads its one search field, the browser's half of
// server/service/nss_keysearch.go: text searches the key's name, group and
// creator; an address or a CIDR range searches the usage instead. Both
// sides decide "looks like an address" the same way and word a bad one the
// same way, so a typo is caught here without asking the server, and the
// server's 400 for what slips past reads the same in the field.

/** KeySearch is how one search term reads. */
export type KeySearch =
	| { kind: 'empty' }
	| { kind: 'text' }
	| { kind: 'address'; range: boolean }
	| { kind: 'invalid'; message: string };

/** onlyChars reports whether every character of s is in allowed; true for ''. */
function onlyChars(s: string, allowed: RegExp): boolean {
	return [...s].every((c) => allowed.test(c));
}

/**
 * looksLikeAddress reports whether q is shaped like an IP address or a CIDR
 * range, valid or not: digits and dots with a dot or a "/" (IPv4, "10/8"
 * included), or hex digits, colons and dots with at least two colons
 * (IPv6), with an optional zone and an optional "/" and digits.
 */
export function looksLikeAddress(q: string): boolean {
	const slash = q.indexOf('/');
	let body = slash >= 0 ? q.slice(0, slash) : q;
	const bits = slash >= 0 ? q.slice(slash + 1) : '';
	if (!onlyChars(bits, /[0-9]/)) return false;
	if (body.includes(':')) {
		const zone = body.indexOf('%');
		if (zone >= 0) body = body.slice(0, zone);
		return body.split(':').length - 1 >= 2 && onlyChars(body, /[0-9a-fA-F:.]/);
	}
	if (body === '' || (!body.includes('.') && slash < 0)) return false;
	return onlyChars(body, /[0-9.]/);
}

/** addressProblem says why s, which is not an IP address, is not one. */
function addressProblem(s: string): string {
	if (s.includes(':')) return 'it is not an IPv6 address';
	const parts = s.split('.');
	if (parts.length !== 4) return 'an IPv4 address has four parts, like 10.0.4.7';
	for (const part of parts) {
		if (!/^[0-9]+$/.test(part) || Number(part) > 255) {
			return 'each part of an IPv4 address is 0 to 255';
		}
		if (part.length > 1 && part.startsWith('0')) {
			return 'a part of an IPv4 address has no leading zero';
		}
	}
	return 'it is not an IPv4 address';
}

/** validAddress reports whether s is one IP address, a zone allowed on IPv6. */
function validAddress(s: string): boolean {
	const zone = s.indexOf('%');
	if (zone >= 0) {
		return s.includes(':') && zone < s.length - 1 && isIPAddress(s.slice(0, zone));
	}
	return isIPAddress(s);
}

/**
 * readKeySearch reads a trimmed search term: nothing, text, an address or a
 * range, or an address-shaped term that is not one, with the message the
 * field shows ("10.0.4.0/33 is not a valid range: an IPv4 prefix is 0 to
 * 32.").
 */
export function readKeySearch(raw: string): KeySearch {
	const q = raw.trim();
	if (q === '') return { kind: 'empty' };
	if (!looksLikeAddress(q)) return { kind: 'text' };

	const slash = q.indexOf('/');
	if (slash < 0) {
		return validAddress(q)
			? { kind: 'address', range: false }
			: { kind: 'invalid', message: `${q} is not a valid address: ${addressProblem(q)}.` };
	}
	const address = q.slice(0, slash);
	const bits = q.slice(slash + 1);
	const invalid = (problem: string): KeySearch => ({
		kind: 'invalid',
		message: `${q} is not a valid range: ${problem}.`
	});
	if (address.includes('%')) {
		return validAddress(address)
			? invalid('a range cannot carry a zone')
			: invalid(addressProblem(address));
	}
	if (!isIPAddress(address)) return invalid(addressProblem(address));
	const v6 = address.includes(':');
	const max = v6 ? 128 : 32;
	if (bits === '' || Number(bits) > max) {
		return invalid(`${v6 ? 'an IPv6' : 'an IPv4'} prefix is 0 to ${max}`);
	}
	return { kind: 'address', range: true };
}

/**
 * usageWindowPhrase names how far back an address search reaches, from
 * nss.usage_retention in seconds: "in the last 90 days", or, with every row
 * kept, "in all the usage the server keeps".
 */
export function usageWindowPhrase(retentionSeconds: number): string {
	if (retentionSeconds <= 0) return 'in all the usage the server keeps';
	const days = Math.max(1, Math.round(retentionSeconds / 86400));
	return days === 1 ? 'in the last day' : `in the last ${days} days`;
}
