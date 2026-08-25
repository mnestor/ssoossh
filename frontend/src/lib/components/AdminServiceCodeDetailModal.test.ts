import { render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';

import type { AdminEnrollment, EnrollmentRetrievalResponse } from '$lib/api/types';
import AdminServiceCodeDetailModal from './AdminServiceCodeDetailModal.svelte';

const now = new Date('2026-08-22T12:00:00Z');

/** enrollment builds an admin enrollment, overriding only what a case cares about. */
function enrollment(overrides: Partial<AdminEnrollment> = {}): AdminEnrollment {
	return {
		id: 'enr-1234-5678',
		approved_by_username: 'alice',
		approved_by_email: 'alice@example.com',
		principals: ['svc-deploy'],
		key_id: 'svc-deploy/req-1',
		public_key_fingerprint: 'SHA256:abc',
		options: {
			extensions: ['permit-pty'],
			force_command: '/usr/local/bin/deploy',
			source_addresses: ['198.51.100.0/24'],
			no_touch_required: true
		},
		certificate_valid_seconds: 3600,
		created_at: '2026-08-20T12:00:00Z',
		expires_at: '2026-11-20T12:00:00Z',
		first_redeemed_at: '2026-08-22T10:00:00Z',
		last_retrieved_at: '2026-08-22T10:00:00Z',
		retrieval_count: 12,
		...overrides
	};
}

/** mockDetail stubs the admin detail fetch the modal makes on open. */
function mockDetail(
	enrollmentData: AdminEnrollment,
	retrievals: EnrollmentRetrievalResponse[] = [],
	retrieval_total = retrievals.length
) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(
					JSON.stringify({
						data: { enrollment: enrollmentData, retrievals, retrieval_total },
						error: null
					}),
					{
						status: 200,
						headers: { 'Content-Type': 'application/json' }
					}
				)
			)
		)
	);
}

/** aRedemption is one retrieval log row, overridable per case. */
function aRedemption(
	overrides: Partial<EnrollmentRetrievalResponse> = {}
): EnrollmentRetrievalResponse {
	return {
		retrieved_at: '2026-08-22T10:00:00Z',
		source_ip: '203.0.113.9',
		certificate_serial: 42,
		succeeded: true,
		...overrides
	};
}

afterEach(() => {
	vi.unstubAllGlobals();
});

