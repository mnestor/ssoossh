import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { ServiceEnrollment } from '$lib/api/types';
import { resetFakePage } from '$lib/testing/page.svelte';
import Page from './+page.svelte';

// $app/state and $app/navigation are replaced with a reactive fake so the
// shallow-routing flow (click a row, panel opens) is exercised for real.
// See src/lib/testing/page.svelte.ts for why the fake refuses to update
// page.url.
vi.mock('$app/state', async () => {
	const { fakePage } = await import('$lib/testing/page.svelte');
	return { page: fakePage };
});
vi.mock('$app/navigation', async () => {
	const { fakePushState } = await import('$lib/testing/page.svelte');
	return { pushState: fakePushState };
});

/** Timestamps relative to now, so the page's clock-relative copy is stable
 * without freezing a timer. */
const anHourAgo = new Date(Date.now() - 60 * 60 * 1000).toISOString();
const twoDaysAgo = new Date(Date.now() - 2 * 24 * 60 * 60 * 1000).toISOString();
const inNinetyDays = new Date(Date.now() + 90 * 24 * 60 * 60 * 1000).toISOString();

/** deployCode is a fully populated live enrollment, overridable per test. */
function deployCode(overrides: Partial<ServiceEnrollment> = {}): ServiceEnrollment {
	return {
		id: 'enr-1',
		certificate_request_id: 'req-1',
		principals: ['svc-deploy'],
		key_id: 'svc-deploy/req-1',
		public_key_fingerprint: 'SHA256:abc123',
		options: {
			extensions: ['permit-pty'],
			force_command: '/usr/local/bin/deploy',
			source_addresses: ['198.51.100.0/24'],
			no_touch_required: false
		},
		certificate_valid_seconds: 3600,
		created_at: twoDaysAgo,
		expires_at: inNinetyDays,
		first_redeemed_at: anHourAgo,
		last_retrieved_at: anHourAgo,
		retrieval_count: 12,
		...overrides
	};
}

/** mockFetch answers the enrollment list with `enrollments`, and any other
 * call — the panel's retrieval log — with an empty log. */
function mockFetch(enrollments: ServiceEnrollment[]) {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			const body = String(input).includes('/retrievals') ? { retrievals: [] } : { enrollments };
			return Promise.resolve(
				new Response(JSON.stringify({ data: body, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
}

/** mockFetchError stubs the global fetch to reject. */
function mockFetchError(message = 'network error') {
	vi.stubGlobal(
		'fetch',
		vi.fn(() => Promise.reject(new Error(message)))
	);
}

beforeEach(() => {
	resetFakePage('http://localhost/service-codes');
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Service codes page', () => {
	describe('when enrollments load successfully', () => {
		it('should list a row per approved code', async () => {
			mockFetch([deployCode(), deployCode({ id: 'enr-2', principals: ['svc-backup'] })]);
			render(Page);
			await screen.findByText('svc-deploy');
			expect(screen.getAllByRole('button')).toHaveLength(2);
		});

		it('should name each row by the account it mints for', async () => {
			mockFetch([deployCode()]);
			render(Page);
			expect(await screen.findByText('svc-deploy')).toBeInTheDocument();
		});

		it('should mark a code that is still redeemable as active', async () => {
			mockFetch([deployCode()]);
			render(Page);
			expect(await screen.findByText('Active')).toBeInTheDocument();
		});

		// A job that stopped working is explained by the code beneath it, so
		// an expired code moves down the page rather than off it.
		it('should keep an expired code under its own heading', async () => {
			mockFetch([deployCode({ expires_at: anHourAgo })]);
			render(Page);
			expect(await screen.findByText('Expired codes')).toBeInTheDocument();
			expect(screen.getByText('Expired')).toBeInTheDocument();
		});
	});

	describe('when a row is opened', () => {
		it('should show the details behind it', async () => {
			mockFetch([deployCode()]);
			render(Page);
			await userEvent.click(await screen.findByRole('button'));
			expect(await screen.findByText('svc-deploy/req-1')).toBeInTheDocument();
			expect(screen.getByText('SHA256:abc123')).toBeInTheDocument();
		});

		it('should show the options fixed at approval', async () => {
			mockFetch([deployCode()]);
			render(Page);
			await userEvent.click(await screen.findByRole('button'));
			expect(await screen.findByText('permit-pty')).toBeInTheDocument();
			expect(screen.getByText('/usr/local/bin/deploy')).toBeInTheDocument();
			expect(screen.getByText('198.51.100.0/24')).toBeInTheDocument();
		});

		it('should show the redemption log', async () => {
			mockFetch([deployCode()]);
			render(Page);
			await userEvent.click(await screen.findByRole('button'));
			expect(await screen.findByText('Never retrieved.')).toBeInTheDocument();
		});
	});

	// The page exists because the code cannot be shown. A regression that put
	// one on the wire should fail here as loudly as it does server-side.
	it('should never render an enrollment code', async () => {
		mockFetch([{ ...deployCode(), ...({ code: 'super-secret-code' } as object) }]);
		render(Page);
		await screen.findByText('svc-deploy');
		expect(screen.queryByText(/super-secret-code/)).not.toBeInTheDocument();
	});

	describe('when the identity has approved nothing', () => {
		it('should explain how an enrollment gets created', async () => {
			mockFetch([]);
			render(Page);
			expect(
				await screen.findByText(/have not approved any service enrollments/)
			).toBeInTheDocument();
		});
	});

	describe('when the load fails', () => {
		it('should surface the error', async () => {
			mockFetchError('boom');
			render(Page);
			expect(await screen.findByText('Could not load your service codes')).toBeInTheDocument();
		});
	});
});
