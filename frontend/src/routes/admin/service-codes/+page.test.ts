import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import Page from './+page.svelte';
import * as endpoints from '$lib/api/endpoints';
import type { AdminEnrollmentsResponse, AdminEnrollment } from '$lib/api/types';

vi.mock('$app/navigation', () => ({
	pushState: vi.fn()
}));

vi.mock('$app/state', () => ({
	page: {
		url: new URL('http://localhost:5173/admin/service-codes'),
		params: {},
		state: {}
	}
}));

const mockAdminEnrollment = (overrides: Partial<AdminEnrollment> = {}): AdminEnrollment => ({
	id: 'enrollment-1',
	approved_by_username: 'alice',
	approved_by_email: 'alice@example.com',
	principals: ['svc-account'],
	key_id: 'key-1',
	public_key_fingerprint: 'SHA256:abc123',
	options: {
		extensions: [],
		force_command: null,
		source_addresses: null,
		no_touch_required: false
	},
	created_at: '2026-08-01T00:00:00Z',
	expires_at: new Date(Date.now() + 3600000).toISOString(),
	retrieval_count: 5,
	last_retrieved_at: new Date(Date.now() - 60000).toISOString(),
	first_redeemed_at: '2026-08-02T00:00:00Z',
	certificate_valid_seconds: 3600,
	...overrides
});

describe('Admin Service Codes List Page', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('should render page heading and description', async () => {
		vi.spyOn(endpoints, 'listAdminEnrollments').mockResolvedValue({
			enrollments: [],
			meta: { total: 0, limit: 25, offset: 0, page: 1, page_count: 0 }
		});

		render(Page);

		await waitFor(() => {
			expect(screen.getByText('Service enrollment codes')).toBeInTheDocument();
		});
	});

	it('should render SearchInput component', async () => {
		vi.spyOn(endpoints, 'listAdminEnrollments').mockResolvedValue({
			enrollments: [],
			meta: { total: 0, limit: 25, offset: 0, page: 1, page_count: 0 }
		});

		render(Page);

		await waitFor(() => {
			expect(screen.getByTestId('search-enrollments')).toBeInTheDocument();
		});
	});

	it('should render empty state when no enrollments found', async () => {
		vi.spyOn(endpoints, 'listAdminEnrollments').mockResolvedValue({
			enrollments: [],
			meta: { total: 0, limit: 25, offset: 0, page: 1, page_count: 0 }
		});

		render(Page);

		await waitFor(() => {
			expect(screen.getByText(/No service enrollment codes found/)).toBeInTheDocument();
		});
	});

	it('should fetch enrollments on initial load', async () => {
		const mockFetch = vi.spyOn(endpoints, 'listAdminEnrollments').mockResolvedValue({
			enrollments: [mockAdminEnrollment()],
			meta: { total: 1, limit: 25, offset: 0, page: 1, page_count: 1 }
		});

		render(Page);

		await waitFor(() => {
			expect(mockFetch).toHaveBeenCalledWith(expect.any(Object), 25, 0, undefined);
		});
	});

	it('should render enrollment rows when data is loaded', async () => {
		vi.spyOn(endpoints, 'listAdminEnrollments').mockResolvedValue({
			enrollments: [
				mockAdminEnrollment({ id: 'enroll-1', principals: ['svc-a'] }),
				mockAdminEnrollment({ id: 'enroll-2', principals: ['svc-b'] })
			],
			meta: { total: 2, limit: 25, offset: 0, page: 1, page_count: 1 }
		});

		render(Page);

		await waitFor(() => {
			const rows = screen.getAllByTestId('enrollment-row');
			expect(rows).toHaveLength(2);
		});
	});

	it('should render pager when pagination metadata is available', async () => {
		vi.spyOn(endpoints, 'listAdminEnrollments').mockResolvedValue({
			enrollments: Array(25)
				.fill(null)
				.map((_, i) => mockAdminEnrollment({ id: `enroll-${i}` })),
			meta: { total: 50, limit: 25, offset: 0, page: 1, page_count: 2 }
		});

		render(Page);

		await waitFor(() => {
			expect(screen.getByTestId('enrollments-pager')).toBeInTheDocument();
		});
	});

	it('should show loading state initially', async () => {
		vi.spyOn(endpoints, 'listAdminEnrollments').mockImplementation(
			() => new Promise((resolve) => setTimeout(() => resolve({
				enrollments: [],
				meta: { total: 0, limit: 25, offset: 0, page: 1, page_count: 0 }
			}), 100))
		);

		render(Page);

		expect(screen.getByText('Loading…')).toBeInTheDocument();

		await waitFor(() => {
			expect(screen.queryByText('Loading…')).not.toBeInTheDocument();
		});
	});

	it('should show error state on load failure', async () => {
		vi.spyOn(endpoints, 'listAdminEnrollments').mockRejectedValue(new Error('Network error'));

		render(Page);

		await waitFor(() => {
			expect(screen.getByText('Could not load service codes')).toBeInTheDocument();
		});
	});
});
