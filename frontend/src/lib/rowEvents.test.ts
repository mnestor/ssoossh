import { describe, expect, it } from 'vitest';

import { certificateEvent } from './rowEvents';

describe('certificateEvent', () => {
	it.each([
		{ type: 'user', want: 'certificate issued' },
		{ type: 'pam', want: 'PAM login' },
		{ type: 'console', want: 'console login' },
		{ type: 'service', want: 'service certificate issued' },
		{ type: undefined, want: 'certificate issued' },
		{ type: 'something-new', want: 'certificate issued' }
	])('should describe a $type certificate as "$want"', ({ type, want }) => {
		expect(certificateEvent(type)).toBe(want);
	});
});
