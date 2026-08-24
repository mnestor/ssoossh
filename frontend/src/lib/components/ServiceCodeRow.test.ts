import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import type { ServiceEnrollment } from '$lib/api/types';
import ServiceCodeRow from './ServiceCodeRow.svelte';

const now = new Date('2026-08-22T12:00:00Z');

/** enrollment builds a service enrollment, overriding only what a case cares about. */
function enrollment(overrides: Partial<ServiceEnrollment> = {}): ServiceEnrollment {
	return {
		id: 'enr-1',
		certificate_request_id: 'req-1',
		principals: ['svc-deploy'],
		key_id: 'svc-deploy/req-1',
		public_key_fingerprint: 'SHA256:abc',
		options: { extensions: [], no_touch_required: false },
		certificate_valid_seconds: 3600,
		created_at: '2026-08-20T12:00:00Z',
		expires_at: '2026-11-20T12:00:00Z',
		first_redeemed_at: '2026-08-22T10:00:00Z',
		last_retrieved_at: '2026-08-22T10:00:00Z',
		retrieval_count: 12,
		...overrides
	};
}

describe('ServiceCodeRow', () => {
	it('should name the row by the account the code mints for', () => {
		render(ServiceCodeRow, { enrollment: enrollment(), now, onclick: vi.fn() });
		expect(screen.getByText('svc-deploy')).toBeInTheDocument();
	});

	it('should fall back to a placeholder when the principals could not be decoded', () => {
		render(ServiceCodeRow, { enrollment: enrollment({ principals: [] }), now, onclick: vi.fn() });
		expect(screen.getByText('unknown account')).toBeInTheDocument();
	});

	it('should report how long ago the code was approved', () => {
		render(ServiceCodeRow, { enrollment: enrollment(), now, onclick: vi.fn() });
		expect(screen.getByText(/2d ago/)).toBeInTheDocument();
	});

	it('should state the lifetime of the certificates it hands out', () => {
		render(ServiceCodeRow, { enrollment: enrollment(), now, onclick: vi.fn() });
		expect(screen.getByText(/certificates valid for 1h/)).toBeInTheDocument();
	});

	// A row predating the split between the code's lifetime and the
	// certificate's carries no duration, and the code's expiry bounded both.
	it('should say certificates last until the code expires when no lifetime is reported', () => {
		const row = enrollment({ certificate_valid_seconds: undefined });
		render(ServiceCodeRow, { enrollment: row, now, onclick: vi.fn() });
		expect(screen.getByText(/certificates last until the code expires/)).toBeInTheDocument();
	});

	it('should summarize how often the code has been redeemed', () => {
		render(ServiceCodeRow, { enrollment: enrollment(), now, onclick: vi.fn() });
		expect(screen.getByText(/redeemed 12 times, last 2h ago/)).toBeInTheDocument();
	});

	it('should say a single redemption once rather than as a count', () => {
		const row = enrollment({ retrieval_count: 1 });
		render(ServiceCodeRow, { enrollment: row, now, onclick: vi.fn() });
		expect(screen.getByText(/redeemed once/)).toBeInTheDocument();
	});

	it('should say so when nothing has ever redeemed the code', () => {
		const row = enrollment({ retrieval_count: 0, last_retrieved_at: undefined });
		render(ServiceCodeRow, { enrollment: row, now, onclick: vi.fn() });
		expect(screen.getByText('never redeemed')).toBeInTheDocument();
	});

	it('should mark a still-redeemable code as active', () => {
		render(ServiceCodeRow, { enrollment: enrollment(), now, onclick: vi.fn() });
		expect(screen.getByText('Active')).toBeInTheDocument();
	});

	it('should mark a code past its expiry as expired', () => {
		const row = enrollment({ expires_at: '2026-08-21T12:00:00Z' });
		render(ServiceCodeRow, { enrollment: row, now, onclick: vi.fn() });
		expect(screen.getByText('Expired')).toBeInTheDocument();
	});

	it('should call onclick when the row is activated', async () => {
		const onclick = vi.fn();
		render(ServiceCodeRow, { enrollment: enrollment(), now, onclick });
		await userEvent.click(screen.getByRole('button'));
		expect(onclick).toHaveBeenCalledOnce();
	});
});
