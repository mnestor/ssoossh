import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The admin list keeps its filters in the address, so a certificate's back
// chip returns to the narrowed view. The router is stubbed: a fake page for
// what the address says, and a goto that records what is written.
const { goto } = vi.hoisted(() => ({ goto: vi.fn() }));

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('$app/navigation', () => ({ goto }));

import { fakeGoto, resetFakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

/** shallowReplace is how a filter reaches the address: in place, without a
 * navigation and without a history entry of its own. */
const shallowReplace = expect.objectContaining({ shallow: true, replace: true });

const requested: string[] = [];

function stubList(certificates: object[]) {
	requested.length = 0;
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			requested.push(String(input));
			return Promise.resolve(
				new Response(
					JSON.stringify({
						data: { certificates, total: certificates.length },
						error: null
					}),
					{ status: 200, headers: { 'Content-Type': 'application/json' } }
				)
			);
		})
	);
}

const issued = {
	id: 'cert-1',
	type: 'user',
	serial_number: '1',
	key_id: 'k1',
	principals: 'alice',
	public_key_fingerprint: 'SHA256:abc',
	reported_username: 'alice',
	reported_hostname: 'laptop',
	decided_by_username: 'bob',
	issued_at: '2026-10-11T10:00:00Z',
	expires_at: new Date(Date.now() + 86400000).toISOString()
};

beforeEach(() => {
	goto.mockReset();
	// Shallow routing as SvelteKit does it: page.state is reassigned on
	// every write and page.url never moves. A page that tracked either
	// would write again on its own write (#178).
	goto.mockImplementation(fakeGoto);
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('admin certificate list', () => {
	// The subject is what the requesting machine claimed; the approver is the
	// one verified identity on the row.
	it('should show who approved each certificate', async () => {
		resetFakePage('http://localhost/admin/certificates');
		stubList([issued]);
		render(Page);
		expect(await screen.findByTestId('cert-row')).toHaveTextContent('approved by bob');
	});

	it('should open on the filters the address names', async () => {
		resetFakePage('http://localhost/admin/certificates?type=pam&q=build');
		stubList([]);
		render(Page);
		await vi.waitFor(() => expect(requested.some((url) => url.includes('type=pam'))).toBe(true));
	});

	// An address saved while the list paged by offset still opens: the
	// server now refuses offset, so the page must not pass it along.
	it('should not send an offset when the address names one', async () => {
		resetFakePage('http://localhost/admin/certificates?type=user&offset=50');
		stubList([issued]);
		render(Page);
		await screen.findByTestId('cert-row');
		expect(requested.some((url) => url.includes('offset'))).toBe(false);
	});

	it('should write a changed filter back to the address', async () => {
		resetFakePage('http://localhost/admin/certificates');
		stubList([]);
		render(Page);
		await userEvent.click(await screen.findByTestId('status-filter-expired'));
		await vi.waitFor(() =>
			expect(goto).toHaveBeenCalledWith('/admin/certificates?status=expired', shallowReplace)
		);
	});

	// Issue #178: the write used to re-run the effect that made it, which
	// rewrote the address once per microtask and hung the page.
	it('should write the address once when a filter changes', async () => {
		resetFakePage('http://localhost/admin/certificates');
		stubList([]);
		render(Page);
		await userEvent.click(await screen.findByTestId('status-filter-expired'));
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(goto).toHaveBeenCalledTimes(1);
	});

	it('should carry its filters to the certificate page for the back chip', async () => {
		resetFakePage('http://localhost/admin/certificates?type=user');
		stubList([issued]);
		render(Page);
		expect(await screen.findByTestId('cert-row')).toHaveAttribute(
			'href',
			'/certs/cert-1?from=admin&back=%3Ftype%3Duser'
		);
	});
});
