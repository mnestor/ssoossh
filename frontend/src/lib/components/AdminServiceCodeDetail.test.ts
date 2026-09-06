import { render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';

import type { AdminEnrollmentDetail } from '$lib/api/endpoints';
import type { AdminEnrollment, EnrollmentRetrievalResponse } from '$lib/api/types';
import AdminServiceCodeDetail from './AdminServiceCodeDetail.svelte';

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

/** detail is what the route hands the panel: the enrollment and its
 * redemption log. The panel never fetches it — GET
 * /api/admin/enrollments/:id is audited, and a second read would record a
 * second look. */
function detail(
	enrollmentData: AdminEnrollment = enrollment(),
	retrievals: EnrollmentRetrievalResponse[] = [],
	retrieval_total = retrievals.length
): AdminEnrollmentDetail {
	return { enrollment: enrollmentData, retrievals, retrieval_total };
}

/** stubHolders answers the one call the panel still makes on its own: the
 * account's holders, which has its own tests. */
function stubHolders() {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: { service_account: 'svc-deploy', holders: [] } }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
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

describe('AdminServiceCodeDetail', () => {
	it('should show the short form of the enrollment id', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(), now });
		expect(screen.getByText('enr-1')).toBeInTheDocument();
	});

	it('should show the approver username and email', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(), now });
		expect(screen.getByText(/alice.*alice@example.com/)).toBeInTheDocument();
	});

	it('should show the account the code mints for', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(), now });
		expect(screen.getByTestId('service-code-account')).toHaveTextContent('svc-deploy');
	});

	it('should show the certificate lifetime', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(), now });
		expect(screen.getByText('1h')).toBeInTheDocument();
	});

	it('should say certificates last until the code expires when no lifetime is reported', () => {
		stubHolders();
		render(AdminServiceCodeDetail, {
			detail: detail(enrollment({ certificate_valid_seconds: undefined })),
			now
		});
		expect(screen.getByText('until the code expires')).toBeInTheDocument();
	});

	it('should show the key id fixed at approval', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(), now });
		expect(screen.getByText('svc-deploy/req-1')).toBeInTheDocument();
	});

	it('should show the fingerprint of the bound keypair', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(), now });
		expect(screen.getByText('SHA256:abc')).toBeInTheDocument();
	});

	it('should show the granted extensions', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(), now });
		expect(screen.getByText('permit-pty')).toBeInTheDocument();
	});

	it('should show the forced command', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(), now });
		expect(screen.getByText('/usr/local/bin/deploy')).toBeInTheDocument();
	});

	it('should show the source address restriction', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(), now });
		expect(screen.getByText('198.51.100.0/24')).toBeInTheDocument();
	});

	it('should say when no options were fixed at approval', () => {
		stubHolders();
		render(AdminServiceCodeDetail, {
			detail: detail(enrollment({ options: { extensions: [], no_touch_required: false } })),
			now
		});
		expect(screen.getByText(/No extensions or restrictions/)).toBeInTheDocument();
	});

	it('should report the validity window as one period', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(), now });
		expect(screen.getByText(/Aug 20, 2026.* – .*Nov 20, 2026/)).toBeInTheDocument();
	});

	it('should report how much of the validity window is left', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(), now });
		expect(screen.getByText(/left\)/)).toBeInTheDocument();
	});

	// The log below lists every redemption with the host that made it, so a
	// summary above it was two numbers to reconcile rather than an answer.
	it('should not restate the redemption count above the log', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(enrollment(), [aRedemption()]), now });
		expect(screen.queryByText('Last redeemed')).not.toBeInTheDocument();
	});

	it('should point at the redemption history from the code summary', () => {
		stubHolders();
		render(AdminServiceCodeDetail, { detail: detail(enrollment(), [aRedemption()]), now });
		expect(screen.getByTestId('redemption-history-link')).toHaveAttribute(
			'href',
			'#redemption-history'
		);
	});

	it('should report an expired code as already expired', () => {
		stubHolders();
		render(AdminServiceCodeDetail, {
			detail: detail(enrollment({ expires_at: '2026-08-21T12:00:00Z' })),
			now
		});
		expect(screen.getByText('Expired')).toBeInTheDocument();
	});

	// An expired code needs no expire control: the outcome it would produce
	// is already true.
	it('should not offer to expire a code that has already expired', () => {
		stubHolders();
		render(AdminServiceCodeDetail, {
			detail: detail(enrollment({ expires_at: '2026-08-21T12:00:00Z' })),
			now
		});
		expect(screen.queryByTestId('admin-expire-code')).not.toBeInTheDocument();
	});

	it('should never render an enrollment code', () => {
		stubHolders();
		const data = { ...enrollment(), ...({ code: 'super-secret-code' } as object) };
		render(AdminServiceCodeDetail, { detail: detail(data as AdminEnrollment), now });
		expect(screen.queryByText(/super-secret-code/)).not.toBeInTheDocument();
	});

	describe('the retrieval log', () => {
		it('should list each redemption', () => {
			stubHolders();
			render(AdminServiceCodeDetail, { detail: detail(enrollment(), [aRedemption()]), now });
			expect(screen.getByText('203.0.113.9')).toBeInTheDocument();
		});

		it('should mark a redemption that failed at signing', () => {
			stubHolders();
			render(AdminServiceCodeDetail, {
				detail: detail(enrollment(), [aRedemption({ succeeded: false })]),
				now
			});
			expect(screen.getByText('Failed')).toBeInTheDocument();
		});

		it('should say the most recent page when the log is truncated', () => {
			stubHolders();
			render(AdminServiceCodeDetail, {
				detail: detail(enrollment(), [aRedemption()], 8760),
				now
			});
			expect(screen.getByText(/1 most recent of 8760 redemptions/)).toBeInTheDocument();
		});

		it('should not claim truncation when the whole log fits', () => {
			stubHolders();
			render(AdminServiceCodeDetail, { detail: detail(enrollment(), [aRedemption()]), now });
			expect(screen.queryByText(/most recent of/)).not.toBeInTheDocument();
		});

		// The section is unconditional now that it is what the summary points
		// at: a link into a section that is not rendered goes nowhere.
		it('should say so when the code has never been redeemed', () => {
			stubHolders();
			render(AdminServiceCodeDetail, { detail: detail(enrollment(), []), now });
			expect(screen.getByText('Never redeemed.')).toBeInTheDocument();
		});
	});

	// Editable from the admin console as well as from the holder's own page,
	// for the deployment where the account's holders are outside ssoossh
	// entirely and so have no page of their own to set it on.
	describe('the notification address', () => {
		it('should say who hears about the code when no address is set', () => {
			stubHolders();
			render(AdminServiceCodeDetail, {
				detail: detail(enrollment({ notification_email: '' })),
				now
			});
			expect(screen.getByText(/go to everyone with access to the account/)).toBeInTheDocument();
		});

		it('should disable saving until the address changes', async () => {
			stubHolders();
			render(AdminServiceCodeDetail, {
				detail: detail(enrollment({ notification_email: 'deploys@example.com' })),
				now
			});
			await waitFor(() => expect(screen.getByTestId('notification-email-save')).toBeDisabled());

			await userEvent.type(screen.getByTestId('notification-email-input'), 'x');
			expect(screen.getByTestId('notification-email-save')).toBeEnabled();
		});

		it('should report the address the server stored', async () => {
			// The PATCH gets its own answer; the holders call above it is
			// answered by the same stub, which is harmless — the panel renders
			// nobody and this case is about the address.
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

			render(AdminServiceCodeDetail, {
				detail: detail(enrollment({ notification_email: '' })),
				now
			});

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
