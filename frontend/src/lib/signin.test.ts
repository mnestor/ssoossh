import { describe, expect, it } from 'vitest';

import { describeSignInFailure } from './signin';

describe('describeSignInFailure', () => {
	it.each([
		{ input: null, want: null },
		{ input: undefined, want: null },
		{ input: '', want: null }
	])('should return nothing when there is no reason ($input)', ({ input, want }) => {
		expect(describeSignInFailure(input)).toBe(want);
	});

	it.each([
		{ input: 'expired', reason: 'expired', support: false },
		{ input: 'refused', reason: 'refused', support: false },
		{ input: 'session', reason: 'session', support: true },
		{ input: 'failed', reason: 'failed', support: true },
		{ input: 'something-new', reason: 'failed', support: true }
	])('should read "$input" as $reason', ({ input, reason, support }) => {
		const failure = describeSignInFailure(input);
		expect({ reason: failure?.reason, support: failure?.needsSupport }).toEqual({
			reason,
			support
		});
	});
});
