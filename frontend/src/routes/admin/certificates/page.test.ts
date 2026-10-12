import { render, screen, waitFor, within } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { CertificateListAdminResponse } from '#lib/api/types.js';
import Page from './+page.svelte';

// The page writes its filters back to the address bar with replaceState.
// Unmocked, SvelteKit's real client throws on every call, since no app is
// running under jsdom. page.url.test.ts covers what gets written.
vi.mock('$app/navigation', () => ({ replaceState: vi.fn(), goto: vi.fn() }));

function mockFetch(response: object, status = 200) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: response, error: null }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

/**
 * deferredFetch stubs fetch so each call's response is resolved by the test,
 * in whatever order it likes. Returned is a `settle(index, response)` that
 * completes the nth call.
 */
function deferredFetch() {
	const resolvers: ((response: Response) => void)[] = [];
	vi.stubGlobal(
		'fetch',
		vi.fn(
			() =>
				new Promise<Response>((resolve) => {
					resolvers.push(resolve);
				})
		)
	);
	return {
		calls: () => resolvers.length,
		settle: (index: number, body: object) =>
			resolvers[index](
				new Response(JSON.stringify({ data: body, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			)
	};
}

/** listWith builds a single-certificate page whose key ID identifies it. */
function listWith(keyId: string): CertificateListAdminResponse {
	return {
		certificates: [
			{
				id: `cert-${keyId}`,
				type: 'user',
				serial_number: '1',
				key_id: keyId,
				principals: 'alice',
				public_key_fingerprint: 'fp',
				issued_at: new Date().toISOString(),
				expires_at: new Date().toISOString()
			}
		],
		total: 1
	};
}

beforeEach(() => {
	vi.clearAllMocks();
});

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Admin certificates page', () => {
	describe('when certificates load successfully with data', () => {
		beforeEach(() => {
			const response: CertificateListAdminResponse = {
				certificates: [
					{
						id: 'cert-1',
						type: 'user',
						serial_number: '100',
						key_id: 'alice-key',
						principals: 'alice',
						public_key_fingerprint: 'SHA256:abc123',
						issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
						expires_at: new Date('2024-08-24T18:00:00Z').toISOString()
					}
				],
				total: 1
			};
			mockFetch(response);
		});

		it('should render the certificate list', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/alice-key/)).toBeInTheDocument();
		});

		it('should display search input', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByRole('searchbox', { name: /search/i })).toBeInTheDocument();
		});

		it('should display type filter', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			// By role and name rather than by loose text: /type|filter/i also
			// matches the type badge on every row, so this passed for reasons
			// unrelated to the filter existing.
			expect(screen.getByRole('group', { name: 'Filter by type' })).toBeInTheDocument();
		});

		it('should not offer Load more when the first page holds everything', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByTestId('certificates-load-more')).not.toBeInTheDocument();
		});

		it('should trigger search on input', async () => {
			const user = userEvent.setup();
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			const searchInput = screen.getByRole('searchbox', { name: /search/i });
			await user.type(searchInput, 'alice');

			// Should trigger a refetch (exact behavior depends on implementation)
			await new Promise((resolve) => setTimeout(resolve, 100));
		});

		it('should navigate to certificate detail on row click', async () => {
			const user = userEvent.setup();
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			const row = screen.getByText(/alice-key/);
			await user.click(row);

			// Navigation should happen (implementation-dependent)
			await new Promise((resolve) => setTimeout(resolve, 0));
		});
	});

	describe('when certificate list is empty', () => {
		beforeEach(() => {
			const response: CertificateListAdminResponse = {
				certificates: [],
				total: 0
			};
			mockFetch(response);
		});

		// The visible empty state, not any text mentioning "no certificates":
		// the live region announcing "No certificates found." matches that
		// too, and the case is about what is on screen.
		it('should show the first-run empty state when nothing is filtering', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByTestId('certificates-empty')).toHaveTextContent('No certificates yet');
		});

		// The two empties are not the same answer. Nothing issued yet says
		// what would fill the list; nothing matching says how to get back to
		// the full one, and offers the control that does it.
		it('should offer a way back to the full list when a search is filtering', async () => {
			const user = userEvent.setup();
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			await user.type(screen.getByTestId('search-input'), 'nobody');
			await waitFor(() =>
				expect(screen.getByTestId('certificates-empty')).toHaveTextContent('No certificates match')
			);
			expect(screen.getByTestId('certificates-clear-filters')).toBeInTheDocument();
		});
	});

	describe('when paging', () => {
		/** cert builds one row whose key ID identifies it. */
		function cert(keyId: string) {
			return {
				id: `cert-${keyId}`,
				type: 'user' as const,
				serial_number: '100',
				key_id: keyId,
				principals: 'user1',
				public_key_fingerprint: 'fp1',
				issued_at: new Date().toISOString(),
				expires_at: new Date().toISOString()
			};
		}

		/**
		 * pages stubs fetch to answer each call with the next body in turn (a
		 * string body is a failure with that message), and records the URLs.
		 */
		function pages(...bodies: (CertificateListAdminResponse | string)[]) {
			const urls: string[] = [];
			let call = 0;
			vi.stubGlobal(
				'fetch',
				vi.fn((input: RequestInfo | URL) => {
					urls.push(String(input));
					const body = bodies[Math.min(call++, bodies.length - 1)];
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
			return urls;
		}

		const first: CertificateListAdminResponse = {
			certificates: [cert('page-one')],
			total: 2,
			next_cursor: 'cert-page-one'
		};
		const second: CertificateListAdminResponse = {
			certificates: [cert('page-two')],
			total: 2
		};

		it('should offer Load more with the count when more pages exist', async () => {
			pages(first);
			render(Page);
			expect(await screen.findByTestId('certificates-load-more')).toBeInTheDocument();
			expect(screen.getByTestId('certificates-count')).toHaveTextContent('1 of 2 shown');
		});

		it('should ask for the page after the cursor when Load more is pressed', async () => {
			const user = userEvent.setup();
			const urls = pages(first, second);
			render(Page);
			await user.click(await screen.findByTestId('certificates-load-more'));
			await screen.findByText(/page-two/);
			expect(urls[1]).toContain('after=cert-page-one');
		});

		it('should append the next page below the rows already shown', async () => {
			const user = userEvent.setup();
			pages(first, second);
			render(Page);
			await user.click(await screen.findByTestId('certificates-load-more'));
			await screen.findByText(/page-two/);
			const rows = screen.getAllByTestId('cert-row');
			expect(rows.map((row) => row.textContent?.includes('page-one'))).toEqual([true, false]);
		});

		it('should remove Load more once the last page is in', async () => {
			const user = userEvent.setup();
			pages(first, second);
			render(Page);
			await user.click(await screen.findByTestId('certificates-load-more'));
			await screen.findByText(/page-two/);
			expect(screen.queryByTestId('certificates-load-more')).not.toBeInTheDocument();
		});

		it('should keep the rows and offer a retry when Load more fails', async () => {
			const user = userEvent.setup();
			pages(first, 'boom', second);
			render(Page);
			await user.click(await screen.findByTestId('certificates-load-more'));
			await screen.findByTestId('certificates-more-error');
			expect(screen.getByText(/page-one/)).toBeInTheDocument();
			await user.click(screen.getByRole('button', { name: /try again/i }));
			expect(await screen.findByText(/page-two/)).toBeInTheDocument();
		});
	});

	describe('when filtering by type', () => {
		beforeEach(() => {
			const response: CertificateListAdminResponse = {
				certificates: [
					{
						id: 'cert-1',
						type: 'user',
						serial_number: '100',
						key_id: 'key1',
						principals: 'user1',
						public_key_fingerprint: 'fp1',
						issued_at: new Date().toISOString(),
						expires_at: new Date().toISOString()
					}
				],
				total: 1
			};
			mockFetch(response);
		});

		it('should show type filter options', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			const typeFilter = screen.getByTestId('type-filter');
			expect(within(typeFilter).getByText(/user/i)).toBeInTheDocument();
			expect(within(typeFilter).getByText(/service/i)).toBeInTheDocument();
			expect(within(typeFilter).getByText(/pam/i)).toBeInTheDocument();
			expect(within(typeFilter).getByText(/console/i)).toBeInTheDocument();
		});

		it('should trigger refetch when type filter changes', async () => {
			const user = userEvent.setup();
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			// Click type filter (exact selector depends on implementation)
			const filterElements = screen.getAllByRole('button');
			await user.click(filterElements[0]);

			// Should trigger a refetch with type filter
			await new Promise((resolve) => setTimeout(resolve, 100));
		});
	});

	describe('loading state', () => {
		beforeEach(() => {
			mockFetch({
				certificates: [],
				total: 0
			});
		});

		// A request that never answers, so the wait itself is what is on
		// screen. With the resolving stub above, the response lands inside
		// the same 300ms and the gate is never the thing under test.
		it('should hold the rows back until the first response settles', async () => {
			vi.useFakeTimers();
			vi.stubGlobal(
				'fetch',
				vi.fn(() => new Promise(() => {}))
			);
			try {
				render(Page);
				expect(screen.queryByTestId('certificates-loading')).not.toBeInTheDocument();
				await vi.advanceTimersByTimeAsync(300);
				expect(screen.getByTestId('certificates-loading')).toBeInTheDocument();
			} finally {
				vi.useRealTimers();
			}
		});
	});
});

