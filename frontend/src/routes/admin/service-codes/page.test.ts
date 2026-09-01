import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { fakePage, resetFakePage } from '$lib/testing/page.svelte';
import Page from './+page.svelte';

// Test methodology: render the admin service-codes list against a stubbed
// fetch, with $app/state and $app/navigation replaced by the shared
// reactive fake. What matters: the list renders what the API returns, an
// empty answer and a failure read differently, and opening a row goes
// through shallow routing (state changes, URL does not).

vi.mock('$app/state', async () => {
	const { fakePage } = await import('$lib/testing/page.svelte');
	return { page: fakePage };
});
vi.mock('$app/navigation', async () => {
	const { fakePushState } = await import('$lib/testing/page.svelte');
	return { pushState: fakePushState };
});

/** adminEnrollment builds one row of the admin list. */
function adminEnrollment(id: string, account: string) {
	return {
		id,
		service_account: account,
		approved_by_username: 'alice',
		approved_by_email: 'alice@example.com',
		certificate_request_id: 'req-' + id,
		principals: [account],
		key_id: account + '/req-' + id,
		public_key_fingerprint: 'SHA256:abc',
		options: { extensions: [], no_touch_required: false },
		certificate_valid_seconds: 3600,
		created_at: '2026-08-20T12:00:00Z',
		expires_at: '2026-11-20T12:00:00Z',
		retrieval_count: 3
	};
}

/** pageMeta builds the paging envelope for one page of rows. */
function pageMeta(total: number, offset = 0, limit = 25) {
	return {
		total,
		limit,
		offset,
		page: Math.floor(offset / limit) + 1,
		page_count: Math.max(1, Math.ceil(total / limit))
	};
}

/** stubList answers every fetch with one admin enrollments payload. */
function stubList(enrollments: object[], meta = pageMeta(enrollments.length)) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: { enrollments, meta }, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

beforeEach(() => {
	vi.unstubAllGlobals();
	resetFakePage('http://localhost/admin/service-codes');
});

describe('admin service codes page', () => {
	it('should list every enrollment the API returns', async () => {
		stubList([adminEnrollment('enr-1', 'svc-deploy'), adminEnrollment('enr-2', 'svc-backup')]);

		render(Page);

		expect(await screen.findAllByTestId('enrollment-row')).toHaveLength(2);
		expect(screen.getByText('svc-deploy')).toBeInTheDocument();
		expect(screen.getByText('svc-backup')).toBeInTheDocument();
	});

	it('should say so when no codes exist', async () => {
		stubList([]);

		render(Page);

		expect(await screen.findByTestId('enrollments-empty')).toBeInTheDocument();
	});

	it('should surface a load failure instead of an empty list', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() =>
				Promise.resolve(
					new Response(JSON.stringify({ data: null, error: 'not authorized as auditor' }), {
						status: 403,
						headers: { 'Content-Type': 'application/json' }
					})
				)
			)
		);

		render(Page);

		expect(await screen.findByText('Could not load service codes')).toBeInTheDocument();
		expect(screen.queryByTestId('enrollments-empty')).not.toBeInTheDocument();
	});

	it('should open a row through shallow routing', async () => {
		stubList([adminEnrollment('enr-1', 'svc-deploy')]);

		render(Page);
		const user = userEvent.setup();

		await user.click((await screen.findAllByTestId('enrollment-row'))[0]);

		expect(fakePage.state.modalEnrollmentId).toBe('enr-1');
	});

	it('should page rather than pile every code onto one screen', async () => {
		const rows = Array.from({ length: 25 }, (_, i) => adminEnrollment('enr-' + i, 'svc-' + i));
		stubList(rows, pageMeta(60));

		render(Page);

		expect(await screen.findByTestId('enrollments-pager')).toBeInTheDocument();
	});
});
