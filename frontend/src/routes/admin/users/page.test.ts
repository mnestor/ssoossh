import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { AdminUserSummary } from '$lib/api/types';
import Page from './+page.svelte';

// The admin list is driven entirely by what the server returns, so these
// tests stub fetch rather than the endpoints module: that way the query
// string the page builds is itself under test, and a page that silently
// stopped sending `q` would fail here rather than at an operator's desk.
const requests: string[] = [];

/** user builds a directory row, overriding only what a case cares about. */
function user(overrides: Partial<AdminUserSummary> = {}): AdminUserSummary {
	return {
		id: 'user-1',
		username: 'alice',
		email: 'alice@corp.example',
		subject: 'sub-alice',
		created_at: '2026-08-01T10:00:00Z',
		updated_at: '2026-08-01T10:00:00Z',
		disabled_at: undefined,
		...overrides
	} as AdminUserSummary;
}

/** mockUsers answers every request with users and a page envelope. */
function mockUsers(users: AdminUserSummary[], meta: Partial<Record<string, number>> = {}) {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			requests.push(String(input));
			const body = {
				users,
				meta: {
					total: users.length,
					limit: 25,
					offset: 0,
					page: 1,
					page_count: 1,
					...meta
				}
			};
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
	requests.length = 0;
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Admin users list', () => {
	it('should list a user returned by the server', async () => {
		mockUsers([user()]);
		render(Page);
		expect(await screen.findByText('alice')).toBeInTheDocument();
	});

	it('should mark an active user as active', async () => {
		mockUsers([user()]);
		render(Page);
		expect(await screen.findByText('Active')).toBeInTheDocument();
	});

	it('should mark a disabled user as disabled', async () => {
		mockUsers([user({ disabled_at: '2026-08-20T09:00:00Z' })]);
		render(Page);
		expect(await screen.findByText('Disabled')).toBeInTheDocument();
	});

	it('should show the empty state when no users match', async () => {
		mockUsers([]);
		render(Page);
		expect(await screen.findByText('No users found')).toBeInTheDocument();
	});

	it('should surface a load failure rather than an empty directory', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new Error('network is down')))
		);
		render(Page);
		expect(await screen.findByText(/network is down/)).toBeInTheDocument();
	});

	it('should send the typed term to the server', async () => {
		mockUsers([user()]);
		render(Page);
		await screen.findByText('alice');

		await userEvent.type(screen.getByRole('searchbox'), 'ali');
		await waitFor(() => {
			expect(requests.some((url) => url.includes('q=ali'))).toBe(true);
		});
	});

	it('should return to the first page when a new search is run', async () => {
		mockUsers([user()], { total: 60, page_count: 3, offset: 50, page: 3 });
		render(Page);
		await screen.findByText('alice');

		await userEvent.type(screen.getByRole('searchbox'), 'ali');
		await waitFor(() => {
			const searched = requests.filter((url) => url.includes('q=ali'));
			expect(searched.length).toBeGreaterThan(0);
			// getAdminUsers omits a zero offset entirely (0 is falsy), so
			// "back to the first page" shows up as the absence of the
			// parameter. A search that kept the old offset would ask for page
			// 3 of a result set that may only have one page, which reads to
			// the operator as "no matches".
			expect(searched.every((url) => !url.includes('offset='))).toBe(true);
		});
	});

	it('should ask for the next window when the pager is used', async () => {
		mockUsers([user()], { total: 60, page_count: 3 });
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByRole('button', { name: /next/i }));
		await waitFor(() => {
			expect(requests.some((url) => url.includes('offset=25'))).toBe(true);
		});
	});

	it('should not render a pager when a single page holds every user', async () => {
		mockUsers([user()]);
		render(Page);
		await screen.findByText('alice');
		expect(screen.queryByRole('navigation', { name: /pagination/i })).not.toBeInTheDocument();
	});
});
