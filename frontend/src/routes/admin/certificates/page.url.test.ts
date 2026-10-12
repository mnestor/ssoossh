import { render, screen } from '@testing-library/svelte';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The admin list keeps its filters in the address, so a certificate's back
// chip returns to the narrowed view. The router is stubbed: a fake page for
// what the address says, and a replaceState that records what is written.
const { replaceState } = vi.hoisted(() => ({ replaceState: vi.fn() }));

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('$app/navigation', () => ({ replaceState, goto: vi.fn() }));

import { resetFakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

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
	replaceState.mockReset();
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
