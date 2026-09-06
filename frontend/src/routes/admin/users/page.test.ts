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

	// The name is the leading column, so it has to be searchable: a
	// directory whose first column cannot be searched sends anyone looking
	// for a person to scroll instead.
	it('should show the name a person is known by', async () => {
		mockUsers([user({ name: 'Alice Ashworth' })]);
		render(Page);
		expect(await screen.findByText('Alice Ashworth')).toBeInTheDocument();
	});

	it('should render an em dash for a user whose provider sent no name', async () => {
		mockUsers([user({ name: '' })]);
		render(Page);
		expect(await screen.findByText('—')).toBeInTheDocument();
	});

	// The Action column held one word per row and cost more width than it
	// earned; the identity cells carry the link instead.
	it('should link the name to the user record', async () => {
		mockUsers([user({ name: 'Alice Ashworth' })]);
		render(Page);
		const link = await screen.findByTestId('user-link');
		expect(link).toHaveAttribute('href', expect.stringContaining('/admin/users/user-1'));
	});

	it('should link the username to the user record', async () => {
		mockUsers([user()]);
		render(Page);
		const link = await screen.findByRole('link', { name: 'alice' });
		expect(link).toHaveAttribute('href', expect.stringContaining('/admin/users/user-1'));
	});

	it('should no longer render a separate action column', async () => {
		mockUsers([user()]);
		render(Page);
		await screen.findByText('alice');
		expect(screen.queryByRole('columnheader', { name: 'Action' })).not.toBeInTheDocument();
	});
});

describe('Admin users status filter', () => {
	it('should send no status parameter while showing every account', async () => {
		mockUsers([user()]);
		render(Page);
		await screen.findByText('alice');
		expect(requests.every((url) => !url.includes('status='))).toBe(true);
	});

	it('should ask the server for active accounts only', async () => {
		mockUsers([user()]);
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByTestId('status-filter-active'));
		await waitFor(() => {
			expect(requests.some((url) => url.includes('status=active'))).toBe(true);
		});
	});

	it('should ask the server for disabled accounts only', async () => {
		mockUsers([user()]);
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByTestId('status-filter-disabled'));
		await waitFor(() => {
			expect(requests.some((url) => url.includes('status=disabled'))).toBe(true);
		});
	});

	it('should drop the status parameter again when the filter returns to all', async () => {
		mockUsers([user()]);
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByTestId('status-filter-disabled'));
		await waitFor(() => {
			expect(requests.some((url) => url.includes('status=disabled'))).toBe(true);
		});

		requests.length = 0;
		await userEvent.click(screen.getByTestId('status-filter-all'));
		await waitFor(() => {
			expect(requests.length).toBeGreaterThan(0);
			expect(requests.every((url) => !url.includes('status='))).toBe(true);
		});
	});

	// The filtered set is a different list, so page 3 of the old one means
	// nothing in it.
	it('should return to the first page when the filter changes', async () => {
		mockUsers([user()], { total: 60, page_count: 3 });
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByRole('button', { name: /next/i }));
		await waitFor(() => {
			expect(requests.some((url) => url.includes('offset=25'))).toBe(true);
		});

		requests.length = 0;
		await userEvent.click(screen.getByTestId('status-filter-disabled'));
		await waitFor(() => {
			const filtered = requests.filter((url) => url.includes('status=disabled'));
			expect(filtered.length).toBeGreaterThan(0);
			// A zero offset is omitted entirely by getAdminUsers, so "back to
			// the first page" reads as the absence of the parameter.
			expect(filtered.every((url) => !url.includes('offset='))).toBe(true);
		});
	});

	it('should mark the selected filter as pressed for assistive technology', async () => {
		mockUsers([user()]);
		render(Page);
		await screen.findByText('alice');

		await userEvent.click(screen.getByTestId('status-filter-active'));
		await waitFor(() => {
			expect(screen.getByTestId('status-filter-active')).toHaveAttribute('aria-pressed', 'true');
			expect(screen.getByTestId('status-filter-all')).toHaveAttribute('aria-pressed', 'false');
		});
	});
});
