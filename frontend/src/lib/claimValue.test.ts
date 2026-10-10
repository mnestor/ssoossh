import { describe, expect, it } from 'vitest';

import { describeClaimValue, lengthOf } from './claimValue.js';

// Test methodology: one table of claim values a real provider sends, each
// with the shape it must be shown in. The shapes are what keep a group
// named "eng, ops" one group and an empty string apart from null.

describe('describeClaimValue', () => {
	const cases: [string, unknown, ReturnType<typeof describeClaimValue>][] = [
		['a string', 'alice', { kind: 'text', text: 'alice' }],
		['a number', 1735689600, { kind: 'text', text: '1735689600' }],
		['a boolean', false, { kind: 'text', text: 'false' }],
		['an empty string', '', { kind: 'empty-string' }],
		['null', null, { kind: 'null' }],
		['undefined', undefined, { kind: 'null' }],
		['an array', ['eng, ops', 'sre'], { kind: 'list', items: ['eng, ops', 'sre'] }],
		['an empty array', [], { kind: 'list', items: [] }],
		['an array holding objects', [{ a: 1 }, null], { kind: 'list', items: ['{"a":1}', 'null'] }],
		['an object', { a: 1 }, { kind: 'json', lines: ['{', '  "a": 1', '}'] }]
	];

	for (const [name, value, want] of cases) {
		it(`should show ${name} in its own shape`, () => {
			expect(describeClaimValue(value)).toEqual(want);
		});
	}
});

describe('lengthOf', () => {
	it('should count the items of a list', () => {
		expect(lengthOf({ kind: 'list', items: ['a', 'b', 'c'] })).toBe(3);
	});

	it('should count the lines of an object', () => {
		expect(lengthOf({ kind: 'json', lines: ['{', '}'] })).toBe(2);
	});

	it('should count anything else as one row', () => {
		expect(lengthOf({ kind: 'text', text: 'alice' })).toBe(1);
	});
});
