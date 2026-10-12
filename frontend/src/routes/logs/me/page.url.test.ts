import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// The history keeps its filters in the address, so a certificate's back
// chip (which carries the search) returns to the same narrowed view. The
// router is stubbed: a fake page for what the address says, and a
// replaceState that records what the page writes back.
const { replaceState } = vi.hoisted(() => ({ replaceState: vi.fn() }));

vi.mock('$app/state', async () => {
	const { fakePage } = await import('#lib/testing/page.svelte.js');
	return { page: fakePage };
});
vi.mock('$app/navigation', () => ({ replaceState, goto: vi.fn() }));

import { resetFakePage } from '#lib/testing/page.svelte.js';
import Page from './+page.svelte';

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
	replaceState.mockReset();
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
			expect(replaceState).toHaveBeenCalledWith('/logs/me?type=console', expect.anything())
		);
	});

	it('should read the endpoint the outcome in the address asks for', async () => {
		resetFakePage('http://localhost/logs/me?outcome=denied');
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(requested.some((url) => url.includes('/decisions/denied'))).toBe(true);
	});

	it('should write a changed filter back to the address', async () => {
		resetFakePage('http://localhost/logs/me');
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		await userEvent.click(screen.getByTestId('type-filter-console'));
		await vi.waitFor(() =>
			expect(replaceState).toHaveBeenCalledWith('/logs/me?type=console', expect.anything())
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
