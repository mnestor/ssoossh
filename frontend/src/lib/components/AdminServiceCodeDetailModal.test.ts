import { describe, expect, it, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/svelte';
import AdminServiceCodeDetailModal from './AdminServiceCodeDetailModal.svelte';
import * as endpoints from '$lib/api/endpoints';
import type { AdminEnrollment } from '$lib/api/types';

const mockAdminEnrollment = (overrides: Partial<AdminEnrollment> = {}): AdminEnrollment => ({
	id: 'enrollment-123',
	approved_by_username: 'alice',
	approved_by_email: 'alice@example.com',
	principals: ['service-account'],
	key_id: 'key-abc123',
	public_key_fingerprint: 'SHA256:abcdef123456',
	options: {
		extensions: ['permit-pty'],
		force_command: null,
		source_addresses: ['10.0.0.0/8'],
		no_touch_required: false
	},
	created_at: '2026-08-01T10:00:00Z',
	expires_at: new Date(Date.now() + 86400000).toISOString(),
	retrieval_count: 42,
	last_retrieved_at: new Date(Date.now() - 3600000).toISOString(),
	first_redeemed_at: '2026-08-02T00:00:00Z',
	certificate_valid_seconds: 7200,
	...overrides
});

describe('AdminServiceCodeDetailModal', () => {
	beforeEach(() => {
		vi.clearAllMocks();
	});

	it('should show active status when not expired', async () => {
		const enrollment = mockAdminEnrollment({
			expires_at: new Date(Date.now() + 86400000).toISOString()
		});
		vi.spyOn(endpoints, 'getAdminEnrollmentDetail').mockResolvedValue({
			enrollment,
			retrievals: { retrievals: [] },
			retrieval_total: 0
		});

		render(AdminServiceCodeDetailModal, {
			props: {
				enrollment,
				onclosed: vi.fn()
			}
		});

		await waitFor(() => {
			expect(screen.getByText('Active')).toBeInTheDocument();
		});
	});

	it('should show expired status when past expiry', async () => {
		const enrollment = mockAdminEnrollment({
			expires_at: new Date(Date.now() - 3600000).toISOString()
		});
		vi.spyOn(endpoints, 'getAdminEnrollmentDetail').mockResolvedValue({
			enrollment,
			retrievals: { retrievals: [] },
			retrieval_total: 0
		});

		render(AdminServiceCodeDetailModal, {
			props: {
				enrollment,
				onclosed: vi.fn()
			}
		});

		await waitFor(() => {
			expect(screen.getByText('Expired')).toBeInTheDocument();
		});
	});

	it('should display reassign control', async () => {
		const enrollment = mockAdminEnrollment();
		vi.spyOn(endpoints, 'getAdminEnrollmentDetail').mockResolvedValue({
			enrollment,
			retrievals: { retrievals: [] },
			retrieval_total: 0
		});

		render(AdminServiceCodeDetailModal, {
			props: {
				enrollment,
				onclosed: vi.fn()
			}
		});

		await waitFor(() => {
			expect(screen.getByPlaceholderText('Username or user ID')).toBeInTheDocument();
		});
	});

	it('should display early expiry confirmation', async () => {
		const enrollment = mockAdminEnrollment({
			expires_at: new Date(Date.now() + 86400000).toISOString()
		});
		vi.spyOn(endpoints, 'getAdminEnrollmentDetail').mockResolvedValue({
			enrollment,
			retrievals: { retrievals: [] },
			retrieval_total: 0
		});

		render(AdminServiceCodeDetailModal, {
			props: {
				enrollment,
				onclosed: vi.fn()
			}
		});

		await waitFor(() => {
			expect(screen.getByRole('button', { name: 'Expire this code' })).toBeInTheDocument();
		});
	});

	it('should not show expiry control when already expired', async () => {
		const enrollment = mockAdminEnrollment({
			expires_at: new Date(Date.now() - 3600000).toISOString()
		});
		vi.spyOn(endpoints, 'getAdminEnrollmentDetail').mockResolvedValue({
			enrollment,
			retrievals: { retrievals: [] },
			retrieval_total: 0
		});

		render(AdminServiceCodeDetailModal, {
			props: {
				enrollment,
				onclosed: vi.fn()
			}
		});

		await waitFor(() => {
			expect(screen.queryByText('Expire this code')).not.toBeInTheDocument();
		});
	});
});