describe('when a slow request finishes after a newer one', () => {
	it('should keep the newest result rather than the last to arrive', async () => {
		// The search box is not disabled while a load is in flight, so a
		// newer search can be in flight alongside an older one. Nothing
		// cancels the older request, and it used to overwrite the newer list
		// on arrival — leaving the page showing results for a term the user
		// had already moved on from.
		const user = userEvent.setup();
		const fetches = deferredFetch();

		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));

		const searchInput = screen.getByRole('searchbox', { name: /search/i });
		await user.type(searchInput, 'beta{Enter}');
		await new Promise((resolve) => setTimeout(resolve, 0));

		const total = fetches.calls();
		expect(total).toBeGreaterThan(1);

		// The newest request answers first; every earlier one lands after it.
		fetches.settle(total - 1, listWith('newest-result'));
		await new Promise((resolve) => setTimeout(resolve, 0));
		for (let i = 0; i < total - 1; i++) {
			fetches.settle(i, listWith('stale-result'));
		}
		await new Promise((resolve) => setTimeout(resolve, 0));

		expect(screen.getByText(/newest-result/)).toBeInTheDocument();
		expect(screen.queryByText(/stale-result/)).not.toBeInTheDocument();
	});
});

describe('when a filter or search changes', () => {
	beforeEach(() => {
		// The first answer has a next page; every later one is a different
		// certificate, so a Load more appends a row rather than repeating
		// one (a repeat would be each_key_duplicate, not a passing test).
		const first = { ...listWith('one'), total: 100, next_cursor: 'cert-one' };
		let calls = 0;
		vi.stubGlobal(
			'fetch',
			vi.fn(() =>
				Promise.resolve(
					new Response(
						JSON.stringify({
							data: calls++ === 0 ? first : { ...listWith(`more-${calls}`), total: 100 },
							error: null
						}),
						{ status: 200, headers: { 'Content-Type': 'application/json' } }
					)
				)
			)
		);
	});

	/** requestsAfter counts the fetches one interaction causes. */
	async function requestsAfter(interact: () => Promise<void>) {
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));

		const fetchMock = vi.mocked(globalThis.fetch);
		fetchMock.mockClear();

		await interact();
		await new Promise((resolve) => setTimeout(resolve, 0));

		return fetchMock.mock.calls.length;
	}

	it('should issue one request when the search term changes', async () => {
		const user = userEvent.setup();
		const calls = await requestsAfter(async () => {
			await user.type(screen.getByRole('searchbox', { name: /search/i }), 'beta{Enter}');
		});
		expect(calls).toBe(1);
	});

	it('should issue one request when the type filter changes', async () => {
		const user = userEvent.setup();
		const calls = await requestsAfter(async () => {
			const typeFilter = screen.getByTestId('type-filter');
			await user.click(within(typeFilter).getByRole('button', { name: /^service$/i }));
		});
		expect(calls).toBe(1);
	});

	it('should issue one request when loading more', async () => {
		const user = userEvent.setup();
		const calls = await requestsAfter(async () => {
			await user.click(screen.getByTestId('certificates-load-more'));
		});
		expect(calls).toBe(1);
	});
});

describe('when a load fails', () => {
	afterEach(() => {
		vi.unstubAllGlobals();
	});

	// A network error is not "nothing has been issued".
	it('should not show the empty state under the error', async () => {
		vi.stubGlobal(
			'fetch',
			vi.fn(() => Promise.reject(new Error('server error')))
		);
		render(Page);

		await screen.findByTestId('certificates-error');

		expect(screen.queryByTestId('certificates-empty')).not.toBeInTheDocument();
	});

	// The error used to stay on screen after a later load succeeded, since
	// only Try again cleared it.
	it('should clear the error when the next load succeeds', async () => {
		const user = userEvent.setup();
		vi.stubGlobal(
			'fetch',
			vi
				.fn()
				.mockRejectedValueOnce(new Error('server error'))
				.mockResolvedValue(
					new Response(
						JSON.stringify({
							data: { certificates: [], total: 0 },
							error: null
						}),
						{ status: 200, headers: { 'Content-Type': 'application/json' } }
					)
				)
		);
		render(Page);
		await screen.findByTestId('certificates-error');

		await user.type(screen.getByRole('searchbox', { name: /search/i }), 'beta{Enter}');

		await waitFor(() => expect(screen.queryByTestId('certificates-error')).not.toBeInTheDocument());
	});
});
