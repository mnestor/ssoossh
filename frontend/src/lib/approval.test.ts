import { describe, expect, it } from 'vitest';

import { ApiError } from './api/client';
import type { CertificateOptions, RequestDetail, RequestStatus } from './api/types';
import {
	anyTrimmed,
	approvalBlockedReason,
	criticalOptionDiff,
	describeLoadError,
	extensionDiff
} from './approval';

/** options builds a CertificateOptions with everything empty by default, so
 * each test states only the field it is about. */
function options(overrides: Partial<CertificateOptions> = {}): CertificateOptions {
	return { extensions: [], no_touch_required: false, ...overrides };
}

/** detail builds a plausible pending user request for the caller. */
function detail(overrides: Partial<RequestDetail> = {}): RequestDetail {
	return {
		id: 'f0e1d2c3-0000-4000-8000-000000000000',
		type: 'user',
		status: 'pending',
		source_ip: '198.51.100.7',
		public_key: 'ssh-ed25519 AAAA...',
		principals: ['alice'],
		valid_seconds: 28800,
		requested: options(),
		granted: options(),
		created_at: '2026-08-14T09:00:00Z',
		approval_url: '/approve/f0e1d2c3-0000-4000-8000-000000000000',
		is_owned_by_you: true,
		already_closed: false,
		...overrides
	};
}

describe('extensionDiff', () => {
	it('should mark an extension granted when it survives narrowing', () => {
		const entries = extensionDiff(
			options({ extensions: ['permit-pty'] }),
			options({ extensions: ['permit-pty'] })
		);
		expect(entries).toEqual([{ label: 'permit-pty', status: 'granted' }]);
	});

	it('should mark an extension trimmed when the server does not permit it', () => {
		const entries = extensionDiff(
			options({ extensions: ['permit-pty', 'permit-port-forwarding'] }),
			options({ extensions: ['permit-pty'] })
		);
		expect(entries).toContainEqual({ label: 'permit-port-forwarding', status: 'trimmed' });
	});

	it('should mark an extension added when policy grants one nobody asked for', () => {
		const entries = extensionDiff(options(), options({ extensions: ['permit-pty'] }));
		expect(entries).toEqual([{ label: 'permit-pty', status: 'added' }]);
	});

	it('should return no entries when neither side has extensions', () => {
		expect(extensionDiff(options(), options())).toEqual([]);
	});

	// The wire type marks these optional, and a Go nil slice can reach the
	// browser as a missing key rather than [].
	it('should tolerate extension lists being absent entirely', () => {
		const bare = { no_touch_required: false } as unknown as CertificateOptions;
		expect(extensionDiff(bare, bare)).toEqual([]);
	});
});

describe('criticalOptionDiff', () => {
	it('should mark force-command trimmed when the server drops it', () => {
		const entries = criticalOptionDiff(options({ force_command: '/bin/true' }), options());
		expect(entries).toEqual([{ label: 'force-command', value: '/bin/true', status: 'trimmed' }]);
	});

	it('should mark source-address trimmed when the server drops it', () => {
		const entries = criticalOptionDiff(
			options({ source_addresses: ['10.0.0.0/8', '192.0.2.1/32'] }),
			options()
		);
		expect(entries).toEqual([
			{ label: 'source-address', value: '10.0.0.0/8, 192.0.2.1/32', status: 'trimmed' }
		]);
	});

	it('should mark no-touch-required granted when both sides carry it', () => {
		const entries = criticalOptionDiff(
			options({ no_touch_required: true }),
			options({ no_touch_required: true })
		);
		expect(entries).toEqual([{ label: 'no-touch-required', status: 'granted' }]);
	});

	it('should prefer the granted value when both sides carry a force-command', () => {
		const entries = criticalOptionDiff(
			options({ force_command: '/bin/asked' }),
			options({ force_command: '/bin/granted' })
		);
		expect(entries[0].value).toBe('/bin/granted');
	});

	it('should return no entries when no critical options are involved', () => {
		expect(criticalOptionDiff(options(), options())).toEqual([]);
	});
});

describe('anyTrimmed', () => {
	it('should report true when at least one entry was trimmed', () => {
		expect(
			anyTrimmed([
				{ label: 'a', status: 'granted' },
				{ label: 'b', status: 'trimmed' }
			])
		).toBe(true);
	});

	it('should report false when nothing was trimmed', () => {
		expect(
			anyTrimmed([
				{ label: 'a', status: 'granted' },
				{ label: 'b', status: 'added' }
			])
		).toBe(false);
	});

	it('should report false for an empty diff', () => {
		expect(anyTrimmed([])).toBe(false);
	});
});

describe('approvalBlockedReason', () => {
	it('should allow a decision on a pending request the caller owns', () => {
		expect(approvalBlockedReason(detail())).toBeNull();
	});

	it('should block a request that belongs to another account', () => {
		expect(approvalBlockedReason(detail({ is_owned_by_you: false }))).toBe('not-yours');
	});

	it('should report in-progress for a request already being signed', () => {
		expect(approvalBlockedReason(detail({ status: 'signing' }))).toBe('in-progress');
	});

	// Ownership is checked first on purpose: "not yours" is the more useful
	// thing to say, even about a request that is also closed.
	it('should prefer the ownership reason over the resolved one', () => {
		expect(approvalBlockedReason(detail({ is_owned_by_you: false, status: 'denied' }))).toBe(
			'not-yours'
		);
	});

	const resolved: RequestStatus[] = ['approved', 'enrolled', 'denied', 'expired', 'failed'];
	for (const status of resolved) {
		it(`should block a decision when the request is already ${status}`, () => {
			expect(approvalBlockedReason(detail({ status, already_closed: true }))).toBe(
				'already-resolved'
			);
		});
	}

	// already_closed and status are computed from the same row server-side,
	// but the flag is what the API promises, so it has to be honored on its
	// own.
	it('should block a decision when already_closed is set despite a pending status', () => {
		expect(approvalBlockedReason(detail({ already_closed: true }))).toBe('already-resolved');
	});
});

describe('describeLoadError', () => {
	it('should explain that a forbidden request belongs to someone else', () => {
		expect(describeLoadError(new ApiError(403, 'forbidden')).title).toBe(
			'This request belongs to someone else'
		);
	});

	it('should report a missing request as no such request', () => {
		expect(describeLoadError(new ApiError(404, 'not found')).title).toBe('No such request');
	});

	it('should surface the underlying message for an unclassified failure', () => {
		expect(describeLoadError(new ApiError(0, 'network request failed')).message).toBe(
			'network request failed'
		);
	});

	it('should describe a non-Error throw without inventing a message', () => {
		expect(describeLoadError('boom').message).toBe('something went wrong');
	});
});
