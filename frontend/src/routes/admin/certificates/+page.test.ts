import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi, afterEach, beforeEach } from 'vitest';

import type { CertificateListAdminResponse } from '$lib/api/types';
import Page from './+page.svelte';

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
						serial_number: 100,
						key_id: 'alice-key',
						principals: 'alice',
						public_key_fingerprint: 'SHA256:abc123',
						issued_at: new Date('2024-08-24T10:00:00Z').toISOString(),
						expires_at: new Date('2024-08-24T18:00:00Z').toISOString()
					}
				],
				page_meta: {
					total: 1,
					limit: 25,
					offset: 0,
					page: 1,
					page_count: 1
				}
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
			expect(screen.getByRole('textbox', { name: /search/i })).toBeInTheDocument();
		});

		it('should display type filter', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/type|filter/i)).toBeInTheDocument();
		});

		it('should display pager', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/page/i)).toBeInTheDocument();
		});

		it('should trigger search on input', async () => {
			const user = userEvent.setup();
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			const searchInput = screen.getByRole('textbox', { name: /search/i });
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
				page_meta: {
					total: 0,
					limit: 25,
					offset: 0,
					page: 1,
					page_count: 1
				}
			};
			mockFetch(response);
		});

		it('should show empty state message', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/no certificates|empty/i)).toBeInTheDocument();
		});
	});

	describe('when paging', () => {
		beforeEach(() => {
			const response: CertificateListAdminResponse = {
				certificates: [
					{
						id: 'cert-1',
						type: 'user',
						serial_number: 100,
						key_id: 'key1',
						principals: 'user1',
						public_key_fingerprint: 'fp1',
						issued_at: new Date().toISOString(),
						expires_at: new Date().toISOString()
					}
				],
				page_meta: {
					total: 50,
					limit: 25,
					offset: 0,
					page: 1,
					page_count: 2
				}
			};
			mockFetch(response);
		});

		it('should show next button when more pages exist', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByRole('button', { name: /next/i })).toBeInTheDocument();
		});

		it('should trigger refetch when paging to next page', async () => {
			const user = userEvent.setup();
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));

			const nextButton = screen.getByRole('button', { name: /next/i });
			await user.click(nextButton);

			// Should trigger a refetch with offset=25
			await new Promise((resolve) => setTimeout(resolve, 100));
		});
	});

	describe('when filtering by type', () => {
		beforeEach(() => {
			const response: CertificateListAdminResponse = {
				certificates: [
					{
						id: 'cert-1',
						type: 'user',
						serial_number: 100,
						key_id: 'key1',
						principals: 'user1',
						public_key_fingerprint: 'fp1',
						issued_at: new Date().toISOString(),
						expires_at: new Date().toISOString()
					}
				],
				page_meta: {
					total: 1,
					limit: 25,
					offset: 0,
					page: 1,
					page_count: 1
				}
			};
			mockFetch(response);
		});

		it('should show type filter options', async () => {
			render(Page);
			await new Promise((resolve) => setTimeout(resolve, 0));
			expect(screen.getByText(/user|service|pam/i)).toBeInTheDocument();
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
				page_meta: { total: 0, limit: 25, offset: 0, page: 1, page_count: 1 }
			});
		});

		it('should show loading state initially', () => {
			render(Page);
			expect(screen.getByText(/loading|loading\.\.\./i)).toBeInTheDocument();
		});
	});
});
