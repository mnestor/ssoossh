import { render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { ServiceEnrollment } from '$lib/api/types';
import { fakePage, resetFakePage } from '$lib/testing/page.svelte';
import Page from './+page.svelte';

// Test methodology: render the holder-facing code page against a stubbed
// fetch, with $app/state replaced by the shared fake so the route parameter
// can be set. What matters is that the page resolves the id out of the
// caller's own enrollment list — the list the server has already scoped to
// the accounts this identity holds — that a code outside it reads as no
// such code rather than as an error, and that the chip at the top returns
// to the account the code belongs to.

vi.mock('$app/state', async () => {
	const { fakePage } = await import('$lib/testing/page.svelte');
	return { page: fakePage };
});

/** deployCode is one live enrollment, overridable per case. */
function deployCode(overrides: Partial<ServiceEnrollment> = {}): ServiceEnrollment {
	return {
		id: 'enr-1',
		service_account: 'svc-deploy',
		approved_by_username: 'alice',
		certificate_request_id: 'req-1',
		principals: ['svc-deploy'],
		key_id: 'svc-deploy/req-1',
		public_key_fingerprint: 'SHA256:abc123',
		options: { extensions: ['permit-pty'], no_touch_required: false },
		certificate_valid_seconds: 3600,
		created_at: '2026-08-20T12:00:00Z',
		expires_at: new Date(Date.now() + 90 * 24 * 60 * 60 * 1000).toISOString(),
		retrieval_count: 0,
		...overrides
	};
}

/** mockFetch answers the enrollment list, and the two calls the detail
 * makes for itself: the redemption log and the account's holders. */
function mockFetch(enrollments: ServiceEnrollment[]) {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			const url = String(input);
			let body: unknown = { enrollments };
			if (url.includes('/retrievals')) {
				body = { retrievals: [], total: 0 };
			} else if (url.includes('/holders')) {
				body = { service_account: 'svc-deploy', holders: [] };
			}
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
	resetFakePage('http://localhost/service-codes/enr-1');
	fakePage.params = { id: 'enr-1' };
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Service code page', () => {
	it('should show the code the route names', async () => {
		mockFetch([deployCode()]);
		render(Page);
		expect(await screen.findByTestId('service-code-account')).toHaveTextContent('svc-deploy');
	});

	it('should show what the code hands out', async () => {
		mockFetch([deployCode()]);
		render(Page);
		expect(await screen.findByText('SHA256:abc123')).toBeInTheDocument();
	});

	// The chip is the point of the page having an address at all: the reader
	// came from the account's codes and has to be able to get back to them.
	it('should offer a way back to the account the code belongs to', async () => {
		mockFetch([deployCode()]);
		render(Page);
		await screen.findByTestId('service-code-account');
		expect(screen.getByTestId('service-code-back')).toHaveAttribute(
			'href',
			'/service-codes?account=svc-deploy'
		);
	});

	it('should name the account on the way back', async () => {
		mockFetch([deployCode()]);
		render(Page);
		await screen.findByTestId('service-code-account');
		expect(screen.getByTestId('service-code-back')).toHaveTextContent('All codes for svc-deploy');
	});

	// A code outside the caller's own list is a code they may not see, and
	// the list is already scoped to that. Saying which of the two it is would
	// report the existence of codes the reader has no access to.
	it('should say there is no such code when the id resolves to nothing', async () => {
		mockFetch([deployCode({ id: 'enr-other', service_account: 'svc-other' })]);
		render(Page);
		expect(await screen.findByTestId('service-code-missing')).toBeInTheDocument();
	});

	it('should fall back to the account list when there is no code to go back from', async () => {
		mockFetch([]);
		render(Page);
		await screen.findByTestId('service-code-missing');
		expect(screen.getByTestId('service-code-back')).toHaveAttribute('href', '/service-codes');
	});

	it('should surface a load failure', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new Error('boom')))
		);
		render(Page);
		expect(await screen.findByText('Could not load this service code')).toBeInTheDocument();
	});

	// Retiring the code is the page's own action, in the heading's top right
	// rather than a section near the foot of it, and an already-expired code
	// needs no control: the outcome it would produce is already true.
	it('should offer to retire a code that still works', async () => {
		mockFetch([deployCode()]);
		render(Page);
		await screen.findByTestId('service-code-account');
		expect(screen.getByTestId('expire-code')).toBeInTheDocument();
	});

	it('should not offer to retire a code that has already expired', async () => {
		mockFetch([deployCode({ expires_at: '2026-08-21T12:00:00Z' })]);
		render(Page);
		await screen.findByTestId('service-code-account');
		expect(screen.queryByTestId('expire-code')).not.toBeInTheDocument();
	});

	// The heading is where the action hangs, so it has to name the page
	// rather than stay screen-reader-only the way it used to. It names the
	// thing, not the account: the back chip above it already says which
	// account the code belongs to.
	it('should name the page in the heading', async () => {
		mockFetch([deployCode()]);
		render(Page);
		expect(
			await screen.findByRole('heading', { name: 'Service code', level: 1 })
		).toBeInTheDocument();
	});

	// The page exists because the code cannot be shown. A regression that put
	// one on the wire should fail here as loudly as it does server-side.
	it('should never render an enrollment code', async () => {
		mockFetch([{ ...deployCode(), ...({ code: 'super-secret-code' } as object) }]);
		render(Page);
		await screen.findByTestId('service-code-account');
		expect(screen.queryByText(/super-secret-code/)).not.toBeInTheDocument();
	});
});
