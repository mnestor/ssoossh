import { render, screen } from '@testing-library/svelte';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import type { AdminEnrollment } from '$lib/api/types';
import { fakePage, resetFakePage } from '$lib/testing/page.svelte';
import Page from './+page.svelte';

// Test methodology: render the admin detail route against a stubbed fetch,
// with $app/state replaced by the shared fake so the route parameter can be
// set. The page resolves its enrollment out of the list answer, so the
// found / not-found split — one id in the list, one not — is the behavior
// under test, along with the failure path.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('$lib/testing/page.svelte');
	return { page: fakePage };
});

/** adminEnrollment builds one admin enrollment row. */
function adminEnrollment(id: string): AdminEnrollment {
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
		expires_at: '2026-11-20T12:00:00Z',
		retrieval_count: 3
	} as AdminEnrollment;
}

/**
 * stubFetch answers the list URL with rows and any other URL (the modal's
 * own detail fetch) with a matching detail payload.
 */
function stubFetch(rows: AdminEnrollment[]) {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			const url = String(input);
			const body = url.includes('/admin/enrollments?')
				? {
						enrollments: rows,
						meta: { total: rows.length, limit: 1000, offset: 0, page: 1, page_count: 1 }
					}
				: { enrollment: rows[0], retrievals: [], retrieval_total: 0 };
			return Promise.resolve(
				new Response(JSON.stringify({ data: body, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
}

beforeEach(() => {
	vi.unstubAllGlobals();
	resetFakePage('http://localhost/admin/service-codes/enr-1');
	fakePage.params = { id: 'enr-1' };
});

describe('admin service code detail page', () => {
	it('should show the enrollment named by the route', async () => {
		stubFetch([adminEnrollment('enr-1')]);

		render(Page);

		expect(await screen.findByText('svc-deploy')).toBeInTheDocument();
	});

	it('should report an id the list does not contain', async () => {
		stubFetch([adminEnrollment('enr-other')]);

		render(Page);

		expect(await screen.findByText('Enrollment not found')).toBeInTheDocument();
	});

	it('should surface a load failure', async () => {
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

		expect(await screen.findByText('Could not load enrollment')).toBeInTheDocument();
	});
});
