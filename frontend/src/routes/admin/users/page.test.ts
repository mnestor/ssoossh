import { render, screen, waitFor } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import type { AdminUserSummary } from '#lib/api/types.js';
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

/**
 * mockUsers answers every request with users, the total and, when given,
 * the cursor for a next page.
 */
function mockUsers(users: AdminUserSummary[], page: { total?: number; next_cursor?: string } = {}) {
	mockUserPages({ users, total: page.total ?? users.length, next_cursor: page.next_cursor });
}

/**
 * mockUserPages answers the nth request with the nth page, repeating the
 * last; a string page is a failed request with that message. The first
 * page is what the page opens on, the rest what Load more appends.
 */
function mockUserPages(
	...pages: ({ users: AdminUserSummary[]; total: number; next_cursor?: string } | string)[]
) {
	let call = 0;
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			requests.push(String(input));
			const body = pages[Math.min(call++, pages.length - 1)];
			if (typeof body === 'string') {
				return Promise.resolve(
					new Response(JSON.stringify({ data: null, error: { message: body } }), {
						status: 500,
						headers: { 'Content-Type': 'application/json' }
					})
				);
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

/** twoPages is a directory of two users, one per page. */
function twoPages(...later: string[]) {
	mockUserPages({ users: [user()], total: 2, next_cursor: 'user-1' }, ...later, {
		users: [user({ id: 'user-2', username: 'bob', email: 'bob@corp.example' })],
		total: 2
	});
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

	it('should show the empty state when nobody has signed in', async () => {
		mockUsers([]);
		render(Page);
		expect(await screen.findByText('No users yet')).toBeInTheDocument();
	});

	// "Disabled" with nobody disabled used to blame a search that was not
	// there and say nobody had signed in.
	it('should say nobody is disabled when the Disabled filter is empty', async () => {
		mockUsers([]);
		render(Page);
		await screen.findByText('No users yet');
		await userEvent.click(screen.getByTestId('status-filter-disabled'));
		expect(await screen.findByTestId('users-empty')).toHaveTextContent('No disabled users');
	});

	it('should offer a way back to everyone from a filtered empty list', async () => {
		mockUsers([]);
		render(Page);
		await screen.findByText('No users yet');
		await userEvent.click(screen.getByTestId('status-filter-disabled'));
		expect(await screen.findByTestId('users-clear-filters')).toBeInTheDocument();
	});

	it('should call the date column what it is', async () => {
		mockUsers([user()]);
		render(Page);
		expect(await screen.findByRole('columnheader', { name: 'First sign-in' })).toBeInTheDocument();
	});

	it('should not make a dash into a link for a user with no name', async () => {
		mockUsers([user({ name: '' })]);
		render(Page);
		await screen.findByTestId('user-username-link');
		expect(screen.queryByTestId('user-link')).not.toBeInTheDocument();
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
		twoPages();
		render(Page);
		await userEvent.click(await screen.findByTestId('users-load-more'));
		await screen.findByText('bob');

		await userEvent.type(screen.getByRole('searchbox'), 'ali');
		await waitFor(() => {
			const searched = requests.filter((url) => url.includes('q=ali'));
			expect(searched.length).toBeGreaterThan(0);
			// A search that kept the cursor would resume the new result set
			// part way down, which reads to the operator as missing matches.
			expect(searched.every((url) => !url.includes('after='))).toBe(true);
		});
	});

	it('should ask for the page after the cursor when Load more is pressed', async () => {
		twoPages();
		render(Page);
		await userEvent.click(await screen.findByTestId('users-load-more'));
		await waitFor(() => {
			expect(requests.some((url) => url.includes('after=user-1'))).toBe(true);
		});
	});

	it('should append the next page below the users already shown', async () => {
		twoPages();
		render(Page);
		await userEvent.click(await screen.findByTestId('users-load-more'));
		await screen.findByText('bob');
		expect(screen.getByText('alice')).toBeInTheDocument();
	});

	it('should offer Load more when more users exist', async () => {
		twoPages();
		render(Page);
		expect(await screen.findByTestId('users-load-more')).toBeInTheDocument();
	});

	it('should remove Load more once the last page is in', async () => {
		twoPages();
		render(Page);
		await userEvent.click(await screen.findByTestId('users-load-more'));
		await screen.findByText('bob');
		expect(screen.queryByTestId('users-load-more')).not.toBeInTheDocument();
	});

	it('should keep the users shown and offer a retry when Load more fails', async () => {
		twoPages('boom');
		render(Page);
		await userEvent.click(await screen.findByTestId('users-load-more'));
		await screen.findByTestId('users-more-error');
		expect(screen.getByText('alice')).toBeInTheDocument();
		await userEvent.click(screen.getByRole('button', { name: /try again/i }));
		expect(await screen.findByText('bob')).toBeInTheDocument();
	});

	it('should not offer Load more when a single page holds every user', async () => {
		mockUsers([user()]);
		render(Page);
		await screen.findByText('alice');
		expect(screen.queryByTestId('users-load-more')).not.toBeInTheDocument();
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

	// The filtered set is a different list, so how far the old one was
	// loaded means nothing in it.
	it('should return to the first page when the filter changes', async () => {
		twoPages();
		render(Page);
		await userEvent.click(await screen.findByTestId('users-load-more'));
		await screen.findByText('bob');

		requests.length = 0;
		await userEvent.click(screen.getByTestId('status-filter-disabled'));
		await waitFor(() => {
			const filtered = requests.filter((url) => url.includes('status=disabled'));
			expect(filtered.length).toBeGreaterThan(0);
			expect(filtered.every((url) => !url.includes('after='))).toBe(true);
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

describe('when a slow request finishes after a newer one', () => {
	// Nothing cancels a request in flight and the search box stays live, so
	// an older answer could land last and replace the list with results for
	// a term the reader had already moved off.
	it('should keep the newest result rather than the last to arrive', async () => {
		const resolvers: ((response: Response) => void)[] = [];
		vi.stubGlobal(
			'fetch',
			vi.fn(() => new Promise<Response>((resolve) => resolvers.push(resolve)))
		);
		/** answer completes the nth request with one user. */
		const answer = (index: number, username: string) =>
			resolvers[index](
				new Response(
					JSON.stringify({
						data: {
							users: [user({ id: username, username })],
							total: 1
						},
						error: null
					}),
					{ status: 200, headers: { 'Content-Type': 'application/json' } }
				)
			);

		render(Page);
		await waitFor(() => expect(resolvers.length).toBe(1));
		await userEvent.type(screen.getByRole('searchbox'), 'beta{Enter}');
		await waitFor(() => expect(resolvers.length).toBe(2));

		answer(1, 'newest-result');
		await screen.findByText('newest-result');
		answer(0, 'stale-result');
		await new Promise((resolve) => setTimeout(resolve, 0));

		expect(screen.queryByText('stale-result')).not.toBeInTheDocument();
	});
});
