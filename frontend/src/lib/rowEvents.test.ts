import { describe, expect, it } from 'vitest';

import { certificateEvent } from './rowEvents';

describe('certificateEvent', () => {
	it.each([
		{ type: 'user', want: 'user certificate issued' },
		{ type: 'pam', want: 'PAM login' },
		{ type: 'console', want: 'console login' },
		{ type: 'service', want: 'service certificate issued' },
		{ type: undefined, want: 'certificate issued' },
		{ type: 'something-new', want: 'certificate issued' }
	])('should describe a $type certificate as "$want"', ({ type, want }) => {
		expect(certificateEvent(type)).toBe(want);
	});

	// The PAM service is reported by an unauthenticated caller, so a row
	// names only a service pam_ssoossh is documented for, marked as
	// reported, and never echoes free text.
	it.each([
		{ service: 'sudo', want: 'sudo (reported)' },
		{ service: 'sudo-i', want: 'sudo (reported)' },
		{ service: 'su', want: 'su (reported)' },
		{ service: 'su-l', want: 'su (reported)' },
		{ service: 'sudoers', want: 'PAM login' },
		{ service: 'sum', want: 'PAM login' },
		{ service: 'sshd', want: 'SSH login (reported)' },
		{ service: 'login', want: 'login (reported)' },
		{ service: '', want: 'PAM login' },
		{ service: undefined, want: 'PAM login' },
		{ service: 'approved by admin', want: 'PAM login' },
		{ service: 'toString', want: 'PAM login' }
	])('should describe a PAM certificate reporting $service as "$want"', ({ service, want }) => {
		expect(certificateEvent('pam', service)).toBe(want);
	});

	it('should ignore a reported PAM service when the certificate is not PAM', () => {
		expect(certificateEvent('user', 'sudo')).toBe('user certificate issued');
	});
});
