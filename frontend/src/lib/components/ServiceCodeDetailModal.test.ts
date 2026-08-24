import { render, screen, waitFor } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';

import type { EnrollmentRetrievalsResponse, ServiceEnrollment } from '$lib/api/types';
import ServiceCodeDetailModal from './ServiceCodeDetailModal.svelte';

const now = new Date('2026-08-22T12:00:00Z');

/** enrollment builds a service enrollment, overriding only what a case cares about. */
function enrollment(overrides: Partial<ServiceEnrollment> = {}): ServiceEnrollment {
	return {
		id: 'enr-1234-5678',
		certificate_request_id: 'req-1',
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

/** mockRetrievals stubs the retrieval-log fetch the panel makes on open.
 * `total` defaults to the page length — the untruncated case. */
function mockRetrievals(
	retrievals: EnrollmentRetrievalsResponse['retrievals'],
	total = retrievals.length
) {
	vi.stubGlobal(
		'fetch',
		vi.fn(() =>
			Promise.resolve(
				new Response(JSON.stringify({ data: { retrievals, total }, error: null }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				})
			)
		)
	);
}

/** aRedemption is one row of the log, overridable per case. */
function aRedemption(overrides: Partial<EnrollmentRetrievalsResponse['retrievals'][0]> = {}) {
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

describe('ServiceCodeDetailModal', () => {
	it('should show the short form of the enrollment id', () => {
		mockRetrievals([]);
		render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
		expect(screen.getByText('enr-1')).toBeInTheDocument();
	});

	it('should label the id rather than prefixing it with a bare hash', () => {
		mockRetrievals([]);
		render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
		expect(screen.getByTitle('enr-1234-5678')).toHaveTextContent(/^ID/);
	});

	it('should name the account the code mints for', () => {
		mockRetrievals([]);
		render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
		// Scoped to the element that names the account rather than matching
		// the text anywhere: the reassignment copy below names the same
		// principal, so a bare text match is ambiguous.
		expect(screen.getByTestId('service-code-account')).toHaveTextContent('svc-deploy');
	});

	it('should show the lifetime of the certificates it hands out', () => {
		mockRetrievals([]);
		render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
		expect(screen.getByText('1h')).toBeInTheDocument();
	});

	it('should say certificates last until the code expires when no lifetime is reported', () => {
		mockRetrievals([]);
		const row = enrollment({ certificate_valid_seconds: undefined });
		render(ServiceCodeDetailModal, { enrollment: row, now, onclosed: vi.fn() });
		expect(screen.getByText('until the code expires')).toBeInTheDocument();
	});

	it('should show the key id fixed at approval', () => {
		mockRetrievals([]);
		render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
		expect(screen.getByText('svc-deploy/req-1')).toBeInTheDocument();
	});

	it('should show the fingerprint of the bound keypair', () => {
		mockRetrievals([]);
		render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
		expect(screen.getByText('SHA256:abc')).toBeInTheDocument();
	});

	it('should show the granted extensions', () => {
		mockRetrievals([]);
		render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
		expect(screen.getByText('permit-pty')).toBeInTheDocument();
	});

	it('should show the forced command', () => {
		mockRetrievals([]);
		render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
		expect(screen.getByText('/usr/local/bin/deploy')).toBeInTheDocument();
	});

	it('should show the source address restriction', () => {
		mockRetrievals([]);
		render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
		expect(screen.getByText('198.51.100.0/24')).toBeInTheDocument();
	});

	it('should say when no options were fixed at approval', () => {
		mockRetrievals([]);
		const row = enrollment({ options: { extensions: [], no_touch_required: false } });
		render(ServiceCodeDetailModal, { enrollment: row, now, onclosed: vi.fn() });
		expect(screen.getByText(/No extensions or restrictions/)).toBeInTheDocument();
	});

	it('should report when the code stops working', () => {
		mockRetrievals([]);
		render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
		expect(screen.getByText(/expires in/)).toBeInTheDocument();
	});

	it('should report an expired code as already expired', () => {
		mockRetrievals([]);
		const row = enrollment({ expires_at: '2026-08-21T12:00:00Z' });
		render(ServiceCodeDetailModal, { enrollment: row, now, onclosed: vi.fn() });
		expect(screen.getByText('Expired')).toBeInTheDocument();
	});

	// The whole reason the panel exists is that the code cannot be shown.
	it('should never render an enrollment code', () => {
		mockRetrievals([]);
		const row = { ...enrollment(), ...({ code: 'super-secret-code' } as object) };
		render(ServiceCodeDetailModal, { enrollment: row, now, onclosed: vi.fn() });
		expect(screen.queryByText(/super-secret-code/)).not.toBeInTheDocument();
	});

	describe('the retrieval log', () => {
		it('should list each redemption', async () => {
			mockRetrievals([
				{
					retrieved_at: '2026-08-22T10:00:00Z',
					source_ip: '203.0.113.9',
					certificate_serial: 42,
					succeeded: true
				}
			]);
			render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
			expect(await screen.findByText('203.0.113.9')).toBeInTheDocument();
		});

		// A redemption that passed code validation but failed at signing is
		// still worth surfacing: someone held the code.
		it('should mark a redemption that failed at signing', async () => {
			mockRetrievals([
				{
					retrieved_at: '2026-08-22T10:00:00Z',
					source_ip: '203.0.113.9',
					certificate_serial: 42,
					succeeded: false
				}
			]);
			render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
			expect(await screen.findByText('Failed')).toBeInTheDocument();
		});

		it('should say so when the code has never been retrieved', async () => {
			mockRetrievals([]);
			render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
			expect(await screen.findByText('Never retrieved.')).toBeInTheDocument();
		});

		// The server caps the log, so the last row on screen is not the first
		// redemption. Saying nothing would let it read as though it were.
		it('should say what it is showing a slice of when the log is truncated', async () => {
			mockRetrievals([aRedemption()], 8760);
			render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
			expect(await screen.findByText(/1 most recent of 8760 redemptions/)).toBeInTheDocument();
		});

		it('should not claim truncation when the whole log fits', async () => {
			mockRetrievals([aRedemption()]);
			render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
			await screen.findByText('203.0.113.9');
			expect(screen.queryByText(/most recent of/)).not.toBeInTheDocument();
		});
	});

	describe('owner reassignment control', () => {
		it('should have a free-text username/user-id field', () => {
			mockRetrievals([]);
			render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
			const input = screen.getByPlaceholderText('Username or user ID') as HTMLInputElement;
			expect(input).toBeInTheDocument();
			expect(input.type).toBe('text');
		});

		it('should show reassignment description mentioning the service account', () => {
			mockRetrievals([]);
			render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });
			expect(screen.getByText(/Transfer ownership to another user/)).toBeInTheDocument();
			// The reassignment label text should mention the account
			const label = screen.getByText(/Reassign to user/);
			expect(label.closest('div')).toHaveTextContent('svc-deploy');
		});

		it('should have a Reassign button initially disabled until a user is entered', async () => {
			mockRetrievals([]);
			const user = userEvent.setup();
			render(ServiceCodeDetailModal, { enrollment: enrollment(), now, onclosed: vi.fn() });

			const button = screen.getByRole('button', { name: 'Reassign' });
			expect(button).toBeDisabled();

			const input = screen.getByPlaceholderText('Username or user ID') as HTMLInputElement;
			await user.type(input, 'bob');

			expect(button).not.toBeDisabled();
		});
	});
});
