import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import Page from './+page.svelte';

// Test methodology: render the admin service-codes list against a stubbed
// fetch. What matters: the list renders what the API returns, an empty
// answer and a failure read differently, and a row is a link to the code's
// own page — the detail itself is /admin/service-codes/[id]'s to test.

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

/** json wraps a body in the envelope every endpoint answers with. */
function json(data: unknown): Response {
	return new Response(JSON.stringify({ data, error: null }), {
		status: 200,
		headers: { 'Content-Type': 'application/json' }
	});
}

/** stubList answers the list fetch. */
function stubList(enrollments: object[], meta = pageMeta(enrollments.length)) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() => Promise.resolve(json({ enrollments, meta })))
	);
}

beforeEach(() => {
	vi.unstubAllGlobals();
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

	// A code is its own page now. The list's job is to address it: the
	// detail, and everything a reader can do to a code from it, belongs to
	// /admin/service-codes/[id].
	it("should link each row to the code's own page", async () => {
		stubList([adminEnrollment('enr-1', 'svc-deploy')]);

		render(Page);

		expect((await screen.findAllByTestId('enrollment-row'))[0]).toHaveAttribute(
			'href',
			'/admin/service-codes/enr-1'
		);
	});

	it('should page rather than pile every code onto one screen', async () => {
		const rows = Array.from({ length: 25 }, (_, i) => adminEnrollment('enr-' + i, 'svc-' + i));
		stubList(rows, pageMeta(60));

		render(Page);

		expect(await screen.findByTestId('enrollments-pager')).toBeInTheDocument();
	});
});
