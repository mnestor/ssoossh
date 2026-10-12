import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The history keeps its filters in the address, so a certificate's back
// chip (which carries the search) returns to the same narrowed view. The
// router is stubbed: a fake page for what the address says, and a
// goto that records what the page writes back.
const { goto } = vi.hoisted(() => ({ goto: vi.fn() }));

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('$app/navigation', async () => {
	const { fakeAfterNavigate } = await import('#lib/testing/page.svelte.js');
	return { goto, afterNavigate: fakeAfterNavigate };
});

import { fakeGoto, fakeNavigate, resetFakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

/** shallowReplace is how a filter reaches the address: in place, without a
 * navigation and without a history entry of its own. */
const shallowReplace = expect.objectContaining({ shallow: true, replace: true });

const requested: string[] = [];

function stubEmpty() {
	requested.length = 0;
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			requested.push(String(input));
			const body = String(input).includes('/decisions/denied')
				? { denials: [] }
				: { certificates: [] };
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
	goto.mockReset();
	// Shallow routing as SvelteKit does it: page.state is reassigned on
	// every write and page.url never moves. A page that tracked either
	// would write again on its own write (#178).
	goto.mockImplementation(fakeGoto);
	stubEmpty();
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('history filters in the address', () => {
	it('should open on the filters the address names', async () => {
		resetFakePage('http://localhost/logs/me?type=pam&outcome=denied');
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(screen.getByTestId('type-filter-pam')).toHaveAttribute('aria-pressed', 'true');
	});

	// Below `sm` the groups are selects; they have to open on the same
	// narrowed view as the chips, or a phone loses the back chip's state.
	it.each([
		{ testid: 'type-filter-select', want: 'pam' },
		{ testid: 'status-filter-select', want: 'expired' },
		{ testid: 'outcome-filter-select', want: 'denied' }
	])('should open the $testid on the value the address names', async ({ testid, want }) => {
		resetFakePage('http://localhost/logs/me?type=pam&status=expired&outcome=denied');
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(screen.getByTestId(testid)).toHaveValue(want);
	});

	it('should write a filter picked in a select back to the address', async () => {
		resetFakePage('http://localhost/logs/me');
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		await userEvent.selectOptions(screen.getByTestId('type-filter-select'), 'console');
		await vi.waitFor(() =>
			expect(goto).toHaveBeenCalledWith('/logs/me?type=console', shallowReplace)
		);
	});

	it('should read the endpoint the outcome in the address asks for', async () => {
		resetFakePage('http://localhost/logs/me?outcome=denied');
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(requested.some((url) => url.includes('/decisions/denied'))).toBe(true);
	});

	// Issue #178: the write used to re-run the effect that made it, which
	// rewrote the address once per microtask and hung the page.
	it.each([
		{ testid: 'status-filter-live' },
		{ testid: 'type-filter-user' },
		{ testid: 'outcome-filter-denied' }
	])('should write the address once when $testid is picked', async ({ testid }) => {
		resetFakePage('http://localhost/logs/me');
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		await userEvent.click(screen.getByTestId(testid));
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(goto).toHaveBeenCalledTimes(1);
	});

	it('should not write the address when the page opens', async () => {
		resetFakePage('http://localhost/logs/me?status=live&type=user');
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(goto).not.toHaveBeenCalled();
	});

	it('should write a changed filter back to the address', async () => {
		resetFakePage('http://localhost/logs/me');
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		await userEvent.click(screen.getByTestId('type-filter-console'));
		await vi.waitFor(() =>
			expect(goto).toHaveBeenCalledWith('/logs/me?type=console', shallowReplace)
		);
	});
});

describe('a row opened from a filtered history', () => {
	it('should carry the filters to the certificate page for its back chip', async () => {
		resetFakePage('http://localhost/logs/me?type=user');
		vi.stubGlobal(
			'fetch',
			vi.fn((input: RequestInfo | URL) => {
				const body = String(input).includes('/decisions/denied')
					? { denials: [] }
					: {
							certificates: [
								{
									id: 'cert-1',
									type: 'user',
									serial_number: '1',
									principals: 'alice',
									public_key_fingerprint: 'SHA256:abc',
									issued_at: '2026-10-11T10:00:00Z',
									expires_at: new Date(Date.now() + 86400000).toISOString(),
									key_id: 'k1'
								}
							]
						};
				return Promise.resolve(
					new Response(JSON.stringify({ data: body, error: null }), {
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					})
				);
			})
		);
		render(Page);
		expect(await screen.findByTestId('cert-row')).toHaveAttribute(
			'href',
			'/certs/cert-1?from=history&back=%3Ftype%3Duser'
		);
	});
});

// Issue #182: a nav click to the history while it is on screen keeps the
// page mounted. The address drops its query string, and the filters must
// follow it back to their defaults rather than stay set under it.
describe('a nav click to the history while it is open', () => {
	/** openThenRailClick opens the history at href, then clicks the rail's History link. */
	async function openThenRailClick(href: string) {
		resetFakePage(`http://localhost${href}`);
		window.history.replaceState(null, '', href);
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		window.history.pushState(null, '', '/logs/me');
		fakeNavigate('/logs/me');
	}

	it.each([
		{ opened: '?status=live', testid: 'status-filter-any' },
		{ opened: '?type=pam', testid: 'type-filter-any' },
		{ opened: '?outcome=denied', testid: 'outcome-filter-approved' }
	])('should select $testid when the history was open at $opened', async ({ opened, testid }) => {
		await openThenRailClick(`/logs/me${opened}`);
		await vi.waitFor(() =>
			expect(screen.getByTestId(testid)).toHaveAttribute('aria-pressed', 'true')
		);
	});

	it('should empty the search box', async () => {
		await openThenRailClick('/logs/me?q=build');
		await vi.waitFor(() => expect(screen.getByTestId('search-input')).toHaveValue(''));
	});

	it('should read the history again without the filters', async () => {
		await openThenRailClick('/logs/me?type=pam');
		await vi.waitFor(() => expect(requested.at(-1)).not.toContain('type='));
	});

	it('should not write the address', async () => {
		await openThenRailClick('/logs/me?status=live&type=pam');
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(goto).not.toHaveBeenCalled();
	});
});
