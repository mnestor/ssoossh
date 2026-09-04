import { describe, expect, it } from 'vitest';

import { ApiError } from './api/client';
import {
	CODE_LENGTH,
	describeCodeError,
	formatCode,
	isComplete,
	normalizeCode
} from './consolecode';

// The browser half of the console login code. What matters here is that
// typing is forgiving — the person doing it is reading a serial console —
// and that the three server-side failures stay distinguishable, because
// each one sends them somewhere different.

describe('normalizeCode', () => {
	const cases: Array<{ name: string; input: string; want: string }> = [
		{ name: 'should keep a code that is already normalized', input: 'K7M4QP2X', want: 'K7M4QP2X' },
		{ name: 'should drop the display hyphen', input: 'K7M4-QP2X', want: 'K7M4QP2X' },
		{ name: 'should upper-case what was typed in lower case', input: 'k7m4qp2x', want: 'K7M4QP2X' },
		{ name: 'should drop spaces from a paste', input: ' K7M4 QP2X ', want: 'K7M4QP2X' },
		{ name: 'should read an upper I as the digit one', input: 'K7M4QP2I', want: 'K7M4QP21' },
		{ name: 'should read a lower l as the digit one', input: 'k7m4qp2l', want: 'K7M4QP21' },
		{ name: 'should read an O as the digit zero', input: 'K7M4QP2O', want: 'K7M4QP20' },
		{ name: 'should drop a character outside the alphabet', input: 'K7M4QP2U', want: 'K7M4QP2' },
		{ name: 'should drop punctuation', input: 'K7M4!QP2X', want: 'K7M4QP2X' },
		{ name: 'should stop at the code length', input: 'K7M4QP2XZZZZ', want: 'K7M4QP2X' },
		{ name: 'should return nothing for input with no code characters', input: '---', want: '' },
		{ name: 'should return nothing for empty input', input: '', want: '' }
	];

	for (const { name, input, want } of cases) {
		it(name, () => {
			expect(normalizeCode(input)).toBe(want);
		});
	}
});

describe('formatCode', () => {
	const cases: Array<{ name: string; input: string; want: string }> = [
		{ name: 'should group a full code in two', input: 'K7M4QP2X', want: 'K7M4-QP2X' },
		{ name: 'should leave a partial first group alone', input: 'K7M', want: 'K7M' },
		{
			name: 'should not add a trailing separator at a group boundary',
			input: 'K7M4',
			want: 'K7M4'
		},
		{
			name: 'should start the second group at the fifth character',
			input: 'K7M4Q',
			want: 'K7M4-Q'
		},
		{ name: 'should render empty input as empty', input: '', want: '' }
	];

	for (const { name, input, want } of cases) {
		it(name, () => {
			expect(formatCode(input)).toBe(want);
		});
	}

	it('should round trip a normalized code through the display form', () => {
		expect(normalizeCode(formatCode('K7M4QP2X'))).toBe('K7M4QP2X');
	});
});

describe('isComplete', () => {
	it('should accept a code of the full length', () => {
		expect(isComplete('K7M4QP2X')).toBe(true);
	});

	it('should reject a code one character short', () => {
		expect(isComplete('K7M4QP2')).toBe(false);
	});

	it('should reject empty input', () => {
		expect(isComplete('')).toBe(false);
	});
});

describe('describeCodeError', () => {
	const cases: Array<{ name: string; status: number; kind: string }> = [
		{ name: 'should report an expired login as expired', status: 410, kind: 'expired' },
		{ name: 'should report a claimed request as claimed', status: 403, kind: 'claimed' },
		{ name: 'should report an unknown code as not-found', status: 404, kind: 'not-found' },
		{ name: 'should report a malformed code as invalid', status: 400, kind: 'invalid' },
		{ name: 'should report an unexpected status as unknown', status: 500, kind: 'unknown' }
	];

	for (const { name, status, kind } of cases) {
		it(name, () => {
			expect(describeCodeError(new ApiError(status, 'nope')).kind).toBe(kind);
		});
	}

	it('should report a plain error as unknown', () => {
		expect(describeCodeError(new Error('offline')).kind).toBe('unknown');
	});

	it('should carry a plain error message through so it is not lost', () => {
		expect(describeCodeError(new Error('offline')).message).toBe('offline');
	});

	// The three server-side kinds send the user to three different next
	// actions, so their prose has to differ as well as their identifiers.
	it('should give each server-side failure its own title', () => {
		const titles = [410, 403, 404].map(
			(status) => describeCodeError(new ApiError(status, '')).title
		);
		expect(new Set(titles).size).toBe(3);
	});

	it('should state the expected length when the code is malformed', () => {
		expect(describeCodeError(new ApiError(400, '')).message).toContain(String(CODE_LENGTH));
	});
});
