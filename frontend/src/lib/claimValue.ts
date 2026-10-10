/**
 * How the claims echo shows one claim's value.
 *
 * A token's claims are whatever the identity provider sends: their count,
 * their lengths and their types all vary by provider and by user. A single
 * string rendering hid that: an array joined with commas made a group named
 * "eng, ops" read as two groups, an object came out as one line of JSON,
 * and an empty string was a blank cell nobody could tell from null.
 */
export type ClaimValue =
	| { kind: 'text'; text: string }
	| { kind: 'empty-string' }
	| { kind: 'null' }
	| { kind: 'list'; items: string[] }
	| { kind: 'json'; lines: string[] };

/** How many items or lines show before "Show all" is offered. */
export const COLLAPSE_AFTER = 10;

/** item renders one array element: text as it is, anything else as JSON. */
function item(value: unknown): string {
	if (typeof value === 'string') return value;
	if (value === null || value === undefined) return 'null';
	if (typeof value === 'object') return JSON.stringify(value);
	return String(value);
}

/** describeClaimValue sorts a claim value into the shape it is shown in. */
export function describeClaimValue(value: unknown): ClaimValue {
	if (value === null || value === undefined) return { kind: 'null' };
	if (value === '') return { kind: 'empty-string' };
	if (Array.isArray(value)) return { kind: 'list', items: value.map(item) };
	if (typeof value === 'object') {
		return { kind: 'json', lines: JSON.stringify(value, null, 2).split('\n') };
	}
	return { kind: 'text', text: String(value) };
}

/** lengthOf is how many rows a value takes, which decides whether it
 * collapses: list items or JSON lines, and one for anything else. */
export function lengthOf(value: ClaimValue): number {
	if (value.kind === 'list') return value.items.length;
	if (value.kind === 'json') return value.lines.length;
	return 1;
}