describe('AdminServiceCodeDetailModal', () => {
	it('should show the short form of the enrollment id', async () => {
		mockDetail(enrollment());
		render(AdminServiceCodeDetailModal, {
			enrollment: enrollment(),
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText('enr-1')).toBeInTheDocument());
	});

	it('should show the approver username and email', async () => {
		mockDetail(enrollment());
		render(AdminServiceCodeDetailModal, {
			enrollment: enrollment(),
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText(/alice.*alice@example.com/)).toBeInTheDocument());
	});

	it('should show the account the code mints for', async () => {
		mockDetail(enrollment());
		render(AdminServiceCodeDetailModal, {
			enrollment: enrollment(),
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText('svc-deploy')).toBeInTheDocument());
	});

	it('should show the certificate lifetime', async () => {
		mockDetail(enrollment());
		render(AdminServiceCodeDetailModal, {
			enrollment: enrollment(),
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText('1h')).toBeInTheDocument());
	});

	it('should say certificates last until the code expires when no lifetime is reported', async () => {
		const data = enrollment({ certificate_valid_seconds: undefined });
		mockDetail(data);
		render(AdminServiceCodeDetailModal, {
			enrollment: data,
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText('until the code expires')).toBeInTheDocument());
	});

	it('should show the key id fixed at approval', async () => {
		mockDetail(enrollment());
		render(AdminServiceCodeDetailModal, {
			enrollment: enrollment(),
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText('svc-deploy/req-1')).toBeInTheDocument());
	});

	it('should show the fingerprint of the bound keypair', async () => {
		mockDetail(enrollment());
		render(AdminServiceCodeDetailModal, {
			enrollment: enrollment(),
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText('SHA256:abc')).toBeInTheDocument());
	});

	it('should show the granted extensions', async () => {
		mockDetail(enrollment());
		render(AdminServiceCodeDetailModal, {
			enrollment: enrollment(),
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText('permit-pty')).toBeInTheDocument());
	});

	it('should show the forced command', async () => {
		mockDetail(enrollment());
		render(AdminServiceCodeDetailModal, {
			enrollment: enrollment(),
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText('/usr/local/bin/deploy')).toBeInTheDocument());
	});

	it('should show the source address restriction', async () => {
		mockDetail(enrollment());
		render(AdminServiceCodeDetailModal, {
			enrollment: enrollment(),
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText('198.51.100.0/24')).toBeInTheDocument());
	});

	it('should say when no options were fixed at approval', async () => {
		const data = enrollment({
			options: { extensions: [], no_touch_required: false }
		});
		mockDetail(data);
		render(AdminServiceCodeDetailModal, {
			enrollment: data,
			now,
			onclosed: vi.fn()
		});
		await waitFor(() =>
			expect(screen.getByText(/No extensions or restrictions/)).toBeInTheDocument()
		);
	});

	it('should report when the code stops working', async () => {
		mockDetail(enrollment());
		render(AdminServiceCodeDetailModal, {
			enrollment: enrollment(),
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText(/expires in/)).toBeInTheDocument());
	});

	it('should report an expired code as already expired', async () => {
		const data = enrollment({ expires_at: '2026-08-21T12:00:00Z' });
		mockDetail(data);
		render(AdminServiceCodeDetailModal, {
			enrollment: data,
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => expect(screen.getByText('Expired')).toBeInTheDocument());
	});

	it('should never render an enrollment code', async () => {
		const data = { ...enrollment(), ...({ code: 'super-secret-code' } as object) };
		mockDetail(data as AdminEnrollment);
		render(AdminServiceCodeDetailModal, {
			enrollment: data as AdminEnrollment,
			now,
			onclosed: vi.fn()
		});
		await waitFor(() => {
			expect(screen.queryByText(/super-secret-code/)).not.toBeInTheDocument();
		});
	});

	describe('the retrieval log', () => {
		it('should list each redemption', async () => {
			mockDetail(enrollment(), [aRedemption()]);
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment(),
				now,
				onclosed: vi.fn()
			});
			await waitFor(() => expect(screen.getByText('203.0.113.9')).toBeInTheDocument());
		});

		it('should mark a redemption that failed at signing', async () => {
			mockDetail(enrollment(), [
				aRedemption({
					succeeded: false
				})
			]);
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment(),
				now,
				onclosed: vi.fn()
			});
			await waitFor(() => expect(screen.getByText('Failed')).toBeInTheDocument());
		});

		it('should say the most recent page when the log is truncated', async () => {
			mockDetail(enrollment(), [aRedemption()], 8760);
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment(),
				now,
				onclosed: vi.fn()
			});
			await waitFor(() =>
				expect(screen.getByText(/1 most recent of 8760 redemptions/)).toBeInTheDocument()
			);
		});

		it('should not claim truncation when the whole log fits', async () => {
			mockDetail(enrollment(), [aRedemption()]);
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment(),
				now,
				onclosed: vi.fn()
			});
			await waitFor(() => screen.getByText('203.0.113.9'));
			expect(screen.queryByText(/most recent of/)).not.toBeInTheDocument();
		});
	});

	describe('admin actions', () => {
		it('should have a reassignment field that accepts user IDs', async () => {
			mockDetail(enrollment());
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment(),
				now,
				onclosed: vi.fn()
			});

			await waitFor(() =>
				expect(screen.getByPlaceholderText('Username or user ID')).toBeInTheDocument()
			);
			const input = screen.getByPlaceholderText('Username or user ID') as HTMLInputElement;
			expect(input.type).toBe('text');
		});

		it('should show an early-expiry button when the code is not yet expired', async () => {
			mockDetail(enrollment());
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment(),
				now,
				onclosed: vi.fn()
			});
			await waitFor(() => expect(screen.getByText('Expire this code')).toBeInTheDocument());
		});

		it('should not show an early-expiry button when the code is already expired', async () => {
			const data = enrollment({ expires_at: '2026-08-21T12:00:00Z' });
			mockDetail(data);
			render(AdminServiceCodeDetailModal, {
				enrollment: data,
				now,
				onclosed: vi.fn()
			});
			await waitFor(() => {
				expect(screen.queryByText('Expire this code')).not.toBeInTheDocument();
			});
		});

		it('should show expiry confirmation that names the consequence: further retrievals blocked', async () => {
			mockDetail(enrollment());
			const user = userEvent.setup();
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment(),
				now,
				onclosed: vi.fn()
			});

			const expireButton = await screen.findByText('Expire this code');
			await user.click(expireButton);

			await waitFor(() => {
				expect(screen.getByText(/prevent further certificate retrievals/)).toBeInTheDocument();
			});
		});

		it('should say that already-issued certificates keep working after expiry', async () => {
			mockDetail(enrollment());
			const user = userEvent.setup();
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment(),
				now,
				onclosed: vi.fn()
			});

			const expireButton = await screen.findByText('Expire this code');
			await user.click(expireButton);

			await waitFor(() => {
				expect(
					screen.getByText(/will continue to work until they expire on their own/)
				).toBeInTheDocument();
			});
		});

		it('should have a free-text username/user-id field for reassignment', async () => {
			mockDetail(enrollment());
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment(),
				now,
				onclosed: vi.fn()
			});

			await waitFor(() => {
				const input = screen.getByPlaceholderText('Username or user ID') as HTMLInputElement;
				expect(input).toBeInTheDocument();
				expect(input.type).toBe('text');
			});
		});

		it('should show reassignment description mentioning the service account', async () => {
			mockDetail(enrollment());
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment(),
				now,
				onclosed: vi.fn()
			});

			await waitFor(() => {
				expect(screen.getByText(/Transfer ownership to another user/)).toBeInTheDocument();
				// The reassignment label text should mention the account
				const label = screen.getByText(/Reassign to user/);
				expect(label.closest('div')).toHaveTextContent('svc-deploy');
			});
		});

		it('should have a Reassign button that disables until a user ID is entered', async () => {
			mockDetail(enrollment());
			const user = userEvent.setup();
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment(),
				now,
				onclosed: vi.fn()
			});

			await waitFor(() => {
				const button = screen.getByRole('button', { name: 'Reassign' });
				expect(button).toBeDisabled();
			});

			const input = await screen.findByPlaceholderText('Username or user ID');
			await user.type(input, 'bob');

			const button = screen.getByRole('button', { name: 'Reassign' });
			expect(button).not.toBeDisabled();
		});
	});
});
