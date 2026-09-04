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
		service_account: 'svc-deploy',
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
		certificate_serial: '42',
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
	// Editable from the admin console as well as from the holder's own page,
	// for the deployment where the account's holders are outside ssoossh
	// entirely and so have no page of their own to set it on.
	describe('the notification address', () => {
		it('should say who hears about the code when no address is set', async () => {
			mockDetail(enrollment());
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment({ notification_email: '' }),
				now,
				onclosed: vi.fn()
			});
			await waitFor(() =>
				expect(screen.getByText(/go to everyone with access to the account/)).toBeInTheDocument()
			);
		});

		it('should disable saving until the address changes', async () => {
			mockDetail(enrollment());
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment({ notification_email: 'deploys@example.com' }),
				now,
				onclosed: vi.fn()
			});
			await waitFor(() => expect(screen.getByTestId('notification-email-save')).toBeDisabled());

			await userEvent.type(screen.getByTestId('notification-email-input'), 'x');
			expect(screen.getByTestId('notification-email-save')).toBeEnabled();
		});

		it('should report the address the server stored', async () => {
			mockDetail(enrollment());
			render(AdminServiceCodeDetailModal, {
				enrollment: enrollment({ notification_email: '' }),
				now,
				onclosed: vi.fn()
			});
			await waitFor(() => screen.getByTestId('notification-email-input'));

			// Re-stubbed after the detail load so the PATCH gets its own answer.
			vi.stubGlobal(
				'fetch',
				vi.fn(() =>
					Promise.resolve(
						new Response(
							JSON.stringify({ data: { notification_email: 'deploys@example.com' }, error: null }),
							{ status: 200, headers: { 'Content-Type': 'application/json' } }
						)
					)
				)
			);

			await userEvent.type(
				screen.getByTestId('notification-email-input'),
				'  deploys@example.com  '
			);
			await userEvent.click(screen.getByTestId('notification-email-save'));

			expect(await screen.findByTestId('notification-email-saved')).toBeInTheDocument();
			expect(screen.getByTestId('notification-email-input')).toHaveValue('deploys@example.com');
		});
	});
});
