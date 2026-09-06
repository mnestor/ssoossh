import { render, screen } from '@testing-library/svelte';
import { afterEach, describe, expect, it, vi } from 'vitest';
import userEvent from '@testing-library/user-event';

import type {
	AccountHolder,
	EnrollmentRetrievalsResponse,
	ServiceEnrollment
} from '$lib/api/types';
import ServiceCodeDetail from './ServiceCodeDetail.svelte';

const now = new Date('2026-08-22T12:00:00Z');

/** enrollment builds a service enrollment, overriding only what a case cares about. */
function enrollment(overrides: Partial<ServiceEnrollment> = {}): ServiceEnrollment {
	return {
		id: 'enr-1234-5678',
		service_account: 'svc-deploy',
		approved_by_username: 'alice',
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

/** json wraps a body in the envelope every endpoint answers with. */
function json(data: unknown): Response {
	return new Response(JSON.stringify({ data, error: null }), {
		status: 200,
		headers: { 'Content-Type': 'application/json' }
	});
}

/** mockRetrievals stubs the two fetches the page makes: the retrieval log,
 * and the account's holders. Routed by URL rather than answered with one
 * body, because a single stub handed the holders panel the retrieval
 * envelope and it had no holders in it.
 *
 * `total` defaults to the page length — the untruncated case. */
function mockRetrievals(
	retrievals: EnrollmentRetrievalsResponse['retrievals'],
	total = retrievals.length,
	holders: AccountHolder[] = []
) {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			if (String(input).includes('/holders')) {
				return Promise.resolve(json({ service_account: 'svc-deploy', holders }));
			}
			return Promise.resolve(json({ retrievals, total }));
		})
	);
}

/** mockRetrievalsRefused answers the log fetch the way the server does for a
 * request that has no enrollment (404) or belongs to someone else (403),
 * leaving the holders fetch intact. The page keeps its own summary in that
 * case; there is just no log for it to point at. */
function mockRetrievalsRefused(status = 404) {
	vi.stubGlobal(
		'fetch',
		vi.fn((input: RequestInfo | URL) => {
			if (String(input).includes('/holders')) {
				return Promise.resolve(json({ service_account: 'svc-deploy', holders: [] }));
			}
			return Promise.resolve(
				new Response(JSON.stringify({ data: null, error: { message: 'no such enrollment' } }), {
					status,
					headers: { 'Content-Type': 'application/json' }
				})
			);
		})
	);
}

