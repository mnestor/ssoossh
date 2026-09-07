import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { AdminEnrollment } from '$lib/api/types';
import { fakePage, resetFakePage } from '$lib/testing/page.svelte';
import Page from './+page.svelte';

// Test methodology: render the admin detail route against a stubbed fetch,
// with $app/state replaced by the shared fake so the route parameter can be
// set. What matters is that the route asks the detail endpoint for the id it
// was given -- it used to page the list and scan it in the browser, which
// silently stopped resolving anything past the hundred newest codes -- and
// that the three answers a reader must tell apart (no such code, not allowed
// to see it, failed to load) read differently.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('$lib/testing/page.svelte');
	return { page: fakePage };
});

/** adminEnrollment builds one admin enrollment row. */
function adminEnrollment(id: string, overrides: Partial<AdminEnrollment> = {}): AdminEnrollment {
	return {
		id,
		service_account: 'svc-deploy',
		approved_by_username: 'alice',
		approved_by_email: 'alice@example.com',
		principals: ['svc-deploy'],
		key_id: 'svc-deploy/req-1',
		public_key_fingerprint: 'SHA256:abc',
		options: { extensions: [], no_touch_required: false },
		certificate_valid_seconds: 3600,
		created_at: '2026-08-20T12:00:00Z',
		expires_at: new Date(Date.now() + 90 * 24 * 60 * 60 * 1000).toISOString(),
		retrieval_count: 3,
		...overrides
	} as AdminEnrollment;
}

/** requests records every URL fetched, so the route's own call is testable. */
const requests: string[] = [];

/** stubDetail answers the detail endpoint with one enrollment, and the
 * holders panel with nobody. */
function stubDetail(enrollment: AdminEnrollment) {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			const url = String(input);
			requests.push(url);
			const body = url.includes('/holders')
				? { service_account: enrollment.service_account, holders: [] }
				: { enrollment, retrievals: [], retrieval_total: 0 };
			return Promise.resolve(
				new Response(JSON.stringify({ data: body, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
}

/** stubStatus answers every fetch with an error status. */
function stubStatus(status: number, error = 'nope') {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			requests.push(String(input));
			return Promise.resolve(
				new Response(JSON.stringify({ data: null, error }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
}

beforeEach(() => {
	vi.unstubAllGlobals();
	requests.length = 0;
	resetFakePage('http://localhost/admin/service-codes/enr-1');
	fakePage.params = { id: 'enr-1' };
});

describe('admin service code detail page', () => {
	it('should show the enrollment named by the route', async () => {
		stubDetail(adminEnrollment('enr-1'));

		render(Page);

		// By id rather than by account name: the holders panel names the
		// account too, so a bare text match is ambiguous.
		expect(await screen.findByTestId('enrollment-id')).toHaveAttribute('title', 'enr-1');
	});

	// The list is capped at paging.MaxLimit (100) whatever a caller asks
	// for, so resolving one enrollment by scanning a page of it stopped
	// working for anything older than the hundred newest codes.
	it('should ask the detail endpoint for the id rather than paging the list', async () => {
		stubDetail(adminEnrollment('enr-1'));

		render(Page);
		await screen.findByTestId('enrollment-id');

		expect(requests.some((url) => url.includes('/admin/enrollments/enr-1'))).toBe(true);
		expect(requests.every((url) => !url.includes('/admin/enrollments?'))).toBe(true);
	});

	// The endpoint is audited, so fetching it here and again inside the
	// panel would write two admin.enrollment_viewed events for one look.
	it('should read the enrollment once', async () => {
		stubDetail(adminEnrollment('enr-1'));

		render(Page);
		await screen.findByTestId('enrollment-id');

		const reads = requests.filter((url) => url.includes('/admin/enrollments/enr-1'));
		expect(reads).toHaveLength(1);
	});

	it('should report an id no enrollment carries', async () => {
		stubStatus(404, 'enrollment "enr-1" not found');

		render(Page);

		expect(await screen.findByText('No enrollment with that ID.')).toBeInTheDocument();
	});

	// "There is no such code" and "you may not see this one" are different
	// answers, and an operator chasing an id from a log line needs to know
	// which they got.
	it('should distinguish a refusal from a missing enrollment', async () => {
		stubStatus(403);

		render(Page);

		expect(
			await screen.findByText('You do not have permission to view this enrollment.')
		).toBeInTheDocument();
	});

	it('should surface a load failure', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new Error('network is down')))
		);

		render(Page);

		expect(await screen.findByText('Could not load enrollment')).toBeInTheDocument();
	});

	// Expiring a code is the page's action, in the heading's top right —
	// the same place an account is disabled from — rather than a control
	// buried in the admin-actions section near the foot of the page.
	it('should offer to expire a code that still works', async () => {
		stubDetail(adminEnrollment('enr-1'));

		render(Page);
		await screen.findByTestId('enrollment-id');

		expect(screen.getByTestId('admin-expire-code')).toBeInTheDocument();
	});

	// An expired code needs no control: the outcome it would produce is
	// already true.
	it('should not offer to expire a code that has already expired', async () => {
		stubDetail(adminEnrollment('enr-1', { expires_at: '2020-01-01T00:00:00Z' }));

		render(Page);
		await screen.findByTestId('enrollment-id');

		expect(screen.queryByTestId('admin-expire-code')).not.toBeInTheDocument();
	});

	// Always the list, whatever reached the page: an operator who arrived
	// with an id out of a log line was nowhere before this.
	it('should offer a way back to the list', async () => {
		stubDetail(adminEnrollment('enr-1'));

		render(Page);

		expect(screen.getByTestId('admin-service-code-back')).toHaveAttribute(
			'href',
			'/admin/service-codes'
		);
	});
});
