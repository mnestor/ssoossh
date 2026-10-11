import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { CertificateListResponse } from '#lib/api/types.js';
import Page from './+page.svelte';

/** mockFetch stubs the global fetch with a response body and status. */
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

/** mockFetchError stubs the global fetch to reject with an error message. */
function mockFetchError(message = 'network error') {
	vi.stubGlobal(
		'fetch',
		vi.fn(() => Promise.reject(new Error(message)))
	);
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('Dashboard page', () => {
	describe('when certificates load successfully with an empty list', () => {
		beforeEach(() => {
			const emptyResponse: CertificateListResponse = {
				certificates: []
			};
			mockFetch(emptyResponse);
		});

		it('should not show Loading… after the fetch resolves', async () => {
			render(Page);
			// Give the effect a chance to run and complete
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
		});

		it('should show the empty-state message when no certificates are present', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/ssoossh login/)).toBeInTheDocument();
		});

		it('should display the empty-state prompt specifically', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/No certificates yet/)).toBeInTheDocument();
		});
	});

	describe('when the fetch fails', () => {
		beforeEach(() => {
			mockFetchError('server error');
		});

		it('should not show Loading… after the fetch rejects', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
		});

		it('should show the error message', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/Could not load your recent certificates/)).toBeInTheDocument();
		});

		// A network error is not "you hold no certificates": the empty
		// state under it told the reader to run `ssoossh login`.
		it('should not show the empty state under the error', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByTestId('dashboard-empty')).not.toBeInTheDocument();
		});
	});

	describe('when Try again is pressed after a failed load', () => {
		it('should ask the server again and clear the error once it answers', async () => {
			const empty: CertificateListResponse = { certificates: [] };
			vi.stubGlobal(
				'fetch',
				vi
					.fn()
					.mockRejectedValueOnce(new Error('server error'))
					.mockResolvedValue(
						new Response(JSON.stringify({ data: empty, error: null }), {
							status: 200,
							headers: { 'Content-Type': 'application/json' }
						})
					)
			);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			await userEvent.click(screen.getByRole('button', { name: 'Try again' }));
			await new Promise((resolve) => setTimeout(resolve, 0));

			expect(screen.queryByTestId('dashboard-error')).not.toBeInTheDocument();
		});
	});

	describe('when certificates load successfully with data', () => {
		beforeEach(() => {
			const populatedResponse: CertificateListResponse = {
				certificates: [
					{
						id: 'cert-1',
						type: 'user',
						serial_number: '1',
						principals: 'alice',
						public_key_fingerprint: 'SHA256:abc123',
						issued_at: '2026-08-01T10:00:00Z',
						expires_at: new Date(Date.now() + 86400000).toISOString(),
						decided_at: '2026-08-01T10:00:00Z',
						decided_by_username: 'system',
						decided_by_subject: '',
						key_id: 'key-1'
					}
				]
			};
			mockFetch(populatedResponse);
		});

		it('should not show Loading… after the fetch resolves', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
		});

		it('should display the certificate principal', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText('alice')).toBeInTheDocument();
		});
	});

	// A request that never answers, so the wait itself is what is on screen.
	// With a resolving stub the response lands inside the same 300ms and the
	// gate is never the thing under test.
	it('should hold the rows back until the first response settles', async () => {
		vi.useFakeTimers();
		vi.stubGlobal(
			'fetch',
			vi.fn(() => new Promise(() => {}))
		);
		try {
			render(Page);
			expect(screen.queryByTestId('dashboard-loading')).not.toBeInTheDocument();
			await vi.advanceTimersByTimeAsync(300);
			expect(screen.getByTestId('dashboard-loading')).toBeInTheDocument();
		} finally {
			vi.useRealTimers();
		}
	});

	// A row is a link to the certificate's own page. It used to open a
	// dialog over the list, which could not be reloaded, linked to, or left
	// with the browser's own Back.
	describe('the rows', () => {
		beforeEach(() => {
			const response: CertificateListResponse = {
				certificates: [
					{
						id: 'cert-1',
						type: 'user',
						serial_number: '1',
						principals: 'alice',
						public_key_fingerprint: 'SHA256:abc123',
						issued_at: '2026-08-01T10:00:00Z',
						expires_at: new Date(Date.now() + 86400000).toISOString(),
						key_id: 'key-1'
					}
				]
			};
			mockFetch(response);
		});

		it('should link a row to the certificate it is about', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByRole('link', { name: /key-1/ })).toHaveAttribute(
				'href',
				'/certs/cert-1?from=dashboard'
			);
		});
	});

	describe('as a summary rather than a second history', () => {
		function stubMany(count: number, nextCursor: string | null = 'cursor-2') {
			const certs = Array.from({ length: count }, (_, i) => ({
				id: `cert-${i}`,
				type: 'user',
				serial_number: String(i),
				principals: 'alice',
				public_key_fingerprint: 'SHA256:abc',
				issued_at: new Date(Date.now() - i * 60000).toISOString(),
				expires_at: new Date(Date.now() + 86400000).toISOString(),
				key_id: `key-${i}`
			}));
			const fetchMock = vi.fn(() =>
				Promise.resolve(
					new Response(
						JSON.stringify({ data: { certificates: certs, next_cursor: nextCursor }, error: null }),
						{ status: 200, headers: { 'Content-Type': 'application/json' } }
					)
				)
			);
			vi.stubGlobal('fetch', fetchMock);
			return fetchMock;
		}

		it('should ask the server for the five most recent', async () => {
			const fetchMock = stubMany(5);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(String((fetchMock.mock.calls[0] as unknown[])[0])).toContain('limit=5');
		});

		it('should show no more than five rows', async () => {
			stubMany(8);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getAllByTestId('cert-row')).toHaveLength(5);
		});

		// Older certificates are the history page's: paging here made the
		// landing page a second history without refusals or filters.
		it('should offer no Load more even when there is more', async () => {
			stubMany(5);
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.queryByRole('button', { name: /Load more/ })).not.toBeInTheDocument();
		});

		it('should call the list what it shows', async () => {
			stubMany(1);
			render(Page);
			expect(screen.getByRole('heading', { name: 'Recent certificates' })).toBeInTheDocument();
		});

		it('should lead to the full history', async () => {
			stubMany(1);
			render(Page);
			expect(screen.getByTestId('dashboard-history-link')).toHaveAttribute('href', '/logs/me');
		});
	});

	it('should point an empty dashboard at the console login too', async () => {
		mockFetch({ certificates: [] });
		render(Page);
		await new Promise((resolve) => setTimeout(resolve, 0));
		expect(screen.getByTestId('dashboard-empty-console')).toHaveAttribute('href', '/console');
	});
});