/** aRedemption is one row of the log, overridable per case. */
function aRedemption(overrides: Partial<EnrollmentRetrievalsResponse['retrievals'][0]> = {}) {
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

describe('ServiceCodeDetail', () => {
	it('should show the short form of the enrollment id', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByText('enr-1')).toBeInTheDocument();
	});

	it('should label the id rather than prefixing it with a bare hash', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByTitle('enr-1234-5678')).toHaveTextContent(/^ID/);
	});

	it('should name the account the code mints for', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });

		// Scoped to the element that names the account rather than matching
		// the text anywhere: the account appears in the key ID beneath it
		// too, so a bare text match is ambiguous.
		expect(screen.getByTestId('service-code-account')).toHaveTextContent('svc-deploy');
	});

	it('should show the lifetime of the certificates it hands out', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByText('1h')).toBeInTheDocument();
	});

	it('should say certificates last until the code expires when no lifetime is reported', () => {
		mockRetrievals([]);
		const row = enrollment({ certificate_valid_seconds: undefined });
		render(ServiceCodeDetail, { enrollment: row, now });
		expect(screen.getByText('until the code expires')).toBeInTheDocument();
	});

	it('should show the key id fixed at approval', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByText('svc-deploy/req-1')).toBeInTheDocument();
	});

	it('should show the fingerprint of the bound keypair', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByText('SHA256:abc')).toBeInTheDocument();
	});

	it('should show the granted extensions', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByText('permit-pty')).toBeInTheDocument();
	});

	// The options read as they would be written in authorized_keys, so a
	// restriction cannot be mistaken for an extension name.
	it('should show the forced command with its keyword', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByText('command=/usr/local/bin/deploy')).toBeInTheDocument();
	});

	it('should show the source address restriction with its keyword', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByText('from=198.51.100.0/24')).toBeInTheDocument();
	});

	it('should name a waived touch requirement as the extension it is', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByText('no-touch-required')).toBeInTheDocument();
	});

	it('should say when no options were fixed at approval', () => {
		mockRetrievals([]);
		const row = enrollment({ options: { extensions: [], no_touch_required: false } });
		render(ServiceCodeDetail, { enrollment: row, now });
		expect(screen.getByText(/carry the server's defaults/)).toBeInTheDocument();
	});

	// Approval and expiry are one row: the reader's question is how long the
	// code lives, which two rows made them work out for themselves.
	it('should report the validity window as one period', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByText(/Aug 20, 2026.* – .*Nov 20, 2026/)).toBeInTheDocument();
	});

	it('should report how much of the validity window is left', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByText(/left\)/)).toBeInTheDocument();
	});

	// The log below answers "has anything used this, and from where" in full,
	// so the summary that used to sit here is gone: a count above a list of
	// the same redemptions was two numbers to reconcile.
	it('should not summarise the redemptions above the log', async () => {
		mockRetrievals([aRedemption()]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		await screen.findByText('203.0.113.9');
		expect(screen.queryByText('Last redeemed')).not.toBeInTheDocument();
	});

	// A log that never arrived — a 404, or someone else's request — leaves
	// the section out entirely rather than claiming the code was never
	// redeemed. The holders panel's fetch settles on the same tick, so its
	// arrival is when the refused one has had its turn too.
	it('should leave out the history when the log did not load', async () => {
		mockRetrievalsRefused();
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		await screen.findByTestId('account-holders-empty');
		expect(screen.queryByText('Redemption history')).not.toBeInTheDocument();
	});

	// Last on the page, as on the admin's: it is the only part that grows
	// without bound, and the reader came for the facts and the controls.
	it('should put the history below the retire control', async () => {
		mockRetrievals([aRedemption()]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		const history = await screen.findByText('Redemption history');
		const position = screen.getByTestId('expire-code').compareDocumentPosition(history);
		expect(position & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
	});

	it('should report an expired code as already expired', () => {
		mockRetrievals([]);
		const row = enrollment({ expires_at: '2026-08-21T12:00:00Z' });
		render(ServiceCodeDetail, { enrollment: row, now });
		expect(screen.getByText('Expired')).toBeInTheDocument();
	});

	// Retiring a code the reader holds. An expired one needs no control: the
	// outcome it would produce is already true.
	it('should offer to retire a code that still works', () => {
		mockRetrievals([]);
		render(ServiceCodeDetail, { enrollment: enrollment(), now });
		expect(screen.getByTestId('expire-code')).toBeInTheDocument();
	});

	it('should not offer to retire a code that has already expired', () => {
		mockRetrievals([]);
		const row = enrollment({ expires_at: '2026-08-21T12:00:00Z' });
		render(ServiceCodeDetail, { enrollment: row, now });
		expect(screen.queryByTestId('expire-code')).not.toBeInTheDocument();
	});

	// The whole reason the page exists is that the code cannot be shown.
	it('should never render an enrollment code', () => {
		mockRetrievals([]);
		const row = { ...enrollment(), ...({ code: 'super-secret-code' } as object) };
		render(ServiceCodeDetail, { enrollment: row, now });
		expect(screen.queryByText(/super-secret-code/)).not.toBeInTheDocument();
	});

	describe('the retrieval log', () => {
		it('should list each redemption', async () => {
			mockRetrievals([aRedemption()]);
			render(ServiceCodeDetail, { enrollment: enrollment(), now });
			expect(await screen.findByText('203.0.113.9')).toBeInTheDocument();
		});

		// A redemption that passed code validation but failed at signing is
		// still worth surfacing: someone held the code.
		it('should mark a redemption that failed at signing', async () => {
			mockRetrievals([aRedemption({ succeeded: false })]);
			render(ServiceCodeDetail, { enrollment: enrollment(), now });
			expect(await screen.findByText('Failed')).toBeInTheDocument();
		});

		it('should say so when the code has never been retrieved', async () => {
			mockRetrievals([]);
			render(ServiceCodeDetail, { enrollment: enrollment(), now });
			expect(await screen.findByText('Never redeemed.')).toBeInTheDocument();
		});

		// The server caps the log, so the last row on screen is not the first
		// redemption. Saying nothing would let it read as though it were.
		it('should say what it is showing a slice of when the log is truncated', async () => {
			mockRetrievals([aRedemption()], 8760);
			render(ServiceCodeDetail, { enrollment: enrollment(), now });
			expect(await screen.findByText(/1 most recent of 8760 redemptions/)).toBeInTheDocument();
		});

		it('should not claim truncation when the whole log fits', async () => {
			mockRetrievals([aRedemption()]);
			render(ServiceCodeDetail, { enrollment: enrollment(), now });
			await screen.findByText('203.0.113.9');
			expect(screen.queryByText(/most recent of/)).not.toBeInTheDocument();
		});
	});

	// The address is the one thing on this page that can be changed, and it
	// exists for the cases fan-out cannot serve: an account whose holders have
	// never logged in reaches nobody.
	describe('the notification address', () => {
		it('should say who hears about the code when no address is set', () => {
			mockRetrievals([]);
			render(ServiceCodeDetail, { enrollment: enrollment({ notification_email: '' }), now });
			expect(screen.getByText(/go to everyone with access to/)).toBeInTheDocument();
		});

		it('should show the address when one is set', () => {
			mockRetrievals([]);
			render(ServiceCodeDetail, {
				enrollment: enrollment({ notification_email: 'deploys@example.com' }),
				now
			});
			expect(screen.getByTestId('notification-email-input')).toHaveValue('deploys@example.com');
		});

		// Nothing to save until the field differs from what is stored, so the
		// button cannot fire a no-op PATCH.
		it('should disable saving until the address changes', async () => {
			mockRetrievals([]);
			render(ServiceCodeDetail, {
				enrollment: enrollment({ notification_email: 'deploys@example.com' }),
				now
			});
			expect(screen.getByTestId('notification-email-save')).toBeDisabled();

			await userEvent.type(screen.getByTestId('notification-email-input'), 'x');
			expect(screen.getByTestId('notification-email-save')).toBeEnabled();
		});

		// The page renders what the server stored, not the draft: the server
		// trims, so echoing the input back would show whitespace it does not hold.
		it('should report the address the server stored', async () => {
			const fetchMock = vi.fn(() =>
				Promise.resolve(
					new Response(
						JSON.stringify({ data: { notification_email: 'deploys@example.com' }, error: null }),
						{ status: 200, headers: { 'Content-Type': 'application/json' } }
					)
				)
			);
			vi.stubGlobal('fetch', fetchMock);

			render(ServiceCodeDetail, {
				enrollment: enrollment({ notification_email: '', certificate_request_id: undefined }),
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

		// A refusal has to be visible: silently failing would leave the reader
		// believing the credential's mail had been redirected when it had not.
		it('should surface a refusal from the server', async () => {
			vi.stubGlobal(
				'fetch',
				vi.fn(() =>
					Promise.resolve(
						new Response(JSON.stringify({ data: null, error: 'not a valid email address' }), {
							status: 400,
							headers: { 'Content-Type': 'application/json' }
						})
					)
				)
			);

			render(ServiceCodeDetail, {
				enrollment: enrollment({ notification_email: '', certificate_request_id: undefined }),
				now
			});

			await userEvent.type(screen.getByTestId('notification-email-input'), 'nope');
			await userEvent.click(screen.getByTestId('notification-email-save'));

			expect(await screen.findByTestId('notification-email-error')).toBeInTheDocument();
		});
	});
});
